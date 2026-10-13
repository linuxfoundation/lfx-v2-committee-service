// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// DefaultMailingListType is the fixed query-service resource type the live
// mailing-list source queries for messages.
const DefaultMailingListType = "groupsio_mailing_list_message"

const (
	// mailingListResourceType is the query-service resource type for the
	// mailing lists themselves, used to resolve a committee's lists.
	mailingListResourceType = "groupsio_mailing_list"

	// queryPageSize is the maximum page size accepted by the query-service.
	queryPageSize = "1000"

	// maxQueryPages bounds page_token pagination so a misbehaving upstream
	// cannot loop forever.
	maxQueryPages = 20

	// listTagBatchSize bounds how many mailing_list tags go into one message
	// query so the request URL stays well within proxy limits.
	listTagBatchSize = 25
)

// MailingListSourceConfig configures the live mailing-list source. All fields
// are sourced from environment variables in providers.go; an empty BaseURL
// disables the client and produces a zero-thread result without error so
// deployments that don't yet wire the query-service can still generate briefs
// from other activity alone.
type MailingListSourceConfig struct {
	BaseURL string
	Type    string
	Timeout time.Duration
}

// MailingListSource is the live MailingListSource adapter. It resolves the
// committee's mailing lists first and then fetches their messages, so the
// committee association is always read from the mailing list rather than from
// a copy stamped on each message:
//
//	GET {BaseURL}/query/resources?type=groupsio_mailing_list&tags=committee_uid:{uid}
//	GET {BaseURL}/query/resources?type={Type}&tags=mailing_list:{listUID}&tags=...
//	    &date_field=created_at&date_from={windowStart}&date_to={windowEnd}
//
// Authentication is by a *http.Client returned by oauth2/clientcredentials
// (NOT the caller's bearer token).
type MailingListSource struct {
	cfg    MailingListSourceConfig
	client *http.Client
}

// NewMailingListSource constructs a live mailing-list source. The supplied
// *http.Client MUST already carry M2M client_credentials Authorization.
// Callers wire this in providers.go via oauth2/clientcredentials.
func NewMailingListSource(cfg MailingListSourceConfig, client *http.Client) *MailingListSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Type == "" {
		cfg.Type = DefaultMailingListType
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &MailingListSource{cfg: cfg, client: client}
}

// queryGroupsIOMessageData is the per-message payload stored in the
// groupsio_mailing_list_message indexer resource's data field.
type queryGroupsIOMessageData struct {
	TopicID        uint64    `json:"topic_id"`
	Subject        string    `json:"subject"`
	Snippet        string    `json:"snippet"`
	GroupDomain    string    `json:"group_domain"`
	GroupName      string    `json:"group_name"`
	MailingListUID string    `json:"mailing_list_uid"`
	CreatedAt      time.Time `json:"created_at"`
}

// queryGroupsIOMailingListData is the subset of the groupsio_mailing_list
// indexer resource's data field needed to resolve a committee's lists and
// their visibility.
type queryGroupsIOMailingListData struct {
	UID    string `json:"uid"`
	Public bool   `json:"public"`
}

