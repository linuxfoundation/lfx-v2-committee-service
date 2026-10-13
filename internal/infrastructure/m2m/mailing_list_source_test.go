// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

// listResource builds a groupsio_mailing_list resource as returned by the
// query-service.
func listResource(uid string, public bool) string {
	return fmt.Sprintf(`{"id":"groupsio_mailing_list:%s","data":{"uid":%q,"public":%t}}`, uid, uid, public)
}

// messageResource builds a groupsio_mailing_list_message resource.
func messageResource(id string, topicID int, listUID, subject, snippet, createdAt string) string {
	return fmt.Sprintf(`{"id":%q,"data":{"topic_id":%d,"subject":%q,"snippet":%q,`+
		`"group_domain":"lists.example.org","group_name":"dev","mailing_list_uid":%q,"created_at":%q}}`,
		id, topicID, subject, snippet, listUID, createdAt)
}

func resourcesBody(resources ...string) string {
	return `{"resources":[` + strings.Join(resources, ",") + `]}`
}

// fakeQueryService routes by the `type` query parameter and records every
// request so tests can assert on the two-step query shape.
type fakeQueryService struct {
	mu       sync.Mutex
	requests []url.Values

	// listPages and messagePages hold the response body per page. The page
	// served is chosen by the request's page_token ("" → index 0, "pN" → N).
	listPages    []string
	messagePages []string
	listStatus   int
	msgStatus    int
}

