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

// TestMailingListSource_ListMailingListActivityForWindow is the single
// table-driven test for this exported method. All branching scenarios live
// here.
//
// Cases that do not need a real HTTP server set statusCode to 0 and body to
// nil; they configure the source directly via makeSrc.
func TestMailingListSource_ListMailingListActivityForWindow(t *testing.T) {
	windowStart := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 18, 23, 59, 59, 0, time.UTC)

	goodData := queryMailingListData{UID: "good", Subject: "Good Thread"}
	goodJSON, _ := json.Marshal(goodData)

	tests := []struct {
		name string
		// makeSrc, when non-nil, overrides normal server setup (used for the
		// disabled-base-URL case which never contacts an HTTP server).
		makeSrc    func() *MailingListSource
		statusCode int    // 0 means test uses makeSrc instead of a server
		body       []byte // server response body (used when statusCode != 0)
		wantErr    bool
		check      func(t *testing.T, got []port.MailingListActivity)
	}{
		{
			// When BaseURL is empty the source degrades gracefully: nil result,
			// no error, no HTTP request.
			name:    "disabled empty BaseURL returns nil without error",
			makeSrc: func() *MailingListSource { return NewMailingListSource(MailingListSourceConfig{BaseURL: ""}, nil) },
			check: func(t *testing.T, got []port.MailingListActivity) {
				t.Helper()
				assert.Nil(t, got)
			},
		},
		{
			// ThreadID must come from data.uid, not the envelope top-level id.
			// Using distinct values for each field proves we read the right one.
			name:       "ThreadID comes from data.uid not envelope id",
			statusCode: http.StatusOK,
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
			name:       "malformed data payload skips record, keeps good ones",
			statusCode: http.StatusOK,
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
			name:       "subscriber_count mapped when present, zero when absent",
			statusCode: http.StatusOK,
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
		{
			name:       "non-2xx response returns error",
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var src *MailingListSource
			if tc.makeSrc != nil {
				src = tc.makeSrc()
			} else {
				body := tc.body
				statusCode := tc.statusCode
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if statusCode != http.StatusOK {
						w.WriteHeader(statusCode)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}))
				defer srv.Close()
				src = NewMailingListSource(
					MailingListSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
					srv.Client(),
				)
			}

			got, err := src.ListMailingListActivityForWindow(context.Background(), "c-1", windowStart, windowEnd)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}
