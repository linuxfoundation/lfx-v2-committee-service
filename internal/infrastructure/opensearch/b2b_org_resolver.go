// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

const b2bOrgObjectType = "b2b_org"

// B2BOrgResolver resolves a b2b_org SFID in OpenSearch by primary_domain, website, then name.
// It implements port.B2BOrgFallbackResolver.
type B2BOrgResolver struct {
	client *opensearchgo.Client
	index  string
}

var _ port.B2BOrgFallbackResolver = (*B2BOrgResolver)(nil)

// NewClient creates an OpenSearch client for the given base URL.
func NewClient(openSearchURL string) (*opensearchgo.Client, error) {
	client, err := opensearchgo.NewClient(opensearchgo.Config{
		Addresses: []string{openSearchURL},
	})
	if err != nil {
		return nil, errors.NewUnexpected("failed to create OpenSearch client", err)
	}
	return client, nil
}

// NewB2BOrgResolver creates an OpenSearch-backed name/website b2b_org fallback resolver.
func NewB2BOrgResolver(client *opensearchgo.Client, index string) *B2BOrgResolver {
	return &B2BOrgResolver{client: client, index: index}
}

// ResolveSFID looks up a b2b_org SFID in OpenSearch by primary_domain, website, then name.
func (r *B2BOrgResolver) ResolveSFID(ctx context.Context, name, website string) (string, bool, error) {
	if r == nil || r.client == nil {
		return "", false, nil
	}

	domain := extractPrimaryDomain(website)
	name = strings.TrimSpace(name)

	if domain != "" {
		sfid, ok, err := r.searchTerm(ctx, "data.primary_domain", domain)
		if err != nil || ok {
			return sfid, ok, err
		}
		// Anchor with scheme separator so "hat.com" cannot match "redhat.com".
		// searchWildcard independently verifies each hit's exact hostname against
		// domain, since the wildcard pattern alone would also match subdomains
		// and unrelated suffixes (e.g. "example.com.evil.com").
		sfid, ok, err = r.searchWildcard(ctx, "data.website", "*://"+domain+"*", domain)
		if err != nil || ok {
			return sfid, ok, err
		}
		sfid, ok, err = r.searchWildcard(ctx, "data.website", "*://www."+domain+"*", domain)
		if err != nil || ok {
			return sfid, ok, err
		}
	}

	if name != "" {
		return r.searchTerm(ctx, "data.name", name)
	}
	return "", false, nil
}

func (r *B2BOrgResolver) searchTerm(ctx context.Context, field, value string) (string, bool, error) {
	query := map[string]any{
		"size": 2,
		"query": map[string]any{
			"bool": map[string]any{
				"must": []any{
					map[string]any{"term": map[string]any{"latest": true}},
					map[string]any{"term": map[string]any{"object_type": b2bOrgObjectType}},
					map[string]any{"term": map[string]any{field: value}},
				},
			},
		},
		"_source": []string{"object_id", "data.uid"},
	}
	return r.searchFirstSFID(ctx, query, "")
}

// searchWildcard runs a wildcard query and additionally requires each hit's
// data.website to parse to exactly wantDomain. The wildcard pattern alone is
// not sufficient proof of a match: OpenSearch wildcard queries are unanchored
// substring matches, so "*://example.com*" also matches
// "https://example.com.evil.com" and "https://notexample.com".
func (r *B2BOrgResolver) searchWildcard(ctx context.Context, field, pattern, wantDomain string) (string, bool, error) {
	query := map[string]any{
		"size": 10,
		"query": map[string]any{
			"bool": map[string]any{
				"must": []any{
					map[string]any{"term": map[string]any{"latest": true}},
					map[string]any{"term": map[string]any{"object_type": b2bOrgObjectType}},
					map[string]any{"wildcard": map[string]any{field: map[string]any{"value": pattern}}},
				},
			},
		},
		"_source": []string{"object_id", "data.uid", "data.website"},
	}
	return r.searchFirstSFID(ctx, query, wantDomain)
}

func (r *B2BOrgResolver) searchFirstSFID(ctx context.Context, query map[string]any, wantDomain string) (string, bool, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return "", false, errors.NewUnexpected("marshal OpenSearch query", err)
	}

	res, err := r.client.Search(
		r.client.Search.WithContext(ctx),
		r.client.Search.WithIndex(r.index),
		r.client.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return "", false, errors.NewUnexpected("OpenSearch search request failed", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", false, errors.NewUnexpected(fmt.Sprintf("OpenSearch search error %s: %s", res.Status(), raw))
	}

	var parsed struct {
		Hits struct {
			Hits []struct {
				Source struct {
					ObjectID string `json:"object_id"`
					Data     struct {
						UID     string `json:"uid"`
						Website string `json:"website"`
					} `json:"data"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", false, errors.NewUnexpected("decode OpenSearch search response", err)
	}

	hits := parsed.Hits.Hits
	if wantDomain != "" {
		filtered := hits[:0]
		for _, h := range hits {
			if extractPrimaryDomain(h.Source.Data.Website) == wantDomain {
				filtered = append(filtered, h)
			}
		}
		hits = filtered
	}

	if len(hits) == 0 {
		return "", false, nil
	}
	// More than one hit means ambiguous match — skip to avoid misattribution.
	if len(hits) > 1 {
		slog.WarnContext(ctx, "b2b_org resolution skipped: ambiguous match (multiple results)", "hits", len(hits))
		return "", false, nil
	}

	hit := hits[0].Source
	sfid := utils.NormalizeAccountSFID(strings.TrimSpace(hit.ObjectID))
	if sfid == "" {
		sfid = utils.NormalizeAccountSFID(strings.TrimSpace(hit.Data.UID))
	}
	if sfid == "" || len(sfid) != 18 {
		return "", false, nil
	}
	return sfid, true, nil
}

// validHostname matches a conservative subset of legal hostname characters.
// url.Parse does not validate Hostname() against RFC 1123, so without this
// check an attacker-controlled website value (e.g. containing "*", "?", or
// "\") would flow unescaped into an OpenSearch wildcard query pattern.
var validHostname = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

func extractPrimaryDomain(website string) string {
	website = strings.TrimSpace(website)
	if website == "" {
		return ""
	}
	if !strings.Contains(website, "://") {
		website = "https://" + website
	}
	u, err := url.Parse(website)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if !validHostname.MatchString(host) {
		return ""
	}
	return host
}