func (f *fakeQueryService) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.requests = append(f.requests, q)
		f.mu.Unlock()

		pages, status := f.messagePages, f.msgStatus
		if q.Get("type") == mailingListResourceType {
			pages, status = f.listPages, f.listStatus
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		idx := 0
		if tok := q.Get("page_token"); tok != "" {
			_, _ = fmt.Sscanf(tok, "p%d", &idx)
		}
		if idx >= len(pages) {
			_, _ = w.Write([]byte(`{"resources":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[idx]))
	})
}

func (f *fakeQueryService) requestsOfType(typ string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, q := range f.requests {
		if q.Get("type") == typ {
			out = append(out, q)
		}
	}
	return out
}

func newFakeQueryService(t *testing.T, f *fakeQueryService) *MailingListSource {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return newMailingListSource(t, srv)
}

var (
	testWindowStart = time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	testWindowEnd   = time.Date(2026, 8, 26, 23, 59, 59, 0, time.UTC)
)

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListMailingListActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewMailingListSource(MailingListSourceConfig{BaseURL: ""}, nil)
	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Committee has no mailing lists → nil, message query never issued ─────────

func TestListMailingListActivityForWindow_NoLists_SkipsMessageQuery(t *testing.T) {
	f := &fakeQueryService{listPages: []string{resourcesBody()}}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	assert.Nil(t, got)
	assert.Empty(t, f.requestsOfType(DefaultMailingListType), "no lists → no message query")
}

// ── Two-step query shape ──────────────────────────────────────────────────────

func TestListMailingListActivityForWindow_QueryShape(t *testing.T) {
	f := &fakeQueryService{
		listPages:    []string{resourcesBody(listResource("ml-1", true), listResource("ml-2", true))},
		messagePages: []string{resourcesBody()},
	}
	src := newFakeQueryService(t, f)

	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)
	require.NoError(t, err)

	lists := f.requestsOfType(mailingListResourceType)
	require.Len(t, lists, 1)
	assert.Equal(t, []string{"committee_uid:c-1"}, lists[0]["tags"], "lists are resolved by committee_uid tag")
	assert.Equal(t, queryPageSize, lists[0].Get("page_size"))

	msgs := f.requestsOfType(DefaultMailingListType)
	require.Len(t, msgs, 1)
	assert.ElementsMatch(t, []string{"mailing_list:ml-1", "mailing_list:ml-2"}, msgs[0]["tags"],
		"one OR query covers all of the committee's lists")
	assert.Empty(t, msgs[0].Get("tags_all"))
	assert.Equal(t, "created_at", msgs[0].Get("date_field"))
	assert.Equal(t, testWindowStart.UTC().Format(time.RFC3339Nano), msgs[0].Get("date_from"))
	assert.Equal(t, testWindowEnd.UTC().Format(time.RFC3339Nano), msgs[0].Get("date_to"))
	assert.Equal(t, queryPageSize, msgs[0].Get("page_size"))
	assert.Empty(t, msgs[0].Get("start_time[gte]"), "must not use legacy start_time bracket notation")
}

// ── Many lists are split into bounded batches ─────────────────────────────────

func TestListMailingListActivityForWindow_ManyLists_Batched(t *testing.T) {
	n := listTagBatchSize + 5
	var lists []string
	for i := 0; i < n; i++ {
		lists = append(lists, listResource(fmt.Sprintf("ml-%d", i), true))
	}
	f := &fakeQueryService{
		listPages:    []string{resourcesBody(lists...)},
		messagePages: []string{resourcesBody()},
	}
	src := newFakeQueryService(t, f)

	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)
	require.NoError(t, err)

	msgs := f.requestsOfType(DefaultMailingListType)
	require.Len(t, msgs, 2, "lists beyond the batch size require a second message query")
	assert.Len(t, msgs[0]["tags"], listTagBatchSize)
	assert.Len(t, msgs[1]["tags"], 5)

	var want []string
	for i := 0; i < n; i++ {
		want = append(want, fmt.Sprintf("mailing_list:ml-%d", i))
	}
	got := append(append([]string{}, msgs[0]["tags"]...), msgs[1]["tags"]...)
	assert.ElementsMatch(t, want, got, "every list must appear in exactly one batch")
}

// ── Pagination: page_token is followed for both queries ──────────────────────

func TestListMailingListActivityForWindow_FollowsPageToken(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{
			`{"resources":[` + listResource("ml-1", true) + `],"page_token":"p1"}`,
			resourcesBody(listResource("ml-2", true)),
		},
		messagePages: []string{
			`{"resources":[` + messageResource("m1", 1, "ml-1", "First", "A", "2026-08-26T10:00:00Z") + `],"page_token":"p1"}`,
			resourcesBody(messageResource("m2", 2, "ml-2", "Second", "B", "2026-08-26T11:00:00Z")),
		},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 2, "messages from both pages must be returned")
	assert.Equal(t, "1", got[0].ThreadID)
	assert.Equal(t, "2", got[1].ThreadID)

	listReqs := f.requestsOfType(mailingListResourceType)
	require.Len(t, listReqs, 2, "list query must follow page_token")
	assert.Equal(t, "p1", listReqs[1].Get("page_token"))
	assert.Equal(t, []string{"committee_uid:c-1"}, listReqs[1]["tags"], "list filter must be kept on later pages")

	msgs := f.requestsOfType(DefaultMailingListType)
	require.Len(t, msgs, 2, "message query must follow page_token")
	assert.Equal(t, "p1", msgs[1].Get("page_token"))
	assert.ElementsMatch(t, []string{"mailing_list:ml-1", "mailing_list:ml-2"}, msgs[0]["tags"],
		"lists from every list page are included in the message query")

	// The token is only a cursor: the query-service re-runs the filters from the
	// request parameters on every page, so they must all be repeated.
	for _, key := range []string{"type", "date_field", "date_from", "date_to", "page_size", "v"} {
		assert.Equal(t, msgs[0].Get(key), msgs[1].Get(key), "%s must be repeated on later pages", key)
	}
	assert.ElementsMatch(t, msgs[0]["tags"], msgs[1]["tags"], "tags must be repeated on later pages")
}

// ── Pagination is bounded when the upstream never stops returning a token ────

func TestListMailingListActivityForWindow_PageCap_StopsAndReturnsGathered(t *testing.T) {
	var mu sync.Mutex
	messageCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if q.Get("type") == mailingListResourceType {
			_, _ = w.Write([]byte(resourcesBody(listResource("ml-1", true))))
			return
		}
		mu.Lock()
		messageCalls++
		n := messageCalls
		mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"resources":[%s],"page_token":"p%d"}`,
			messageResource(fmt.Sprintf("m%d", n), n, "ml-1", "S", "A", "2026-08-26T10:00:00Z"), n)
	}))
	defer srv.Close()
	src := newMailingListSource(t, srv)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err, "hitting the page cap is not an error")
	assert.Equal(t, maxQueryPages, messageCalls, "must stop after maxQueryPages requests")
	assert.Len(t, got, maxQueryPages, "results gathered before the cap are still returned")
}

// ── Field mapping: ThreadID comes from topic_id, Excerpt from snippet ─────────

