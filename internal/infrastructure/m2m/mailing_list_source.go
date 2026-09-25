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
// mailing-list source queries.
const DefaultMailingListType = "groupsio_mailing_list_message"

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

// MailingListSource is the live MailingListSource adapter. It speaks
//
//	GET {BaseURL}/query/resources?type={Type}&tags=committee:{uid}
//	    &date_field=created_at&date_from={windowStart}&date_to={windowEnd}
//
// against the query-service. Authentication is by a *http.Client returned by
// oauth2/clientcredentials (NOT the caller's bearer token).
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
	TopicID     uint64    `json:"topic_id"`
	Subject     string    `json:"subject"`
	Snippet     string    `json:"snippet"`
	GroupDomain string    `json:"group_domain"`
	GroupName   string    `json:"group_name"`
	IsPrivate   bool      `json:"is_private"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListMailingListActivityForWindow fetches groupsio messages tagged with
// the committee UID whose created_at falls in [windowStart, windowEnd],
// groups them by thread (topic_id), and returns one MailingListActivity
// per thread.
func (m *MailingListSource) ListMailingListActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.MailingListActivity, error) {
	if m == nil || m.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "mailing list source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(m.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u = u.JoinPath("query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", m.cfg.Type)
	q.Set("tags", "committee:"+committeeUID)
	q.Set("date_field", "created_at")
	q.Set("date_from", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("date_to", windowEnd.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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

	// Note: page_token pagination is not yet implemented here; this matches the
	// pattern of all other M2M sources. Because this source returns one record
	// per message (not one per thread), it reaches the default page_size cap
	// sooner than other sources on high-volume lists. A dedicated pagination PR
	// should extend queryEnvelope and follow page_token across all sources.

	// Group per-message records by topic_id, preserving first-seen order for
	// deterministic output.
	byTopic := make(map[uint64][]queryGroupsIOMessageData, len(env.Resources))
	topicOrder := make([]uint64, 0, len(env.Resources))
	for _, r := range env.Resources {
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

		// Privacy propagation: if any message in the thread is marked private the
		// entire thread is marked private in the brief. This is conservative — a
		// thread with mixed visibility must not expose private replies to non-members.
		isPrivate := false
		for _, msg := range msgs {
			if msg.IsPrivate {
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
