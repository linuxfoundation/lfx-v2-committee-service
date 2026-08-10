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
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// VoteResultSourceConfig configures the live vote result source. BaseURL is
// the voting service base URL (not the query service). An empty BaseURL
// disables the adapter and produces a nil result without error so deployments
// that do not wire VOTING_SERVICE_URL still generate briefs without tallies.
type VoteResultSourceConfig struct {
	BaseURL string
	Timeout time.Duration
}

// VoteResultSource is the live VoteResultSource adapter. It calls
//
//	GET {BaseURL}/votes/{voteUID}/results
//
// on the voting service to retrieve per-choice tallies for a closed vote.
// Authentication is by the same M2M *http.Client used by VoteSource.
// Per-choice tallies are not indexed by the query service because they are
// computed at runtime from individual ballot records in ITX.
type VoteResultSource struct {
	cfg    VoteResultSourceConfig
	client *http.Client
}

// NewVoteResultSource constructs a live vote result source. The supplied
// *http.Client MUST already carry M2M client_credentials Authorization.
func NewVoteResultSource(cfg VoteResultSourceConfig, client *http.Client) *VoteResultSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &VoteResultSource{cfg: cfg, client: client}
}

// voteResultChoiceCount is the per-choice response from the voting service
// results endpoint.
type voteResultChoiceCount struct {
	ChoiceID   string  `json:"choice_id"`
	VoteCount  int     `json:"vote_count"`
	Percentage float64 `json:"percentage"`
}

// voteResultQuestion is one question's results block.
type voteResultQuestion struct {
	Question struct {
		Choices []struct {
			ChoiceID   string `json:"choice_id"`
			ChoiceText string `json:"choice_text"`
		} `json:"choices"`
	} `json:"question"`
	GenericChoiceVotes []voteResultChoiceCount `json:"generic_choice_votes"`
}

// voteResultsResponse mirrors the shape of GET /votes/{uid}/results.
type voteResultsResponse struct {
	PollResults   []voteResultQuestion `json:"poll_results"`
	NumRecipients int                  `json:"num_recipients"`
	NumVotesCast  int                  `json:"num_votes_cast"`
	NumAbstained  int                  `json:"num_abstained"`
}

// GetVoteResults fetches the aggregated tally for a single closed vote from
// the voting service. Returns nil when the source is disabled (empty BaseURL)
// or the vote has no results yet.
func (v *VoteResultSource) GetVoteResults(ctx context.Context, voteUID string) (*port.VoteTally, error) {
	if v == nil || v.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "vote result source disabled: VOTING_SERVICE_URL not set")
		return nil, nil
	}

	url := appendPath(v.cfg.BaseURL, "/votes/"+voteUID+"/results")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vote result source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var raw voteResultsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode vote result source response: %w", err)
	}

	tally := &port.VoteTally{
		NumRecipients: raw.NumRecipients,
		NumVotesCast:  raw.NumVotesCast,
		NumAbstained:  raw.NumAbstained,
	}

	// Build a choice-text lookup from the first question's choices so we can
	// populate ChoiceText alongside the vote counts.
	if len(raw.PollResults) > 0 {
		first := raw.PollResults[0]
		textByID := make(map[string]string, len(first.Question.Choices))
		for _, c := range first.Question.Choices {
			textByID[c.ChoiceID] = c.ChoiceText
		}
		tally.ChoiceResults = make([]port.VoteChoiceResult, 0, len(first.GenericChoiceVotes))
		for _, cv := range first.GenericChoiceVotes {
			tally.ChoiceResults = append(tally.ChoiceResults, port.VoteChoiceResult{
				ChoiceID:   cv.ChoiceID,
				ChoiceText: textByID[cv.ChoiceID],
				VoteCount:  cv.VoteCount,
				Percentage: cv.Percentage,
			})
		}
	}

	return tally, nil
}
