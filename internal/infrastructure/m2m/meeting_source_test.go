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

func newMeetingSource(t *testing.T, srv *httptest.Server) *MeetingSource {
	t.Helper()
	return NewMeetingSource(
		MeetingSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListMeetingsForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewMeetingSource(MeetingSourceConfig{BaseURL: ""}, nil)
	got, err := src.ListMeetingsForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Date range: uses date_field=start_time with date_from/date_to ─────────────

func TestListMeetingsForWindow_DateRangeParams(t *testing.T) {
	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[]}`))
	}))
	defer srv.Close()

	windowStart := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 29, 23, 59, 59, 0, time.UTC)

	src := newMeetingSource(t, srv)
	_, err := src.ListMeetingsForWindow(context.Background(), "c-1", windowStart, windowEnd)

	require.NoError(t, err)
	assert.Equal(t, "v1_past_meeting", capturedQuery.Get("type"))
	assert.Equal(t, "committee_uid:c-1", capturedQuery.Get("tags"))
	assert.Equal(t, "start_time", capturedQuery.Get("date_field"), "must use date_field=start_time")
	assert.Equal(t, windowStart.UTC().Format(time.RFC3339), capturedQuery.Get("date_from"))
	assert.Equal(t, windowEnd.UTC().Format(time.RFC3339), capturedQuery.Get("date_to"))
	assert.Empty(t, capturedQuery.Get("start_time[gte]"), "must not use legacy bracket notation")
	assert.Empty(t, capturedQuery.Get("start_time[lte]"), "must not use legacy bracket notation")
}

// ── Field mapping ─────────────────────────────────────────────────────────────

func TestListMeetingsForWindow_FieldMapping(t *testing.T) {
	body := []byte(`{"resources":[{` +
		`"id":"os-doc-id",` +
		`"data":{` +
		`"id":"meeting-uuid-1",` +
		`"title":"Q3 2026 LF Board Meeting",` +
		`"start_time":"2026-08-27T19:00:00Z",` +
		`"summary":"Board convened to discuss budget.",` +
		`"url":"https://meetings.lfx.linuxfoundation.org/m/1",` +
		`"private":false` +
		`}}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMeetingSource(t, srv)
	got, err := src.ListMeetingsForWindow(context.Background(), "c-1",
		time.Now().Add(-24*time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "meeting-uuid-1", got[0].UID, "UID must come from data.id, not envelope id")
	assert.Equal(t, "Q3 2026 LF Board Meeting", got[0].Title)
	assert.Equal(t, time.Date(2026, 8, 27, 19, 0, 0, 0, time.UTC), got[0].StartTime)
	assert.Equal(t, "Board convened to discuss budget.", got[0].Summary)
	assert.Equal(t, "https://meetings.lfx.linuxfoundation.org/m/1", got[0].URL)
	assert.False(t, got[0].Private)
}

// ── Private flag propagates ───────────────────────────────────────────────────

func TestListMeetingsForWindow_PrivateFlag(t *testing.T) {
	body := []byte(`{"resources":[{` +
		`"id":"os-doc-id",` +
		`"data":{"id":"m-1","title":"Private Meeting","start_time":"2026-08-27T19:00:00Z","private":true}` +
		`}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMeetingSource(t, srv)
	got, err := src.ListMeetingsForWindow(context.Background(), "c-1",
		time.Now().Add(-24*time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Private)
}

// ── Malformed data payload → record skipped ───────────────────────────────────

func TestListMeetingsForWindow_MalformedRecord_Skipped(t *testing.T) {
	body := []byte(`{"resources":[` +
		`{"id":"bad","data":[1,2,3]},` +
		`{"id":"good","data":{"id":"m-good","title":"Good Meeting","start_time":"2026-08-27T19:00:00Z","private":false}}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMeetingSource(t, srv)
	got, err := src.ListMeetingsForWindow(context.Background(), "c-1",
		time.Now().Add(-24*time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1, "malformed record must be skipped")
	assert.Equal(t, "m-good", got[0].UID)
}

// ── Non-2xx response → error ──────────────────────────────────────────────────

func TestListMeetingsForWindow_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := newMeetingSource(t, srv)
	_, err := src.ListMeetingsForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.Error(t, err)
}
