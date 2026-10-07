// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package opensearch

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

func b2bOrgHitWithWebsite(objectID, website string) map[string]any {
	return map[string]any{
		"_source": map[string]any{
			"object_id": objectID,
			"data":      map[string]any{"website": website},
		},
	}
}

func TestSearchFirstSFID_noHits(t *testing.T) {
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.URL.Path, "_search")
		writeOpenSearchSearchResponse(w, nil, 0)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "Acme Corp")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, sfid)
}

func TestSearchFirstSFID_singleHitFromObjectID(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit(wantSFID, "")}, 1)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "The Linux Foundation")
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

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "Acme Corp")
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

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "Ambiguous Org")
	require.NoError(t, err)
	assert.False(t, ok, "ambiguous matches must not resolve to an SFID")
	assert.Empty(t, sfid)
}

func TestSearchFirstSFID_rejectsMalformedSFID(t *testing.T) {
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit("not-a-valid-sfid", "")}, 1)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "Bad Org")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, sfid)
}

func TestSearchFirstSFID_fallbackToDataUID(t *testing.T) {
	const wantSFID = "001B000000IqhSLIAZ"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit("", wantSFID)}, 1)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "UID-only Org")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
}

func TestSearchFirstSFID_malformedButRightLengthObjectIDFallsBackToDataUID(t *testing.T) {
	// object_id is present (not empty) and happens to be 18 characters, but
	// contains punctuation and is not a well-formed SFID. A length-only check
	// would wrongly accept it instead of falling back to data.uid.
	const malformed = "not-a-valid-sfid!!"
	const wantSFID = "001B000000IqhSLIAZ"
	require.Len(t, malformed, 18)

	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit(malformed, wantSFID)}, 1)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, _, err := resolver.searchTerm(context.Background(), "data.name", "Malformed ObjectID Org")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
}

func TestExtractPrimaryDomain(t *testing.T) {
	assert.Equal(t, "linuxfoundation.org", extractPrimaryDomain("https://www.linuxfoundation.org/about"))
	assert.Equal(t, "example.com", extractPrimaryDomain("example.com"))
	assert.Equal(t, "", extractPrimaryDomain(""))
}

func TestExtractPrimaryDomain_rejectsWildcardMetacharacters(t *testing.T) {
	// url.Parse does not validate hostname characters, so "*" flows straight
	// through Hostname() unescaped (unlike "?"/"#", which url.Parse treats as
	// delimiters). Without explicit rejection it would reach an OpenSearch
	// wildcard query pattern and broaden the match arbitrarily.
	assert.Equal(t, "", extractPrimaryDomain("https://a*b.com"))
}

func TestB2BOrgResolver_ResolveSFID_wildcardHitWithMismatchedDomainRejected(t *testing.T) {
	// Primary-domain term search misses; the wildcard website search returns a
	// hit whose actual website is a different, longer domain that happens to
	// contain "example.com" as a suffix. Without exact-hostname verification
	// this would incorrectly resolve.
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "wildcard") {
			writeOpenSearchSearchResponse(w, []map[string]any{
				b2bOrgHitWithWebsite("0014100000Te2ovAAB", "https://example.com.evil.com"),
			}, 1)
			return
		}
		writeOpenSearchSearchResponse(w, nil, 0)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "", "https://example.com")
	require.NoError(t, err)
	assert.False(t, ok, "wildcard hit with a mismatched exact hostname must not resolve")
	assert.Empty(t, sfid)
}

func TestB2BOrgResolver_ResolveSFID_wildcardHitVerifiedByExactDomain(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "wildcard") {
			writeOpenSearchSearchResponse(w, []map[string]any{
				b2bOrgHitWithWebsite(wantSFID, "https://example.com"),
			}, 1)
			return
		}
		writeOpenSearchSearchResponse(w, nil, 0)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "", "https://example.com")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
}

func TestB2BOrgResolver_ResolveSFID_ambiguousPrimaryDomainStopsResolution(t *testing.T) {
	// Two orgs share the same primary_domain -> ambiguous. ResolveSFID must
	// not fall through to the website wildcard matcher, which would
	// otherwise resolve a different org than the one causing the ambiguity.
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "wildcard") {
			writeOpenSearchSearchResponse(w, []map[string]any{
				b2bOrgHitWithWebsite("001B000000IqhSLIAZ", "https://example.com"),
			}, 1)
			return
		}
		writeOpenSearchSearchResponse(w, []map[string]any{
			b2bOrgHit("0014100000Te2ovAAB", ""),
			b2bOrgHit("001B000000IqhSLIAZ", ""),
		}, 2)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "", "https://example.com")
	require.NoError(t, err)
	assert.False(t, ok, "ambiguous primary_domain match must stop resolution, not fall through to the website matcher")
	assert.Empty(t, sfid)
}

func TestB2BOrgResolver_ResolveSFID_fullWildcardPageTreatedAsAmbiguous(t *testing.T) {
	// The wildcard search's result page is capped at 10. A full page means
	// additional matches may exist beyond it, so filtering by exact domain
	// cannot prove the (apparent) result is a true miss or a true unique match.
	hits := make([]map[string]any, 10)
	for i := range hits {
		hits[i] = b2bOrgHitWithWebsite(fmt.Sprintf("0014100000Te2ov%03d", i), "https://example.com")
	}
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "wildcard") {
			writeOpenSearchSearchResponse(w, hits, 10)
			return
		}
		writeOpenSearchSearchResponse(w, nil, 0)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "", "https://example.com")
	require.NoError(t, err)
	assert.False(t, ok, "a full result page must be treated as ambiguous, not a confident unique match")
	assert.Empty(t, sfid)
}

func TestB2BOrgResolver_ResolveSFID_byName(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"
	client := newTestOpenSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "_search") {
			http.NotFound(w, r)
			return
		}
		writeOpenSearchSearchResponse(w, []map[string]any{b2bOrgHit(wantSFID, "")}, 1)
	})

	resolver := &B2BOrgResolver{client: client, index: testOpenSearchIndex}
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "The Linux Foundation", "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantSFID, sfid)
}
