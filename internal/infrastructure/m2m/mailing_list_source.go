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
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// DefaultMailingListType is the fixed query-service resource type the live
// mailing-list source queries.
const DefaultMailingListType = "v1_mailing_list_thread"

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
//	    &start_time[gte]={windowStart}&start_time[lte]={windowEnd}
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

type queryMailingListData struct {
	Subject string `json:"subject"`
	URL     string `json:"url"`
	Excerpt string `json:"excerpt"`
	Private bool   `json:"private"`
}

// ListMailingListActivityForWindow fetches mailing-list threads tagged with
// the committee UID whose start_time falls in [windowStart, windowEnd].
func (m *MailingListSource) ListMailingListActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.MailingListActivity, error) {
	if m == nil || m.cfg.BaseURL == "" {
		// Query-service URL not configured — degrade gracefully. A noisy log
		// makes this visible without breaking the generate flow.
		slog.WarnContext(ctx, "mailing list source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(m.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("type", m.cfg.Type)
	q.Set("tags", "committee:"+committeeUID)
	q.Set("start_time[gte]", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("start_time[lte]", windowEnd.UTC().Format(time.RFC3339Nano))
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

	out := make([]port.MailingListActivity, 0, len(env.Resources))
	for _, r := range env.Resources {
		var data queryMailingListData
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				slog.WarnContext(ctx, "skipping mailing list record with malformed data",
					"uid", r.UID, "error", err)
				continue
			}
		}
		out = append(out, port.MailingListActivity{
			ThreadID: r.UID,
			Subject:  data.Subject,
			URL:      data.URL,
			Excerpt:  data.Excerpt,
			Private:  data.Private,
		})
	}
	return out, nil
}
