// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package m2m holds service-to-service (machine-to-machine) clients used by
// the weekly-brief generator to fetch source material from other LFX services.
//
// M2M is the LFX convention for service-brokered authentication: this service
// holds a client_credentials grant of its OWN, and uses it to fetch data from
// other services on behalf of (but NOT delegating) the caller's identity. The
// caller's bearer token is never forwarded — meeting access is authorised at
// the source by service identity, with FGA-level checks applied to which
// committees this service is permitted to read.
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

// MeetingSourceConfig configures the live meeting source. All fields are sourced
// from environment variables in providers.go; an empty BaseURL disables the
// client and produces a zero-meeting result without error so deployments that
// don't yet wire the query-service can still generate briefs from member
// activity alone.
type MeetingSourceConfig struct {
	BaseURL string
	Timeout time.Duration
}

// MeetingSource is the live MeetingSource adapter. It speaks
//
//	GET {BaseURL}/query/resources?type=v1_past_meeting&tags=committee:{uid}
//	    &start_time[gte]={windowStart}&start_time[lte]={windowEnd}
//
// against the query-service. Authentication is by a *http.Client returned by
// oauth2/clientcredentials (NOT the caller's bearer token).
type MeetingSource struct {
	cfg    MeetingSourceConfig
	client *http.Client
}

// NewMeetingSource constructs a live meeting source. The supplied *http.Client
// MUST already carry M2M client_credentials Authorization. Callers wire this in
// providers.go via oauth2/clientcredentials.
func NewMeetingSource(cfg MeetingSourceConfig, client *http.Client) *MeetingSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &MeetingSource{cfg: cfg, client: client}
}

// queryResource is the loose envelope returned by the query-service. Only the
// fields we care about are pulled out; unknown attributes are ignored.
type queryResource struct {
	UID  string          `json:"uid"`
	Data json.RawMessage `json:"data"`
}

type queryEnvelope struct {
	Resources []queryResource `json:"resources"`
}

type queryMeetingData struct {
	Title     string `json:"title"`
	StartTime string `json:"start_time"`
	Summary   string `json:"summary"`
	URL       string `json:"url"`
	Private   bool   `json:"private"`
}

// ListMeetingsForWindow fetches past meetings tagged with the committee UID
// whose start_time falls in [windowStart, windowEnd].
func (m *MeetingSource) ListMeetingsForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.MeetingActivity, error) {
	if m == nil || m.cfg.BaseURL == "" {
		// Query-service URL not configured — degrade gracefully. A noisy log
		// makes this visible without breaking the generate flow.
		slog.WarnContext(ctx, "meeting source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(m.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("type", "v1_past_meeting")
	q.Set("tags", "committee:"+committeeUID)
	q.Set("start_time[gte]", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("start_time[lte]", windowEnd.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build meeting-source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meeting source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("meeting source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode meeting-source response: %w", err)
	}

	out := make([]port.MeetingActivity, 0, len(env.Resources))
	for _, r := range env.Resources {
		var data queryMeetingData
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				slog.WarnContext(ctx, "skipping meeting record with malformed data",
					"uid", r.UID, "error", err)
				continue
			}
		}
		var start time.Time
		if data.StartTime != "" {
			if t, err := time.Parse(time.RFC3339, data.StartTime); err == nil {
				start = t
			}
		}
		out = append(out, port.MeetingActivity{
			UID:       r.UID,
			Title:     data.Title,
			StartTime: start,
			Summary:   data.Summary,
			URL:       data.URL,
			Private:   data.Private,
		})
	}
	return out, nil
}

// appendPath joins two URL path components with exactly one slash separating them.
func appendPath(base, extra string) string {
	if base == "" {
		return extra
	}
	if base[len(base)-1] == '/' && len(extra) > 0 && extra[0] == '/' {
		return base + extra[1:]
	}
	if base[len(base)-1] != '/' && (len(extra) == 0 || extra[0] != '/') {
		return base + "/" + extra
	}
	return base + extra
}
