// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newVoteResultSource(t *testing.T, srv *httptest.Server) *VoteResultSource {
	t.Helper()
	return NewVoteResultSource(
		VoteResultSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

func voteResultEnvelope(t *testing.T, data queryVoteResultData) []byte {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	body, err := json.Marshal(queryEnvelope{Resources: []queryResource{{UID: "vote-1", Data: raw}}})
	require.NoError(t, err)
	return body
}

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestGetVoteResults_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewVoteResultSource(
		VoteResultSourceConfig{BaseURL: "", Timeout: 5 * time.Second},
		nil,
	)
	got, err := src.GetVoteResults(context.Background(), "vote-uid-1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Request parameters ───────────────────────────────────────────────────────

func TestGetVoteResults_RequestParameters(t *testing.T) {
	voteUID := "vote-abc-123"
	var capturedURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(queryEnvelope{Resources: nil})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newVoteResultSource(t, srv)
	_, err := src.GetVoteResults(context.Background(), voteUID)

	require.NoError(t, err)
	assert.Contains(t, capturedURL, "type=vote_result")
	assert.Contains(t, capturedURL, "vote_uid%3A"+voteUID)
}

// ── Choice result mapping ────────────────────────────────────────────────────

func TestGetVoteResults_MapsChoicesFromFirstQuestion(t *testing.T) {
	data := queryVoteResultData{
		NumRecipients: 10,
		NumVotesCast:  8,
		NumAbstained:  1,
		PollQuestionsResult: []queryVoteResultQuestion{
			{
				QuestionID: "q1",
				ChoiceResults: []queryVoteResultChoiceResult{
					{ChoiceID: "c1", ChoiceText: "Yes", VoteCount: 6, Percentage: 75.0},
					{ChoiceID: "c2", ChoiceText: "No", VoteCount: 2, Percentage: 25.0},
				},
			},
			{
				QuestionID: "q2",
				ChoiceResults: []queryVoteResultChoiceResult{
					{ChoiceID: "c3", ChoiceText: "Abstain", VoteCount: 1, Percentage: 100.0},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(voteResultEnvelope(t, data))
	}))
	defer srv.Close()

	src := newVoteResultSource(t, srv)
	got, err := src.GetVoteResults(context.Background(), "vote-1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 10, got.NumRecipients)
	assert.Equal(t, 8, got.NumVotesCast)
	assert.Equal(t, 1, got.NumAbstained)
	require.Len(t, got.ChoiceResults, 2, "only first question's choices")
	assert.Equal(t, "c1", got.ChoiceResults[0].ChoiceID)
	assert.Equal(t, "Yes", got.ChoiceResults[0].ChoiceText)
	assert.Equal(t, 6, got.ChoiceResults[0].VoteCount)
	assert.Equal(t, 75.0, got.ChoiceResults[0].Percentage)
	assert.Equal(t, "c2", got.ChoiceResults[1].ChoiceID)
	assert.Equal(t, "No", got.ChoiceResults[1].ChoiceText)
	assert.Equal(t, 2, got.ChoiceResults[1].VoteCount)
	assert.Equal(t, 25.0, got.ChoiceResults[1].Percentage)
}

// ── Zero results (pipeline not live) ────────────────────────────────────────

func TestGetVoteResults_EmptyResources_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(queryEnvelope{Resources: nil})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newVoteResultSource(t, srv)
	got, err := src.GetVoteResults(context.Background(), "vote-1")

	require.NoError(t, err)
	assert.Nil(t, got, "zero resources means pipeline not live — graceful nil")
}

// ── Malformed data payload → nil, no error ───────────────────────────────────

func TestGetVoteResults_MalformedData_ReturnsNil(t *testing.T) {
	body := []byte(`{"resources":[{"uid":"vote-1","data":[1,2,3]}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newVoteResultSource(t, srv)
	got, err := src.GetVoteResults(context.Background(), "vote-1")

	require.NoError(t, err)
	assert.Nil(t, got, "malformed data should degrade gracefully to nil")
}

// ── Non-2xx response → error ─────────────────────────────────────────────────

func TestGetVoteResults_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	src := newVoteResultSource(t, srv)
	got, err := src.GetVoteResults(context.Background(), "vote-1")

	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "non-2xx")
}

// ── No questions → nil choice results, participation totals present ───────────

func TestGetVoteResults_NoQuestions_ReturnsTallyWithoutChoices(t *testing.T) {
	data := queryVoteResultData{
		NumRecipients:       5,
		NumVotesCast:        3,
		NumAbstained:        2,
		PollQuestionsResult: nil,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(voteResultEnvelope(t, data))
	}))
	defer srv.Close()

	src := newVoteResultSource(t, srv)
	got, err := src.GetVoteResults(context.Background(), "vote-1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 5, got.NumRecipients)
	assert.Equal(t, 3, got.NumVotesCast)
	assert.Equal(t, 2, got.NumAbstained)
	assert.Empty(t, got.ChoiceResults)
}
