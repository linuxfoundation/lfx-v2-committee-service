// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSurveySource(t *testing.T, srv *httptest.Server) *SurveySource {
	t.Helper()
	return NewSurveySource(
		SurveySourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

func surveyEnvelope(t *testing.T, records []querySurveyData, uids []string) []byte {
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

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListSurveyActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewSurveySource(SurveySourceConfig{BaseURL: ""}, nil)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Request parameters ────────────────────────────────────────────────────────

func TestListSurveyActivityForWindow_RequestParameters(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)
	committeeUID := "committee-abc"

	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(queryEnvelope{Resources: nil})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	_, err := src.ListSurveyActivityForWindow(context.Background(), committeeUID, windowStart, windowEnd)

	require.NoError(t, err)
	assert.Equal(t, "survey", capturedQuery.Get("type"))
	assert.Equal(t, "committee_uid:"+committeeUID, capturedQuery.Get("tags"))
	assert.Equal(t, "survey_cutoff_date", capturedQuery.Get("date_field"))
	assert.Equal(t, windowStart.UTC().Format(time.RFC3339Nano), capturedQuery.Get("date_from"))
	assert.Equal(t, windowEnd.UTC().Format(time.RFC3339Nano), capturedQuery.Get("date_to"))
}

// ── Field mapping and per-committee counts ────────────────────────────────────

func TestListSurveyActivityForWindow_FieldMapping(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)
	cutoff := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	records := []querySurveyData{
		{
			UID:          "survey-uid-1",
			Title:        "Developer Experience Survey",
			Status:       "closed",
			CutoffDate:   cutoff.Format(time.RFC3339),
			CollectorURL: "https://www.surveymonkey.com/r/ABC123",
			Committees: []querySurveyCommittee{
				{CommitteeUID: "other-committee", TotalRecipients: 50, TotalResponses: 30},
				{CommitteeUID: "c-target", TotalRecipients: 20, TotalResponses: 14},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(surveyEnvelope(t, records, []string{"survey-uid-1"}))
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-target", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "survey-uid-1", got[0].SurveyUID)
	assert.Equal(t, "Developer Experience Survey", got[0].Title)
	assert.Equal(t, "closed", got[0].Status)
	assert.Equal(t, cutoff.UTC(), got[0].CutoffDate)
	assert.Equal(t, "https://www.surveymonkey.com/r/ABC123", got[0].URL)
	assert.Equal(t, 20, got[0].TotalRecipients, "must use per-committee count, not global")
	assert.Equal(t, 14, got[0].TotalResponses, "must use per-committee count, not global")
}

// ── Client-side cutoff date filter ───────────────────────────────────────────

func TestListSurveyActivityForWindow_FiltersOutOfWindowSurveys(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	records := []querySurveyData{
		{
			Title:      "In Window",
			Status:     "closed",
			CutoffDate: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Committees: []querySurveyCommittee{{CommitteeUID: "c-1", TotalRecipients: 10, TotalResponses: 5}},
		},
		{
			Title:      "Before Window",
			Status:     "closed",
			CutoffDate: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Committees: []querySurveyCommittee{{CommitteeUID: "c-1", TotalRecipients: 10, TotalResponses: 5}},
		},
		{
			Title:      "After Window",
			Status:     "closed",
			CutoffDate: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Committees: []querySurveyCommittee{{CommitteeUID: "c-1", TotalRecipients: 10, TotalResponses: 5}},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(surveyEnvelope(t, records, []string{"s-1", "s-2", "s-3"}))
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "In Window", got[0].Title)
}

// ── Committee not found → zero counts, still returned ────────────────────────

func TestListSurveyActivityForWindow_CommitteeNotInArray_ZeroCounts(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	records := []querySurveyData{
		{
			Title:      "Survey Without This Committee",
			Status:     "closed",
			CutoffDate: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Committees: []querySurveyCommittee{
				{CommitteeUID: "different-committee", TotalRecipients: 10, TotalResponses: 8},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(surveyEnvelope(t, records, []string{"s-1"}))
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-target", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].TotalRecipients)
	assert.Equal(t, 0, got[0].TotalResponses)
}

// ── Malformed record is skipped ───────────────────────────────────────────────

func TestListSurveyActivityForWindow_MalformedRecord_Skipped(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)
	goodData := querySurveyData{
		UID:        "good",
		Title:      "Good Survey",
		Status:     "closed",
		CutoffDate: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Committees: []querySurveyCommittee{{CommitteeUID: "c-1"}},
	}
	goodJSON, _ := json.Marshal(goodData)

	body := []byte(`{"resources":[` +
		`{"id":"bad","data":[1,2,3]},` +
		`{"id":"good","data":` + string(goodJSON) + `}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "good", got[0].SurveyUID)
}

// ── Non-2xx response → error ──────────────────────────────────────────────────

func TestListSurveyActivityForWindow_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "non-2xx")
}

// ── Unparseable cutoff date is skipped ───────────────────────────────────────

func TestListSurveyActivityForWindow_UnparseableCutoffDate_Skipped(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	records := []querySurveyData{
		{Title: "Bad Date Survey", Status: "closed", CutoffDate: "not-a-date"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(surveyEnvelope(t, records, []string{"s-1"}))
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	assert.Empty(t, got)
}

// ── Production-shaped fixture: SurveyUID comes from data.uid, not envelope id ──
//
// The survey-service indexer contract defines data.uid as the v2 UUID and
// data.id as the v1 identifier. The envelope's top-level "id" is the OpenSearch
// document ID (set to data.uid by the indexer, but not the authoritative source).
// This test uses DIFFERENT values for the envelope "id" and data "uid" so it
// definitively proves we read from data.uid.

func TestListSurveyActivityForWindow_ProductionShape_UIDDecodedFromDataUID(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)
	cutoff := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	// Use a distinct value for the envelope "id" vs data "uid" so the assertion
	// can only pass if we read from data.uid (as the indexer contract requires).
	body := []byte(`{"resources":[{` +
		`"id":"os-doc-id-ignored",` +
		`"data":{` +
		`"uid":"survey-uid-1",` +
		`"survey_title":"Dev Experience Survey",` +
		`"survey_status":"closed",` +
		`"survey_cutoff_date":"` + cutoff.UTC().Format(time.RFC3339) + `",` +
		`"collector_url":"https://www.surveymonkey.com/r/ABC123",` +
		`"committees":[{"committee_uid":"c-target","total_recipients":20,"total_responses":14}]` +
		`}}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newSurveySource(t, srv)
	got, err := src.ListSurveyActivityForWindow(context.Background(), "c-target", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "survey-uid-1", got[0].SurveyUID, "SurveyUID must come from data.uid, not the envelope top-level id")
	assert.Equal(t, "Dev Experience Survey", got[0].Title)
	assert.Equal(t, 20, got[0].TotalRecipients)
	assert.Equal(t, 14, got[0].TotalResponses)
}
