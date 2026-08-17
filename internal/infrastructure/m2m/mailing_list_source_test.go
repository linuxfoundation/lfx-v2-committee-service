// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMailingListSource(t *testing.T, srv *httptest.Server) *MailingListSource {
	t.Helper()
	return NewMailingListSource(MailingListSourceConfig{
		BaseURL: srv.URL,
		Type:    DefaultMailingListType,
	}, srv.Client())
}

// ── Field mapping: ThreadID comes from data.uid, not envelope id ─────────────
//
// Use a DIFFERENT value for envelope "id" vs data "uid" to prove we read
// ThreadID from data.uid (per the indexer contract), not the envelope id.

func TestListMailingListActivityForWindow_FieldMapping(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	body := []byte(`{"resources":[{` +
		`"id":"os-doc-id-ignored",` +
		`"data":{` +
		`"uid":"thread-uid-1",` +
		`"subject":"Committee governance update",` +
		`"url":"https://lists.example.org/thread/abc123",` +
		`"excerpt":"We should revise the charter...",` +
		`"private":true` +
		`}}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "thread-uid-1", got[0].ThreadID, "ThreadID must come from data.uid, not the envelope top-level id")
	assert.Equal(t, "Committee governance update", got[0].Subject)
	assert.Equal(t, "https://lists.example.org/thread/abc123", got[0].URL)
	assert.Equal(t, "We should revise the charter...", got[0].Excerpt)
	assert.True(t, got[0].Private)
}

// ── Query parameters ──────────────────────────────────────────────────────────

func TestListMailingListActivityForWindow_QueryParameters(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[]}`))
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-target", windowStart, windowEnd)

	require.NoError(t, err)
	assert.Equal(t, DefaultMailingListType, capturedQuery.Get("type"))
	assert.Equal(t, "committee:c-target", capturedQuery.Get("tags"))
	assert.Equal(t, windowStart.UTC().Format(time.RFC3339Nano), capturedQuery.Get("start_time[gte]"))
	assert.Equal(t, windowEnd.UTC().Format(time.RFC3339Nano), capturedQuery.Get("start_time[lte]"))
}

// ── Malformed data payload → record skipped ───────────────────────────────────

func TestListMailingListActivityForWindow_MalformedRecord_Skipped(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	body := []byte(`{"resources":[` +
		`{"id":"bad","data":[1,2,3]},` +
		`{"id":"good","data":{"uid":"thread-uid-2","subject":"Good thread","private":false}}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1, "malformed record must be skipped, good record must pass through")
	assert.Equal(t, "thread-uid-2", got[0].ThreadID)
}

// ── Disabled source (empty BaseURL) → nil, no error ──────────────────────────

func TestListMailingListActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewMailingListSource(MailingListSourceConfig{BaseURL: ""}, nil)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	assert.Nil(t, got)
}
