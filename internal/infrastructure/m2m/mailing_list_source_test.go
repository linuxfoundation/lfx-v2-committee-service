// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMailingListSource(t *testing.T, srv *httptest.Server) *MailingListSource {
	t.Helper()
	return NewMailingListSource(
		MailingListSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListMailingListActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewMailingListSource(MailingListSourceConfig{BaseURL: ""}, nil)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Field mapping: ThreadID comes from topic_id, Excerpt from snippet ─────────

func TestListMailingListActivityForWindow_FieldMapping(t *testing.T) {
	body := []byte(`{"resources":[{` +
		`"id":"os-doc-id-269273631",` +
		`"data":{` +
		`"topic_id":120939422,` +
		`"subject":"Please review this PR",` +
		`"snippet":"Hello folks!",` +
		`"group_domain":"lists.sonicfoundation.dev",` +
		`"group_name":"dev",` +
		`"is_private":false,` +
		`"created_at":"2026-08-26T15:33:36Z"` +
		`}}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "120939422", got[0].ThreadID)
	assert.Equal(t, "Please review this PR", got[0].Subject)
	assert.Equal(t, "Hello folks!", got[0].Excerpt)
	assert.Equal(t, "https://lists.sonicfoundation.dev/g/dev/topic/120939422", got[0].URL)
	assert.False(t, got[0].Private)
}

// ── Multiple messages in the same thread → one MailingListActivity ────────────

func TestListMailingListActivityForWindow_GroupsByTopicID(t *testing.T) {
	// Three messages: two in topic 111, one in topic 222. The earlier message
	// in topic 111 (msg-1) must be the thread opener — subject and snippet
	// come from it.
	body := []byte(`{"resources":[` +
		`{"id":"msg-2","data":{"topic_id":111,"subject":"Re: Topic A","snippet":"Reply text","group_domain":"lists.example.org","group_name":"dev","is_private":false,"created_at":"2026-08-26T12:00:00Z"}},` +
		`{"id":"msg-1","data":{"topic_id":111,"subject":"Topic A","snippet":"Original text","group_domain":"lists.example.org","group_name":"dev","is_private":false,"created_at":"2026-08-26T11:00:00Z"}},` +
		`{"id":"msg-3","data":{"topic_id":222,"subject":"Topic B","snippet":"Other thread","group_domain":"lists.example.org","group_name":"dev","is_private":false,"created_at":"2026-08-26T13:00:00Z"}}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 2, "two topics → two activities")

	// Topic 111 is encountered first in the response — it comes first in output.
	assert.Equal(t, "111", got[0].ThreadID)
	assert.Equal(t, "Topic A", got[0].Subject, "opener (earliest created_at) determines subject")
	assert.Equal(t, "Original text", got[0].Excerpt)

	assert.Equal(t, "222", got[1].ThreadID)
}

// ── Privacy: any private message in a thread marks the whole thread private ───

func TestListMailingListActivityForWindow_PrivacyPropagation(t *testing.T) {
	body := []byte(`{"resources":[` +
		`{"id":"m1","data":{"topic_id":999,"subject":"S","snippet":"A","group_domain":"d","group_name":"g","is_private":false,"created_at":"2026-08-26T10:00:00Z"}},` +
		`{"id":"m2","data":{"topic_id":999,"subject":"S","snippet":"B","group_domain":"d","group_name":"g","is_private":true,"created_at":"2026-08-26T11:00:00Z"}}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Private, "thread with any private message must be marked private")
}

// ── URL construction: empty group_name → empty URL ────────────────────────────

func TestListMailingListActivityForWindow_EmptyGroupName_NoURL(t *testing.T) {
	body := []byte(`{"resources":[{` +
		`"id":"m1","data":{` +
		`"topic_id":42,"subject":"S","snippet":"E",` +
		`"group_domain":"lists.example.org","group_name":"",` +
		`"is_private":false,"created_at":"2026-08-26T10:00:00Z"` +
		`}}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].URL, "URL must be empty when group_name is not set")
}

// ── Malformed data payload → record skipped ───────────────────────────────────

func TestListMailingListActivityForWindow_MalformedRecord_Skipped(t *testing.T) {
	body := []byte(`{"resources":[` +
		`{"id":"bad","data":[1,2,3]},` +
		`{"id":"good","data":{"topic_id":1,"subject":"Good","snippet":"OK","group_domain":"d","group_name":"g","is_private":false,"created_at":"2026-08-26T10:00:00Z"}}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now().Add(-time.Hour), time.Now())

	require.NoError(t, err)
	require.Len(t, got, 1, "malformed record must be skipped")
	assert.Equal(t, "1", got[0].ThreadID)
}

// ── Non-2xx response → error ──────────────────────────────────────────────────

func TestListMailingListActivityForWindow_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := newMailingListSource(t, srv)
	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.Error(t, err)
}
