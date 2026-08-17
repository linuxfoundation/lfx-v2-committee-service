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
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return NewMailingListSource(MailingListSourceConfig{BaseURL: u.String()}, srv.Client())
}

// ── Field mapping ─────────────────────────────────────────────────────────────

func TestListMailingListActivityForWindow_FieldMapping(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	// Use a DIFFERENT value for envelope "id" vs data "uid" to prove we read
	// ThreadID from data.uid (per the indexer contract), not the envelope id.
	body := []byte(`{"resources":[{` +
		`"id":"os-doc-id-ignored",` +
		`"data":{` +
		`"uid":"thread-uid-1",` +
		`"subject":"Re: CI pipeline update",` +
		`"url":"https://lists.example.com/msg/123",` +
		`"excerpt":"Short preview text",` +
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
	assert.Equal(t, "Re: CI pipeline update", got[0].Subject)
	assert.Equal(t, "https://lists.example.com/msg/123", got[0].URL)
	assert.Equal(t, "Short preview text", got[0].Excerpt)
	assert.True(t, got[0].Private)
}

// ── Disabled source returns nil ───────────────────────────────────────────────

func TestListMailingListActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewMailingListSource(MailingListSourceConfig{}, nil)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Malformed data payload → skipped ─────────────────────────────────────────

func TestListMailingListActivityForWindow_MalformedRecord_Skipped(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	body := []byte(`{"resources":[` +
		`{"id":"bad","data":[1,2,3]},` +
		`{"id":"good","data":{"uid":"good","subject":"Good Thread"}}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "good", got[0].ThreadID)
}

// ── Non-2xx response → error ──────────────────────────────────────────────────

func TestListMailingListActivityForWindow_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())
	require.Error(t, err)
}
