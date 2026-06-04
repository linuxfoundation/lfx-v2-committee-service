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

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// DefaultOrgCommitteeSeatType is the query-service resource type the org-committee-seat source reads.
const DefaultOrgCommitteeSeatType = "committee_member"

// OrgCommitteeSeatSourceConfig configures the live org-committee-seat source. An empty BaseURL
// disables the client and returns zero seats without error so deployments that don't yet wire the
// query-service still boot.
type OrgCommitteeSeatSourceConfig struct {
	BaseURL string
	Type    string
	Timeout time.Duration
}

// OrgCommitteeSeatSource is the live OrgCommitteeSeatReader adapter. It speaks
//
//	GET {BaseURL}/query/resources?type=committee_member
//	    &tags_all=organization_id:{sfid}[&tags_all=project_uid:{p}]
//
// against the query-service using an M2M (client-credentials) *http.Client (NOT the caller's bearer
// token), so the read is privileged and includes private-committee seats. Org/project scoping is the
// sole filter; the account-level b2b_org gate is enforced at the edge by Heimdall.
type OrgCommitteeSeatSource struct {
	cfg    OrgCommitteeSeatSourceConfig
	client *http.Client
}

// NewOrgCommitteeSeatSource constructs a live org-committee-seat source. The supplied *http.Client
// MUST already carry M2M client_credentials Authorization (wired in providers.go).
func NewOrgCommitteeSeatSource(cfg OrgCommitteeSeatSourceConfig, client *http.Client) *OrgCommitteeSeatSource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Type == "" {
		cfg.Type = DefaultOrgCommitteeSeatType
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &OrgCommitteeSeatSource{cfg: cfg, client: client}
}

// ListOrgCommitteeSeats fetches committee_member docs tagged with organization_id:{sfid}, one
// query per project_uid in the family (deduped by member uid). When projectUIDs is empty it queries
// by organization only.
func (s *OrgCommitteeSeatSource) ListOrgCommitteeSeats(ctx context.Context, orgSFID string, projectUIDs []string) ([]*model.CommitteeMember, error) {
	if s == nil || s.cfg.BaseURL == "" {
		// Startup already warns once (OrgCommitteeSeatReaderImpl) when QUERY_SERVICE_URL is
		// unset. This is a per-request, user-facing read path, so keep the per-call signal at
		// debug to avoid log spam when the endpoint is hit frequently.
		slog.DebugContext(ctx, "org committee seat source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}
	if orgSFID == "" {
		return nil, nil
	}

	// Dedupe project scopes so duplicate project_uids (trivially sent via repeated query
	// params) don't trigger redundant upstream queries.
	scopes := make([]string, 0, len(projectUIDs))
	seenScope := make(map[string]bool, len(projectUIDs))
	for _, projectUID := range projectUIDs {
		if projectUID == "" || seenScope[projectUID] {
			continue
		}
		seenScope[projectUID] = true
		scopes = append(scopes, projectUID)
	}
	if len(scopes) == 0 {
		scopes = []string{""} // organization-only scope
	}

	seen := make(map[string]bool)
	var out []*model.CommitteeMember
	for _, projectUID := range scopes {
		members, err := s.queryOnce(ctx, orgSFID, projectUID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.UID != "" && seen[m.UID] {
				continue
			}
			if m.UID != "" {
				seen[m.UID] = true
			}
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *OrgCommitteeSeatSource) queryOnce(ctx context.Context, orgSFID, projectUID string) ([]*model.CommitteeMember, error) {
	u, err := url.Parse(s.cfg.BaseURL)
	if err != nil {
		// url.Parse errors embed the raw URL (e.g. `parse "https://…": …`). wrapError surfaces
		// err.Error() to API clients as an InternalServerError, so log the detail server-side and
		// return a sanitized error that does not leak QUERY_SERVICE_URL.
		slog.ErrorContext(ctx, "invalid query-service base URL", "error", err)
		return nil, fmt.Errorf("org-committee-seat source has an invalid base URL")
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("type", s.cfg.Type)
	q.Add("tags_all", "organization_id:"+orgSFID)
	if projectUID != "" {
		q.Add("tags_all", "project_uid:"+projectUID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build org-committee-seat request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("org-committee-seat request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		// Log the truncated upstream body server-side for diagnostics, but do NOT include it in the
		// returned error: that error is surfaced to API clients as an InternalServerError and the raw
		// upstream body could leak internal details. Clients only see the status.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		slog.ErrorContext(ctx, "org-committee-seat source returned non-2xx",
			"status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("org-committee-seat source returned status %d", resp.StatusCode)
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode org-committee-seat response: %w", err)
	}

	out := make([]*model.CommitteeMember, 0, len(env.Resources))
	for _, r := range env.Resources {
		var m model.CommitteeMember
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &m); err != nil {
				slog.WarnContext(ctx, "skipping committee_member record with malformed data",
					"uid", r.UID, "error", err)
				continue
			}
		}
		if m.UID == "" {
			m.UID = r.UID
		}
		out = append(out, &m)
	}
	return out, nil
}