func TestListMailingListActivityForWindow_FieldMapping(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{`{"resources":[{` +
			`"id":"os-doc-id-269273631",` +
			`"data":{` +
			`"topic_id":120939422,` +
			`"subject":"Please review this PR",` +
			`"snippet":"Hello folks!",` +
			`"group_domain":"lists.sonicfoundation.dev",` +
			`"group_name":"dev",` +
			`"mailing_list_uid":"ml-1",` +
			`"created_at":"2026-08-26T15:33:36Z"` +
			`}}]}`},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

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
	// Three messages: two in topic 111, one in topic 222. The earliest
	// in-window message in topic 111 (msg-1) determines subject and snippet.
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{resourcesBody(
			messageResource("msg-2", 111, "ml-1", "Re: Topic A", "Reply text", "2026-08-26T12:00:00Z"),
			messageResource("msg-1", 111, "ml-1", "Topic A", "Original text", "2026-08-26T11:00:00Z"),
			messageResource("msg-3", 222, "ml-1", "Topic B", "Other thread", "2026-08-26T13:00:00Z"),
		)},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 2, "two topics → two activities")

	// Topic 111 is encountered first in the response — it comes first in output.
	assert.Equal(t, "111", got[0].ThreadID)
	assert.Equal(t, "Topic A", got[0].Subject, "earliest in-window message determines subject")
	assert.Equal(t, "Original text", got[0].Excerpt)

	assert.Equal(t, "222", got[1].ThreadID)
}

// ── Mixed UTC offsets: sort by wall-clock time, not string order ──────────────

func TestListMailingListActivityForWindow_MixedUTCOffsets_SortedByWallClock(t *testing.T) {
	// msg-a: 2026-08-26T11:00:00+02:00 = 09:00 UTC (earlier wall-clock)
	// msg-b: 2026-08-26T09:30:00Z      = 09:30 UTC (later wall-clock)
	// Lexicographic order: "09:30:00Z" < "11:00:00+02:00" → msg-b sorts first (wrong).
	// time.Time.Before order: 09:00 UTC < 09:30 UTC → msg-a sorts first (correct).
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{resourcesBody(
			messageResource("msg-b", 777, "ml-1", "Later", "B", "2026-08-26T09:30:00Z"),
			messageResource("msg-a", 777, "ml-1", "Earlier", "A", "2026-08-26T11:00:00+02:00"),
		)},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Earlier", got[0].Subject, "msg-a (09:00 UTC) must be chosen over msg-b (09:30 UTC) despite lexicographic order")
}

// ── Privacy: visibility comes from the owning mailing list ───────────────────

func TestListMailingListActivityForWindow_Privacy(t *testing.T) {
	tests := []struct {
		name        string
		lists       []string
		messages    []string
		wantPrivate bool
	}{
		{
			name:        "public list → public thread",
			lists:       []string{listResource("ml-pub", true)},
			messages:    []string{messageResource("m1", 999, "ml-pub", "S", "A", "2026-08-26T10:00:00Z")},
			wantPrivate: false,
		},
		{
			name:        "private list → private thread",
			lists:       []string{listResource("ml-priv", false)},
			messages:    []string{messageResource("m1", 999, "ml-priv", "S", "A", "2026-08-26T10:00:00Z")},
			wantPrivate: true,
		},
		{
			name:  "any message on a private list marks the whole thread private",
			lists: []string{listResource("ml-pub", true), listResource("ml-priv", false)},
			messages: []string{
				messageResource("m1", 999, "ml-pub", "S", "A", "2026-08-26T10:00:00Z"),
				messageResource("m2", 999, "ml-priv", "S", "B", "2026-08-26T11:00:00Z"),
			},
			wantPrivate: true,
		},
		{
			name:        "message whose list is unknown is treated as private",
			lists:       []string{listResource("ml-pub", true)},
			messages:    []string{messageResource("m1", 999, "ml-other", "S", "A", "2026-08-26T10:00:00Z")},
			wantPrivate: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQueryService{
				listPages:    []string{resourcesBody(tt.lists...)},
				messagePages: []string{resourcesBody(tt.messages...)},
			}
			src := newFakeQueryService(t, f)

			got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantPrivate, got[0].Private)
		})
	}
}

// ── URL construction: empty group_name → empty URL ────────────────────────────

func TestListMailingListActivityForWindow_EmptyGroupName_NoURL(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{`{"resources":[{` +
			`"id":"m1","data":{` +
			`"topic_id":42,"subject":"S","snippet":"E",` +
			`"group_domain":"lists.example.org","group_name":"",` +
			`"mailing_list_uid":"ml-1","created_at":"2026-08-26T10:00:00Z"` +
			`}}]}`},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].URL, "URL must be empty when group_name is not set")
}

// ── Malformed data payload → record skipped ───────────────────────────────────

func TestListMailingListActivityForWindow_MalformedMessage_Skipped(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{`{"resources":[` +
			`{"id":"bad","data":[1,2,3]},` +
			messageResource("good", 1, "ml-1", "Good", "OK", "2026-08-26T10:00:00Z") +
			`]}`},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1, "malformed record must be skipped")
	assert.Equal(t, "1", got[0].ThreadID)
}

