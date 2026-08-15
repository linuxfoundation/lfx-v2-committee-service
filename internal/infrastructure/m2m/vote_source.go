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

// DefaultVoteType is the fixed query-service resource type the live vote
// source queries.
const DefaultVoteType = "v1_vote"

// VoteSourceConfig configures the live vote source. All fields are sourced
// from environment variables in providers.go; an empty BaseURL disables the
// client and produces a zero-vote result without error so deployments that
// don't yet wire the query-service can still generate briefs from other
// activity alone.
type VoteSourceConfig struct {
	BaseURL string
	Type    string
	Timeout time.Duration
}

// VoteSource is the live VoteSource adapter. It speaks
//
//	GET {BaseURL}/query/resources?type={Type}&tags=committee:{uid}
//	    &date_field=end_time&date_from={windowStart}&date_to={windowEnd}
//
// against the query-service. Authentication is by a *http.Client returned by
// oauth2/clientcredentials (NOT the caller's bearer token).
type VoteSource struct {
	cfg    VoteSourceConfig
	client *http.Client
}

// NewVoteSource constructs a live vote source. The supplied *http.Client MUST
// already carry M2M client_credentials Authorization. Callers wire this in
// providers.go via oauth2/clientcredentials.
func NewVoteSource(cfg VoteSourceConfig, client *http.Client) *VoteSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Type == "" {
		cfg.Type = DefaultVoteType
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &VoteSource{cfg: cfg, client: client}
}

type queryVoteData struct {
	// Name is the poll display name. The v1_vote indexer field is "name", not
	// "subject" — using "subject" silently produced empty vote names.
	Name                          string `json:"name"`
	URL                           string `json:"url"`
	Status                        string `json:"status"`
	NumResponseReceived           int    `json:"num_response_received"`
	TotalVotingRequestInvitations int    `json:"total_voting_request_invitations"`
}

// ListVoteActivityForWindow fetches votes tagged with the committee UID whose
// start_time falls in [windowStart, windowEnd].
func (v *VoteSource) ListVoteActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.VoteActivity, error) {
	if v == nil || v.cfg.BaseURL == "" {
		// Query-service URL not configured — degrade gracefully. A noisy log
		// makes this visible without breaking the generate flow.
		slog.WarnContext(ctx, "vote source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(v.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", v.cfg.Type)
	q.Set("tags", "committee:"+committeeUID)
	// Filter by end_time: we want votes that closed within the window.
	// The v1_vote resource has no start_time field; end_time is the close date.
	q.Set("date_field", "end_time")
	q.Set("date_from", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("date_to", windowEnd.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build vote-source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vote source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vote source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode vote-source response: %w", err)
	}

	out := make([]port.VoteActivity, 0, len(env.Resources))
	for _, r := range env.Resources {
		var data queryVoteData
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				slog.WarnContext(ctx, "skipping vote record with malformed data",
					"uid", r.UID, "error", err)
				continue
			}
		}
		out = append(out, port.VoteActivity{
			VoteID:          r.UID,
			Name:            data.Name,
			URL:             data.URL,
			Status:          data.Status,
			ResponseCount:   data.NumResponseReceived,
			InvitationCount: data.TotalVotingRequestInvitations,
		})
	}
	return out, nil
}