// ListMailingListActivityForWindow fetches groupsio messages for the mailing
// lists associated with the committee whose created_at falls in
// [windowStart, windowEnd], groups them by thread (topic_id), and returns one
// MailingListActivity per thread.
func (m *MailingListSource) ListMailingListActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.MailingListActivity, error) {
	if m == nil || m.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "mailing list source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	publicByList, listOrder, err := m.listCommitteeMailingLists(ctx, committeeUID)
	if err != nil {
		return nil, err
	}
	if len(listOrder) == 0 {
		return nil, nil
	}

	var messages []queryResource
	for start := 0; start < len(listOrder); start += listTagBatchSize {
		end := min(start+listTagBatchSize, len(listOrder))
		q := url.Values{}
		q.Set("type", m.cfg.Type)
		for _, uid := range listOrder[start:end] {
			q.Add("tags", "mailing_list:"+uid)
		}
		q.Set("date_field", "created_at")
		q.Set("date_from", windowStart.UTC().Format(time.RFC3339Nano))
		q.Set("date_to", windowEnd.UTC().Format(time.RFC3339Nano))

		batch, err := m.queryAll(ctx, q)
		if err != nil {
			return nil, err
		}
		messages = append(messages, batch...)
	}

	// Group per-message records by topic_id, preserving first-seen order for
	// deterministic output.
	byTopic := make(map[uint64][]queryGroupsIOMessageData, len(messages))
	topicOrder := make([]uint64, 0, len(messages))
	for _, r := range messages {
		var data queryGroupsIOMessageData
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				slog.WarnContext(ctx, "skipping mailing list record with malformed data",
					"id", r.UID, "error", err)
				continue
			}
		}
		if data.TopicID == 0 {
			slog.WarnContext(ctx, "skipping mailing list record with missing topic_id", "id", r.UID)
			continue
		}
		if data.CreatedAt.IsZero() {
			slog.WarnContext(ctx, "skipping mailing list record with missing created_at", "id", r.UID)
			continue
		}
		if _, seen := byTopic[data.TopicID]; !seen {
			topicOrder = append(topicOrder, data.TopicID)
		}
		byTopic[data.TopicID] = append(byTopic[data.TopicID], data)
	}

	out := make([]port.MailingListActivity, 0, len(byTopic))
	for _, topicID := range topicOrder {
		msgs := byTopic[topicID]

		// Sort ascending by created_at so the earliest in-window message is first.
		// time.Time.Before handles RFC3339 timestamps with any UTC offset correctly.
		//
		// Known limitation: if the thread's true opener was posted before windowStart
		// it is not returned by the query, so msgs[0] may be a reply rather than the
		// true opener. Fetching the pre-window opener would require a separate per-topic
		// API call and is deferred to a follow-up; for a 7-day brief window the impact
		// is minimal.
		sort.Slice(msgs, func(i, j int) bool {
			return msgs[i].CreatedAt.Before(msgs[j].CreatedAt)
		})

		first := msgs[0]

		// Privacy propagation: visibility comes from the owning mailing list. If any
		// message in the thread belongs to a list that is not public, the entire
		// thread is marked private in the brief. This is conservative — a thread with
		// mixed visibility must not expose private replies to non-members.
		isPrivate := false
		for _, msg := range msgs {
			if !publicByList[msg.MailingListUID] {
				isPrivate = true
				break
			}
		}

		threadURL := ""
		if first.GroupDomain != "" && first.GroupName != "" {
			threadURL = fmt.Sprintf("https://%s/g/%s/topic/%d", first.GroupDomain, first.GroupName, topicID)
		}

		out = append(out, port.MailingListActivity{
			ThreadID: fmt.Sprintf("%d", topicID),
			Subject:  first.Subject,
			URL:      threadURL,
			Excerpt:  first.Snippet,
			Private:  isPrivate,
		})
	}
	return out, nil
}

// listCommitteeMailingLists returns the committee's mailing list UIDs in
// first-seen order along with a UID → public lookup. A message whose list is
// absent from the lookup is treated as non-public by the caller.
func (m *MailingListSource) listCommitteeMailingLists(ctx context.Context, committeeUID string) (map[string]bool, []string, error) {
	q := url.Values{}
	q.Set("type", mailingListResourceType)
	q.Set("tags", "committee_uid:"+committeeUID)

	resources, err := m.queryAll(ctx, q)
	if err != nil {
		return nil, nil, err
	}

	publicByList := make(map[string]bool, len(resources))
	order := make([]string, 0, len(resources))
	for _, r := range resources {
		var data queryGroupsIOMailingListData
		if err := json.Unmarshal(r.Data, &data); err != nil {
			slog.WarnContext(ctx, "skipping mailing list record with malformed data",
				"id", r.UID, "error", err)
			continue
		}
		if data.UID == "" {
			slog.WarnContext(ctx, "skipping mailing list record with missing uid", "id", r.UID)
			continue
		}
		if _, seen := publicByList[data.UID]; !seen {
			order = append(order, data.UID)
		}
		publicByList[data.UID] = data.Public
	}
	return publicByList, order, nil
}

// queryAll runs GET /query/resources with the given parameters and follows
// page_token until exhausted (bounded by maxQueryPages).
func (m *MailingListSource) queryAll(ctx context.Context, params url.Values) ([]queryResource, error) {
	u, err := url.Parse(m.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u = u.JoinPath("query/resources")

	var all []queryResource
	pageToken := ""
	for page := 0; page < maxQueryPages; page++ {
		q := url.Values{}
		for k, v := range params {
			q[k] = v
		}
		q.Set("v", "1")
		q.Set("page_size", queryPageSize)
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		u.RawQuery = q.Encode()

		env, err := m.fetch(ctx, u.String())
		if err != nil {
			return nil, err
		}
		all = append(all, env.Resources...)
		if env.PageToken == "" {
			return all, nil
		}
		pageToken = env.PageToken
	}
	slog.WarnContext(ctx, "mailing list source reached page limit, results truncated",
		"type", params.Get("type"), "max_pages", maxQueryPages)
	return all, nil
}

func (m *MailingListSource) fetch(ctx context.Context, target string) (*queryEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build mailing-list-source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mailing list source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mailing list source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode mailing-list-source response: %w", err)
	}
	return &env, nil
}
