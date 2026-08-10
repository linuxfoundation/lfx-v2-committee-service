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

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

func boolPtr(b bool) *bool { return &b }

// summaryResponse builds a minimal query-service envelope with the given records.
func summaryResponse(t *testing.T, records []querySummaryData) []byte {
	t.Helper()
	var resources []queryResource
	for _, d := range records {
		raw, err := json.Marshal(d)
		require.NoError(t, err)
		resources = append(resources, queryResource{UID: d.MeetingAndOccurrenceID, Data: raw})
	}
	body, err := json.Marshal(queryEnvelope{Resources: resources})
	require.NoError(t, err)
	return body
}

func newSummarySource(t *testing.T, srv *httptest.Server) *MeetingAISummarySource {
	t.Helper()
	return NewMeetingAISummarySource(
		MeetingAISummarySourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

// ── Approval gate ────────────────────────────────────────────────────────────

func TestListAISummariesForWindow_ApprovalGate(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)

	records := []querySummaryData{
		{
			MeetingAndOccurrenceID: "m1",
			SummaryTitle:           "Approved Summary",
			SummaryStartTime:       t0.Format(time.RFC3339Nano),
			Content:                "Content A",
			RequiresApproval:       boolPtr(true),
			Approved:               true,
			AISummaryAccess:        "public",
		},
		{
			MeetingAndOccurrenceID: "m2",
			SummaryTitle:           "Pending Summary",
			SummaryStartTime:       t0.Add(time.Hour).Format(time.RFC3339Nano),
			Content:                "Content B",
			RequiresApproval:       boolPtr(true),
			Approved:               false, // not approved — must be filtered out
			AISummaryAccess:        "private",
		},
		{
			MeetingAndOccurrenceID: "m3",
			SummaryTitle:           "Auto-Approved",
			SummaryStartTime:       t0.Add(2 * time.Hour).Format(time.RFC3339Nano),
			Content:                "Content C",
			RequiresApproval:       boolPtr(false), // auto-approved: no human review needed
			Approved:               false,
			AISummaryAccess:        "members",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(summaryResponse(t, records))
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		t0.Add(-time.Hour), t0.Add(4*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 2, "only approved and auto-approved summaries should be returned")

	ids := []string{got[0].MeetingAndOccurrenceID, got[1].MeetingAndOccurrenceID}
	assert.Contains(t, ids, "m1")
	assert.Contains(t, ids, "m3")
	for _, id := range ids {
		assert.NotEqual(t, "m2", id, "pending summary must be filtered out")
	}
}

// ── Private derivation ───────────────────────────────────────────────────────

func TestListAISummariesForWindow_PrivateDerivation(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)

	records := []querySummaryData{
		{
			MeetingAndOccurrenceID: "pub",
			SummaryTitle:           "Public Summary",
			SummaryStartTime:       t0.Format(time.RFC3339Nano),
			Content:                "Public content",
			RequiresApproval:       boolPtr(false),
			Approved:               false,
			AISummaryAccess:        "public",
		},
		{
			MeetingAndOccurrenceID: "priv",
			SummaryTitle:           "Private Summary",
			SummaryStartTime:       t0.Add(time.Hour).Format(time.RFC3339Nano),
			Content:                "Private content",
			RequiresApproval:       boolPtr(false),
			Approved:               false,
			AISummaryAccess:        "members",
		},
		{
			MeetingAndOccurrenceID: "empty-access",
			SummaryTitle:           "Empty Access",
			SummaryStartTime:       t0.Add(2 * time.Hour).Format(time.RFC3339Nano),
			Content:                "Some content",
			RequiresApproval:       boolPtr(false),
			Approved:               false,
			AISummaryAccess:        "", // missing → private by default
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(summaryResponse(t, records))
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		t0.Add(-time.Hour), t0.Add(4*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 3)

	byID := make(map[string]port.MeetingAISummaryActivity, len(got))
	for _, g := range got {
		byID[g.MeetingAndOccurrenceID] = g
	}

	assert.False(t, byID["pub"].Private, "ai_summary_access=public → Private=false")
	assert.True(t, byID["priv"].Private, "ai_summary_access=members → Private=true")
	assert.True(t, byID["empty-access"].Private, "empty ai_summary_access → Private=true")
}

// ── Title fallback ───────────────────────────────────────────────────────────

func TestListAISummariesForWindow_TitleFallback(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)

	records := []querySummaryData{
		{
			MeetingAndOccurrenceID: "m-with-title",
			SummaryTitle:           "My Custom Title",
			ZoomMeetingTopic:       "Zoom Topic (ignored)",
			SummaryStartTime:       t0.Format(time.RFC3339Nano),
			RequiresApproval:       boolPtr(false),
		},
		{
			MeetingAndOccurrenceID: "m-no-title",
			SummaryTitle:           "",
			ZoomMeetingTopic:       "Zoom Topic Used",
			SummaryStartTime:       t0.Add(time.Hour).Format(time.RFC3339Nano),
			RequiresApproval:       boolPtr(false),
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(summaryResponse(t, records))
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		t0.Add(-time.Hour), t0.Add(4*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := make(map[string]port.MeetingAISummaryActivity, len(got))
	for _, g := range got {
		byID[g.MeetingAndOccurrenceID] = g
	}

	assert.Equal(t, "My Custom Title", byID["m-with-title"].Title)
	assert.Equal(t, "Zoom Topic Used", byID["m-no-title"].Title)
}

// ── Empty window ─────────────────────────────────────────────────────────────

func TestListAISummariesForWindow_EmptyWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(queryEnvelope{Resources: nil})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Empty(t, got)
}

// ── HTTP error → graceful degrade ─────────────────────────────────────────────

func TestListAISummariesForWindow_HTTPError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "non-2xx")
}

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListAISummariesForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewMeetingAISummarySource(
		MeetingAISummarySourceConfig{BaseURL: "", Timeout: 5 * time.Second},
		nil,
	)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Query parameters ─────────────────────────────────────────────────────────

func TestListAISummariesForWindow_QueryParameters(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)
	committeeUID := "c-abc-123"

	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(queryEnvelope{Resources: nil})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	_, err := src.ListAISummariesForWindow(context.Background(), committeeUID, windowStart, windowEnd)

	require.NoError(t, err)
	assert.Contains(t, capturedURL, "type=v1_past_meeting_summary")
	assert.Contains(t, capturedURL, "tags=committee%3A"+committeeUID)
	assert.Contains(t, capturedURL, "summary_start_time")
}

// ── Malformed data record is skipped ─────────────────────────────────────────

func TestListAISummariesForWindow_MalformedRecord_Skipped(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	validRecord := querySummaryData{
		MeetingAndOccurrenceID: "good",
		SummaryTitle:           "Good Summary",
		SummaryStartTime:       t0.Format(time.RFC3339Nano),
		Content:                "Good content",
		RequiresApproval:       boolPtr(false),
	}
	goodJSON, _ := json.Marshal(validRecord)

	// The "bad" record has valid JSON at the envelope level but an array (not
	// an object) in the data field, which fails json.Unmarshal into querySummaryData.
	// This exercises the "skip malformed record" branch in the adapter.
	body := []byte(`{"resources":[{"uid":"bad","data":[1,2,3]},{"uid":"good","data":` +
		string(goodJSON) + `}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		t0.Add(-time.Hour), t0.Add(2*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "good", got[0].MeetingAndOccurrenceID)
}

// ── Missing/null data payload is skipped (not treated as auto-approved) ────────

func TestListAISummariesForWindow_MissingDataPayload_Skipped(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	validRecord := querySummaryData{
		MeetingAndOccurrenceID: "good",
		SummaryTitle:           "Good Summary",
		SummaryStartTime:       t0.Format(time.RFC3339Nano),
		Content:                "Good content",
		RequiresApproval:       boolPtr(true),
		Approved:               true,
	}
	goodJSON, _ := json.Marshal(validRecord)

	// One record has a null data field (zero-value RequiresApproval=false would
	// otherwise pass the approval gate as auto-approved — must be rejected).
	body := []byte(`{"resources":[{"uid":"missing-data"},{"uid":"null-data","data":null},{"uid":"good","data":` +
		string(goodJSON) + `}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		t0.Add(-time.Hour), t0.Add(2*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1, "only the record with valid approval data should be returned")
	assert.Equal(t, "good", got[0].MeetingAndOccurrenceID)
}

// ── Absent requires_approval field is not treated as auto-approved ────────────

func TestListAISummariesForWindow_AbsentRequiresApproval_NotAutoApproved(t *testing.T) {
	t0 := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)

	// "{}" is a valid, non-null JSON object. It was previously vulnerable: the
	// zero-value bool RequiresApproval=false would pass the approval gate as
	// auto-approved. Now that RequiresApproval is *bool, absent means nil and
	// the record is treated as unapproved rather than auto-approved.
	absentRAJSON := []byte(`{"uid":"absent-ra","data":{}}`)
	// A record with approved=true but missing requires_approval should still pass.
	approvedNoRAJSON := []byte(`{"uid":"approved-no-ra","data":{"meeting_and_occurrence_id":"approved-no-ra","approved":true}}`)

	body := []byte(`{"resources":[` + string(absentRAJSON) + `,` + string(approvedNoRAJSON) + `]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSummarySource(t, srv)
	got, err := src.ListAISummariesForWindow(context.Background(), "c-1",
		t0.Add(-time.Hour), t0.Add(2*time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1, "record with absent requires_approval and approved=false must be filtered; approved=true must pass")
	assert.Equal(t, "approved-no-ra", got[0].MeetingAndOccurrenceID)
}
