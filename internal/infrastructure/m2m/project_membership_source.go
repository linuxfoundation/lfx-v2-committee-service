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
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// DefaultProjectMembershipType is the query-service resource type the live
// project-membership source queries. This matches the type used by
// lfx-v2-member-service when indexing project_membership resources.
const DefaultProjectMembershipType = "project_membership"

// ProjectMembershipSourceConfig configures the live project-membership source.
// An empty BaseURL disables the client and produces zero results without error
// so deployments that have not yet wired the query-service can still generate
// briefs from other activity alone.
type ProjectMembershipSourceConfig struct {
	BaseURL string
	Type    string
	Timeout time.Duration
}

// ProjectMembershipSource is the live ProjectMembershipSource adapter. It speaks
//
//	GET {BaseURL}/query/resources?v=1&type={Type}&tags=project_uid:{uid}
//	    &date_field=purchase_date&date_from={windowStart}&date_to={windowEnd}
//	    &filters_all=status:Active
//
// against the query-service. Authentication is by a *http.Client returned by
// oauth2/clientcredentials (NOT the caller's bearer token).
//
// filters_all=status:Active is sent so the query-service narrows results
// server-side before pagination; client-side status filtering is kept as a
// defence-in-depth guard for any records that slip through.
//
// Note: page_token pagination is not yet implemented here; this matches the
// pattern of all other M2M sources (meetings, mailing-list, votes, surveys).
// A dedicated pagination PR should extend queryEnvelope and follow page_token
// across all sources consistently.
//
// Note: is_first_membership is not yet indexed in OpenSearch. The slice
// therefore includes renewals alongside new memberships. Callers (the
// weekly-brief generator) should word claims as "purchased this week" rather
// than "joined this week" to remain accurate.
type ProjectMembershipSource struct {
	cfg    ProjectMembershipSourceConfig
	client *http.Client
}

// NewProjectMembershipSource constructs a live project-membership source. The
// supplied *http.Client MUST already carry M2M client_credentials
// Authorization. Callers wire this in providers.go via oauth2/clientcredentials.
func NewProjectMembershipSource(cfg ProjectMembershipSourceConfig, client *http.Client) *ProjectMembershipSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Type == "" {
		cfg.Type = DefaultProjectMembershipType
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &ProjectMembershipSource{cfg: cfg, client: client}
}

type queryProjectMembershipData struct {
	// UID is the membership v2 UUID (json:"uid" per the member-service indexer contract).
	UID string `json:"uid"`
	// CompanyName is the member company name (json:"company_name" per the member-service
	// ProjectMembership model — not "account_name").
	CompanyName string `json:"company_name"`
	// Tier is the membership tier label, e.g. "Silver", "Associate"
	// (json:"tier" per the member-service ProjectMembership model — not "membership_tier").
	Tier string `json:"tier"`
	// PurchaseDate is the purchase date in RFC 3339 format, sourced from
	// Salesforce Asset.PurchaseDate with InstallDate / CreatedDate fallbacks.
	PurchaseDate string `json:"purchase_date"`
	Status       string `json:"status"`
}

// activeStatus is the membership status that indicates a membership is live.
const activeStatus = "Active"

// ListMembershipActivityForWindow fetches project memberships tagged with
// projectUID whose purchase_date falls in [windowStart, windowEnd] and whose
// status is "Active". When projectUID is empty the method degrades to zero
// results so committees without a project association do not fail the brief.
func (p *ProjectMembershipSource) ListMembershipActivityForWindow(ctx context.Context, projectUID string, windowStart, windowEnd time.Time) ([]port.ProjectMembershipActivity, error) {
	if p == nil || p.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "project membership source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}
	if projectUID == "" {
		slog.WarnContext(ctx, "project membership source: projectUID is empty; returning zero memberships")
		return nil, nil
	}

	u, err := url.Parse(p.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u = u.JoinPath("query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", p.cfg.Type)
	q.Set("tags", "project_uid:"+projectUID)
	q.Set("date_field", "purchase_date")
	q.Set("date_from", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("date_to", windowEnd.UTC().Format(time.RFC3339Nano))
	q.Set("filters_all", "status:Active")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build project-membership-source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("project membership source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("project membership source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode project-membership-source response: %w", err)
	}

	out := make([]port.ProjectMembershipActivity, 0, len(env.Resources))
	for _, r := range env.Resources {
		var data queryProjectMembershipData
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				slog.WarnContext(ctx, "skipping project membership record with malformed data",
					"uid", r.UID, "error", err)
				continue
			}
		}
		if data.Status != activeStatus {
			continue
		}
		if data.UID == "" {
			slog.WarnContext(ctx, "skipping project membership record without membership UID",
				"envelope_uid", r.UID)
			continue
		}
		var purchaseDate time.Time
		if data.PurchaseDate != "" {
			if t, parseErr := time.Parse(time.RFC3339, data.PurchaseDate); parseErr == nil {
				purchaseDate = t
			}
		}
		out = append(out, port.ProjectMembershipActivity{
			MembershipUID: data.UID,
			AccountName:   data.CompanyName,
			Tier:          data.Tier,
			PurchaseDate:  purchaseDate,
			Status:        data.Status,
		})
	}
	return out, nil
}
