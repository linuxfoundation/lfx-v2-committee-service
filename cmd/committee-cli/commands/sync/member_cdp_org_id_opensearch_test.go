// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	opensearchgo "github.com/opensearch-project/opensearch-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOpenSearchIndex = "resources"

func newTestOpenSearchClient(t *testing.T, handler http.HandlerFunc) *opensearchgo.Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := opensearchgo.NewClient(opensearchgo.Config{Addresses: []string{srv.URL}})
	require.NoError(t, err)
	return client
}

type openSearchSearchRequest struct {
	From  int `json:"from"`
	Size  int `json:"size"`
	Query struct {
		Bool struct {
			Must []json.RawMessage `json:"must"`
		} `json:"bool"`
	} `json:"query"`
}

func writeOpenSearchSearchResponse(w http.ResponseWriter, hits []map[string]any, total int64) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"hits": map[string]any{
			"total": map[string]any{"value": total},
			"hits":  hits,
		},
	})
}

func memberDiscoveryHit(uid string) map[string]any {
	return map[string]any{
		"_source": map[string]any{
			"object_id": uid,
			"data":      map[string]any{"uid": uid},
		},
	}
}

func TestSearchMemberUIDPage_singlePage(t *testing.T) {
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req openSearchSearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, 0, req.From)
		assert.Equal(t, 100, req.Size)
		writeOpenSearchSearchResponse(w, []map[string]any{
			memberDiscoveryHit("member-a"),
			memberDiscoveryHit("member-b"),
		}, 2)
	})

	uids, total, err := searchMemberUIDPage(context.Background(), client, testOpenSearchIndex, map[string]any{"match_all": map[string]any{}}, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []string{"member-a", "member-b"}, uids)
}

func TestSearchMemberUIDsWithCDPOrgID_paginates(t *testing.T) {
	var requests int
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req openSearchSearchRequest
		require.NoError(t, json.Unmarshal(body, &req))
		requests++

		switch req.From {
		case 0:
			require.Equal(t, memberDiscoveryPageSize, req.Size)
			firstPage := make([]map[string]any, memberDiscoveryPageSize)
			for i := range firstPage {
				firstPage[i] = memberDiscoveryHit(fmt.Sprintf("member-%04d", i))
			}
			writeOpenSearchSearchResponse(w, firstPage, int64(memberDiscoveryPageSize+2))
		case memberDiscoveryPageSize:
			writeOpenSearchSearchResponse(w, []map[string]any{
				memberDiscoveryHit("member-0500"),
				memberDiscoveryHit("member-0501"),
			}, int64(memberDiscoveryPageSize+2))
		default:
			t.Fatalf("unexpected pagination offset from=%d", req.From)
		}
	})

	uids, err := searchMemberUIDsWithCDPOrgID(context.Background(), client, testOpenSearchIndex)
	require.NoError(t, err)
	assert.Equal(t, 2, requests)
	assert.Len(t, uids, memberDiscoveryPageSize+2)
	assert.Equal(t, "member-0000", uids[0])
	assert.Equal(t, "member-0499", uids[memberDiscoveryPageSize-1])
	assert.Equal(t, "member-0500", uids[memberDiscoveryPageSize])
	assert.Equal(t, "member-0501", uids[memberDiscoveryPageSize+1])
}
