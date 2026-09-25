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
	"strings"
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

func b2bOrgHit(objectID, uid string) map[string]any {
	return map[string]any{
		"_source": map[string]any{
			"object_id": objectID,
			"data":      map[string]any{"uid": uid},
		},
	}
}

func memberDiscoveryHit(uid string) map[string]any {
	return map[string]any{
		"_source": map[string]any{
			"object_id": uid,
			"data":      map[string]any{"uid": uid},
		},
	}
}

func TestSearchFirstSFID_noHits(t *testing.T) {
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.URL.Path, "_search")
		writeOpenSearchSearchResponse(w, nil, 0)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.searchTerm(context.Background(), "data.name", "Acme Corp")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, sfid)
}

func TestSearchFirstSFID_singleHitFromObjectID(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit(wantSFID, "")}, 1)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.searchTerm(context.Background(), "data.name", "The Linux Foundation")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
}

func TestSearchFirstSFID_normalizes15CharSFID(t *testing.T) {
	const fifteen = "0017000000abcde"
	const want18 = "0017000000abcdeAAA"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit(fifteen, "")}, 1)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.searchTerm(context.Background(), "data.name", "Acme Corp")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, want18, sfid)
	assert.Len(t, sfid, 18)
}

func TestSearchFirstSFID_ambiguousMultipleHitsSkipped(t *testing.T) {
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{
			b2bOrgHit("0014100000Te2ovAAB", ""),
			b2bOrgHit("001B000000IqhSLIAZ", ""),
		}, 2)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.searchTerm(context.Background(), "data.name", "Ambiguous Org")
	require.NoError(t, err)
	assert.False(t, ok, "ambiguous matches must not resolve to an SFID")
	assert.Empty(t, sfid)
}

func TestSearchFirstSFID_rejectsMalformedSFID(t *testing.T) {
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit("not-a-valid-sfid", "")}, 1)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.searchTerm(context.Background(), "data.name", "Bad Org")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, sfid)
}

func TestSearchFirstSFID_fallbackToDataUID(t *testing.T) {
	const wantSFID = "001B000000IqhSLIAZ"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit("", wantSFID)}, 1)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.searchTerm(context.Background(), "data.name", "UID-only Org")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
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

func TestOpenSearchB2BOrgResolver_ResolveSFID_byName(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "_search") {
			http.NotFound(w, r)
			return
		}
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit(wantSFID, "")}, 1)
	})

	resolver := &openSearchB2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "The Linux Foundation", "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
}
