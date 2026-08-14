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

func newVoteSource(t *testing.T, srv *httptest.Server) *VoteSource {
	t.Helper()
	return NewVoteSource(
		VoteSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

func voteEnvelope(t *testing.T, records []queryVoteData, uids []string) []byte {
	t.Helper()
	resources := make([]queryResource, len(records))
	for i, d := range records {
		raw, err := json.Marshal(d)
		require.NoError(t, err)
		resources[i] = queryResource{UID: uids[i], Data: raw}
	}
	body, err := json.Marshal(queryEnvelope{Resources: resources})
	require.NoError(t, err)
	return body
}

// ── Field mapping: name, status, response/invitation counts ──────────────────

func TestListVoteActivityForWindow_FieldMapping(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)

	records := []queryVoteData{
		{
			Name:                          "AAIF Project Approval",
			URL:                           "https://example.com/vote/1",
			Status:                        "ended",
			NumResponseReceived:           8,
			TotalVotingRequestInvitations: 10,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(voteEnvelope(t, records, []string{"vote-uid-1"}))
	}))
	defer srv.Close()

	src := newVoteSource(t, srv)
	got, err := src.ListVoteActivityForWindow(context.Background(), "c-1", t0, t0.Add(7*24*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "vote-uid-1", got[0].VoteID)
	assert.Equal(t, "AAIF Project Approval", got[0].Name, "must use 'name' field, not 'subject'")
	assert.Equal(t, "ended", got[0].Status)
	assert.Equal(t, 8, got[0].ResponseCount, "num_response_received → ResponseCount")
	assert.Equal(t, 10, got[0].InvitationCount, "total_voting_request_invitations → InvitationCount")
}

// ── Request uses end_time filter, not start_time ──────────────────────────────

func TestListVoteActivityForWindow_UsesEndTimeFilter(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(queryEnvelope{Resources: nil})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newVoteSource(t, srv)
	_, err := src.ListVoteActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	assert.Contains(t, capturedURL, "end_time", "must filter by end_time, not start_time")
	assert.NotContains(t, capturedURL, "start_time", "must not use start_time (field does not exist on v1_vote)")
	assert.Contains(t, capturedURL, "committee%3Ac-1")
}

// ── Malformed record is skipped ───────────────────────────────────────────────

func TestListVoteActivityForWindow_MalformedRecord_Skipped(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	goodData := queryVoteData{Name: "Good Vote", Status: "ended"}
	goodJSON, _ := json.Marshal(goodData)

	body := []byte(`{"resources":[` +
		`{"uid":"bad","data":[1,2,3]},` +
		`{"uid":"good","data":` + string(goodJSON) + `}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newVoteSource(t, srv)
	got, err := src.ListVoteActivityForWindow(context.Background(), "c-1", t0, t0.Add(7*24*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "good", got[0].VoteID)
}

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListVoteActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewVoteSource(VoteSourceConfig{BaseURL: ""}, nil)
	got, err := src.ListVoteActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Non-2xx response → error ─────────────────────────────────────────────────

func TestListVoteActivityForWindow_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()

	src := newVoteSource(t, srv)
	got, err := src.ListVoteActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "non-2xx")
}
