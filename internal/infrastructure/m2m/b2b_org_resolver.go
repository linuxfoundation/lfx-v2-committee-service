// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// B2BOrgResolverConfig configures lookup of b2b_org SFIDs via the query service.
type B2BOrgResolverConfig struct {
	BaseURL string
	Token   string
	Timeout time.Duration
}

// B2BOrgResolver resolves a member organization to its canonical b2b_org SFID.
type B2BOrgResolver struct {
	cfg    B2BOrgResolverConfig
	client *http.Client
}

// NewB2BOrgResolver constructs a resolver. The HTTP client should carry service
// auth (Bearer token or M2M oauth2 client). When BaseURL is empty, ResolveSFID
// returns found=false without error.
func NewB2BOrgResolver(cfg B2BOrgResolverConfig, client *http.Client) *B2BOrgResolver {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &B2BOrgResolver{cfg: cfg, client: client}
}

type b2bOrgQueryResource struct {
	UID  string          `json:"uid"`
	Data json.RawMessage `json:"data"`
}

type b2bOrgQueryEnvelope struct {
	Resources []b2bOrgQueryResource `json:"resources"`
}

// ResolveSFID looks up a b2b_org SFID by organization website/domain and name.
// Returns ("", false, nil) when no match is found.
func (r *B2BOrgResolver) ResolveSFID(ctx context.Context, name, website string) (string, bool, error) {
	if r == nil || r.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "b2b org resolver disabled: QUERY_SERVICE_URL not set")
		return "", false, nil
	}

	domain := extractPrimaryDomain(website)
	name = strings.TrimSpace(name)

	if domain != "" {
		sfid, ok, err := r.queryByCEL(ctx, fmt.Sprintf(`data.primary_domain == %q`, domain))
		if err != nil || ok {
			return sfid, ok, err
		}
		sfid, ok, err = r.queryByCEL(ctx, fmt.Sprintf(`data.website.contains(%q)`, domain))
		if err != nil || ok {
			return sfid, ok, err
		}
	}

	if name != "" {
		return r.queryByCEL(ctx, fmt.Sprintf(`data.name == %q`, name))
	}
	return "", false, nil
}

func (r *B2BOrgResolver) queryByCEL(ctx context.Context, celFilter string) (string, bool, error) {
	u, err := url.Parse(r.cfg.BaseURL)
	if err != nil {
		return "", false, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", "b2b_org")
	q.Set("cel_filter", celFilter)
	q.Set("page_size", "5")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, fmt.Errorf("build b2b_org query request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(r.cfg.Token); tok != "" {
		if !strings.HasPrefix(strings.ToLower(tok), "bearer ") {
			tok = "Bearer " + tok
		}
		req.Header.Set("Authorization", tok)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("b2b_org query request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", false, fmt.Errorf("b2b_org query returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env b2bOrgQueryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return "", false, fmt.Errorf("decode b2b_org query response: %w", err)
	}
	if len(env.Resources) == 0 {
		return "", false, nil
	}

	uid := utils.NormalizeAccountSFID(strings.TrimSpace(env.Resources[0].UID))
	if uid == "" || len(uid) != 18 {
		return "", false, nil
	}
	return uid, true, nil
}

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
	return host
}
