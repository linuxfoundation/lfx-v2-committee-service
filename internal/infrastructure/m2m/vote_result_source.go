// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// DefaultVoteResultType is the query-service resource type for indexed vote
// results. This becomes available once the vote_result indexer pipeline is live
// (ITX PollResults table → lfx-v1-sync-helper → lfx-v2-voting-service →
// OpenSearch). Until then, queries return zero results and the brief degrades
// gracefully to participation counts.
const DefaultVoteResultType = "vote_result"

// VoteResultSourceConfig configures the query-service-backed vote result
// source. BaseURL is the query-service base URL (the same QUERY_SERVICE_URL
// used by VoteSource). An empty BaseURL disables the adapter and returns nil
// without error so deployments without the query service still generate briefs.
type VoteResultSourceConfig struct {
	BaseURL string
	Type    string
	Timeout time.Duration
}

// VoteResultSource fetches indexed vote tallies from the query service.
// It queries the `vote_result` resource type (tagged by vote_uid) rather than
// calling the voting service REST API directly, so brief generation no longer
// has a runtime dependency on ITX.
//
// Graceful degrade: when BaseURL is empty, or when the `vote_result` resource
// type is not yet indexed (pipeline not live), GetVoteResults returns (nil,nil)
// and the generator falls back to participation-count-only claim labels.
type VoteResultSource struct {
	cfg    VoteResultSourceConfig
	client *http.Client
}

// NewVoteResultSource constructs the query-service-backed vote result source.
// The supplied *http.Client MUST already carry M2M client_credentials
// Authorization. Callers wire this in providers.go.
func NewVoteResultSource(cfg VoteResultSourceConfig, client *http.Client) *VoteResultSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Type == "" {
		cfg.Type = DefaultVoteResultType
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &VoteResultSource{cfg: cfg, client: client}
}

// queryVoteResultChoiceResult is one choice's tally from the indexed
// vote_result resource.
type queryVoteResultChoiceResult struct {
	ChoiceID   string  `json:"choice_id"`
	ChoiceText string  `json:"choice_text"`
	VoteCount  int     `json:"vote_count"`
	Percentage float64 `json:"percentage"`
}

// queryVoteResultQuestion is one question's indexed results block.
type queryVoteResultQuestion struct {
	QuestionID    string                        `json:"question_id"`
	ChoiceResults []queryVoteResultChoiceResult `json:"choice_results"`
}

// queryVoteResultData is the data payload of a `vote_result` resource returned
// by the query service. Field names match the PollResults DynamoDB table that
// the lfx-v1-sync-helper syncs into the v1-objects NATS KV bucket.
type queryVoteResultData struct {
	NumRecipients       int                       `json:"num_recipients"`
	NumVotesCast        int                       `json:"num_votes_cast"`
	NumAbstained        int                       `json:"num_abstained"`
	PollQuestionsResult []queryVoteResultQuestion `json:"poll_questions_result"`
}

// GetVoteResults fetches the indexed tally for a single vote from the query
// service. Returns nil when the source is disabled (empty BaseURL), when the
// vote_result pipeline is not yet live (zero results), or when the vote has no
// results yet — all treated as graceful degrade.
func (v *VoteResultSource) GetVoteResults(ctx context.Context, voteUID string) (*port.VoteTally, error) {
	if v == nil || v.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "vote result source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(v.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL for vote result: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", v.cfg.Type)
	q.Set("tags", "vote_uid:"+voteUID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build vote-result-source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vote result source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("vote result source returned non-2xx: status=%d", resp.StatusCode)
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode vote result source response: %w", err)
	}

	// No results yet — vote_result pipeline not live or vote has no results.
	if len(env.Resources) == 0 {
		return nil, nil
	}

	var data queryVoteResultData
	if len(env.Resources[0].Data) > 0 {
		if err := json.Unmarshal(env.Resources[0].Data, &data); err != nil {
			slog.WarnContext(ctx, "vote result source: malformed data payload; skipping tally",
				"vote_uid", voteUID, "error", err)
			return nil, nil
		}
	}

	tally := &port.VoteTally{
		NumRecipients: data.NumRecipients,
		NumVotesCast:  data.NumVotesCast,
		NumAbstained:  data.NumAbstained,
	}

	if len(data.PollQuestionsResult) > 0 {
		first := data.PollQuestionsResult[0]
		tally.ChoiceResults = make([]port.VoteChoiceResult, 0, len(first.ChoiceResults))
		for _, c := range first.ChoiceResults {
			tally.ChoiceResults = append(tally.ChoiceResults, port.VoteChoiceResult{
				ChoiceID:   c.ChoiceID,
				ChoiceText: c.ChoiceText,
				VoteCount:  c.VoteCount,
				Percentage: c.Percentage,
			})
		}
	}

	return tally, nil
}