// ── Malformed or uid-less mailing list → skipped, others still used ──────────

func TestListMailingListActivityForWindow_MalformedList_Skipped(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{`{"resources":[` +
			`{"id":"bad","data":[1,2,3]},` +
			`{"id":"no-uid","data":{"public":true}},` +
			listResource("ml-1", true) +
			`]}`},
		messagePages: []string{resourcesBody(messageResource("m1", 1, "ml-1", "S", "A", "2026-08-26T10:00:00Z"))},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1)
	msgs := f.requestsOfType(DefaultMailingListType)
	require.Len(t, msgs, 1)
	assert.Equal(t, []string{"mailing_list:ml-1"}, msgs[0]["tags"], "only valid lists are queried")
}

// ── Zero topic_id → record skipped ───────────────────────────────────────────

func TestListMailingListActivityForWindow_ZeroTopicID_Skipped(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{`{"resources":[` +
			`{"id":"zero","data":{"topic_id":0,"subject":"S","snippet":"A","mailing_list_uid":"ml-1","created_at":"2026-08-26T10:00:00Z"}},` +
			`{"id":"empty-data","data":{}},` +
			messageResource("good", 5, "ml-1", "Good", "OK", "2026-08-26T10:00:00Z") +
			`]}`},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1, "records with zero or missing topic_id must be skipped")
	assert.Equal(t, "5", got[0].ThreadID)
}

// ── Zero created_at (omitted or null) → record skipped ───────────────────────

func TestListMailingListActivityForWindow_ZeroCreatedAt_Skipped(t *testing.T) {
	// omitted-ts: created_at absent from JSON → zero time.Time → must be skipped
	// null-ts: created_at explicitly null → zero time.Time → must be skipped
	// good: valid created_at → must survive
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		messagePages: []string{`{"resources":[` +
			`{"id":"omitted-ts","data":{"topic_id":10,"subject":"No time","snippet":"A","mailing_list_uid":"ml-1"}},` +
			`{"id":"null-ts","data":{"topic_id":11,"subject":"Null time","snippet":"B","mailing_list_uid":"ml-1","created_at":null}},` +
			messageResource("good", 12, "ml-1", "Good", "OK", "2026-08-26T10:00:00Z") +
			`]}`},
	}
	src := newFakeQueryService(t, f)

	got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1, "records with absent or null created_at must be skipped")
	assert.Equal(t, "12", got[0].ThreadID)
}

// ── Non-2xx on either query → error ───────────────────────────────────────────

func TestListMailingListActivityForWindow_ListQueryNon2xx_ReturnsError(t *testing.T) {
	f := &fakeQueryService{listStatus: http.StatusInternalServerError}
	src := newFakeQueryService(t, f)

	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.Error(t, err)
	assert.Empty(t, f.requestsOfType(DefaultMailingListType), "message query must not run when list resolution fails")
}

func TestListMailingListActivityForWindow_MessageQueryNon2xx_ReturnsError(t *testing.T) {
	f := &fakeQueryService{
		listPages: []string{resourcesBody(listResource("ml-1", true))},
		msgStatus: http.StatusInternalServerError,
	}
	src := newFakeQueryService(t, f)

	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.Error(t, err)
}

// ── One shared deadline covers every request of a call ───────────────────────

func TestListMailingListActivityForWindow_TotalTimeout_BoundsSequentialRequests(t *testing.T) {
	// Every request is faster than the per-request Timeout, but the endless
	// page_token chain would run for maxQueryPages × 20ms without a shared deadline.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("type") == mailingListResourceType {
			_, _ = fmt.Fprintf(w, `{"resources":[%s],"page_token":"next"}`, listResource("ml-1", true))
			return
		}
		_, _ = w.Write([]byte(resourcesBody()))
	}))
	defer srv.Close()

	src := NewMailingListSource(
		MailingListSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second, TotalTimeout: 50 * time.Millisecond},
		srv.Client(),
	)

	start := time.Now()
	_, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", testWindowStart, testWindowEnd)

	require.Error(t, err, "exceeding the total deadline must surface as an error")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second, "must stop at the shared deadline, not run every page")
}

func TestNewMailingListSource_DefaultsTotalTimeout(t *testing.T) {
	src := NewMailingListSource(MailingListSourceConfig{BaseURL: "http://example.invalid"}, nil)
	assert.Equal(t, defaultTotalTimeout, src.cfg.TotalTimeout)
}
