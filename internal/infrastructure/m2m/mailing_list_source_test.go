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

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
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

// ── Field mapping, malformed-record skipping, and subscriber_count ────────────
//
// The v1_mailing_list_thread indexer contract stores the thread v2 UUID at
// data.uid. The envelope top-level "id" is the OpenSearch document ID. Each
// table case sets up its own server response; the window is fixed for all cases.

func TestListMailingListActivityForWindow(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	goodData := queryMailingListData{UID: "good", Subject: "Good Thread"}
	goodJSON, _ := json.Marshal(goodData)

	tests := []struct {
		name  string
		body  []byte
		check func(t *testing.T, got []port.MailingListActivity)
	}{
		{
			// ThreadID must come from data.uid, not the envelope top-level id.
			// Using distinct values for each field proves we read the right one.
			name: "ThreadID comes from data.uid not envelope id",
			body: []byte(`{"resources":[{` +
				`"id":"os-doc-id-ignored",` +
				`"data":{` +
				`"uid":"thread-uid-1",` +
				`"subject":"Release planning for Q3",` +
				`"url":"https://lists.example.org/thread/abc123",` +
				`"excerpt":"We should coordinate with the infra team...",` +
				`"private":true` +
				`}}]}`),
			check: func(t *testing.T, got []port.MailingListActivity) {
				t.Helper()
				require.Len(t, got, 1)
				assert.Equal(t, "thread-uid-1", got[0].ThreadID, "ThreadID must come from data.uid")
				assert.Equal(t, "Release planning for Q3", got[0].Subject)
				assert.Equal(t, "https://lists.example.org/thread/abc123", got[0].URL)
				assert.Equal(t, "We should coordinate with the infra team...", got[0].Excerpt)
				assert.True(t, got[0].Private)
			},
		},
		{
			name: "malformed data payload skips record, keeps good ones",
			body: []byte(`{"resources":[` +
				`{"id":"bad","data":[1,2,3]},` +
				`{"id":"good","data":` + string(goodJSON) + `}` +
				`]}`),
			check: func(t *testing.T, got []port.MailingListActivity) {
				t.Helper()
				require.Len(t, got, 1, "malformed record must be skipped")
				assert.Equal(t, "good", got[0].ThreadID)
			},
		},
		{
			// subscriber_count is present in the query-service payload and must
			// be mapped; when the field is absent the zero value is correct.
			name: "subscriber_count mapped when present, zero when absent",
			body: []byte(`{"resources":[` +
				`{"id":"doc-1","data":{"uid":"t-1","subject":"With subscribers","subscriber_count":42}},` +
				`{"id":"doc-2","data":{"uid":"t-2","subject":"Without subscribers"}}` +
				`]}`),
			check: func(t *testing.T, got []port.MailingListActivity) {
				t.Helper()
				require.Len(t, got, 2)
				assert.Equal(t, 42, got[0].SubscriberCount, "subscriber_count must be mapped from data field")
				assert.Equal(t, 0, got[1].SubscriberCount, "absent subscriber_count must default to zero")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			defer srv.Close()

			src := newMailingListSource(t, srv)
			got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

			require.NoError(t, err)
			tc.check(t, got)
		})
	}
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
