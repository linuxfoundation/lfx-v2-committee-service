// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// natsOrgCommitteeSeatReader is the NATS-KV implementation of port.OrgCommitteeSeatReader for the
// Org Lens Board & Committee tab (LFXV2-1865). It reads committee-service's OWN datastore — the
// committee-members KV bucket via the by-organization secondary index — so there is NO cross-service
// query-service / M2M call. Private-committee seats are included because the read is over the raw
// source of truth (authorization is the account-level b2b_org gate at the edge, not per-seat FGA).
//
// The membership project family (foundation root + descendants) is resolved by the BFF and passed as
// projectUIDs; this reader filters in memory rather than walking the project tree (committee-service
// has no hierarchy primitive — "Gap 2"). Per-org fan-out is small for almost all orgs; the rare
// large org is the case to watch (a project_uid sub-index can follow if needed).
type natsOrgCommitteeSeatReader struct {
	members    port.CommitteeMemberReader
	committees port.CommitteeBaseReader
}

// NewNATSOrgCommitteeSeatReader builds the NATS-KV org-committee-seat reader over a
// CommitteeMemberReader (the org-index member read) and a CommitteeBaseReader (used to recover a
// missing project_uid from the parent committee — see ListOrgCommitteeSeats). In production both are
// the NATS storage adapter.
func NewNATSOrgCommitteeSeatReader(members port.CommitteeMemberReader, committees port.CommitteeBaseReader) port.OrgCommitteeSeatReader {
	return &natsOrgCommitteeSeatReader{members: members, committees: committees}
}

// ListOrgCommitteeSeats returns the org's committee members from the by-organization KV index,
// optionally filtered to the project family. SFID normalization is handled by the storage layer.
func (r *natsOrgCommitteeSeatReader) ListOrgCommitteeSeats(ctx context.Context, orgSFID string, projectUIDs []string) ([]*model.CommitteeMember, error) {
	members, err := r.members.ListMembersByOrganization(ctx, orgSFID)
	if err != nil {
		return nil, err
	}

	// No project family supplied → organization-only scope.
	if len(projectUIDs) == 0 {
		return members, nil
	}

	family := make(map[string]struct{}, len(projectUIDs))
	for _, p := range projectUIDs {
		if p != "" {
			family[p] = struct{}{}
		}
	}

	// All supplied project_uids were empty (e.g. ?project_uids=) → treat as organization-only scope.
	if len(family) == 0 {
		return members, nil
	}

	// project_uid on a member is denormalized from its committee at write time, but records created
	// before that denormalization landed (LFXV2-1442) carry an EMPTY project_uid in KV truth. Those
	// seats would be silently dropped by the family filter even though they belong to the foundation
	// (the OpenSearch index already recovers project_uid for them). So when a member's own project_uid
	// is empty, fall back to its committee's project_uid (cached per committee — an org touches few
	// distinct committees) and enrich the in-memory member so downstream mapping stays consistent.
	committeeProjectCache := make(map[string]string)
	recovered := 0
	filtered := make([]*model.CommitteeMember, 0, len(members))
	for _, m := range members {
		if m == nil {
			continue
		}
		if m.ProjectUID == "" {
			if projectUID := r.resolveCommitteeProjectUID(ctx, m.CommitteeUID, committeeProjectCache); projectUID != "" {
				m.ProjectUID = projectUID
				recovered++
			}
		}
		if _, ok := family[m.ProjectUID]; ok {
			filtered = append(filtered, m)
		}
	}

	slog.DebugContext(ctx, "org committee seats filtered to project family",
		"org_sfid", orgSFID,
		"family_size", len(family),
		"total_seats", len(members),
		"in_family", len(filtered),
		"project_uid_recovered", recovered,
	)

	return filtered, nil
}

// resolveCommitteeProjectUID returns the project_uid for committeeUID, reading the committee base from
// KV (cached for the lifetime of one list call). A lookup failure or an unknown committee yields an
// empty string — the seat is then excluded by the family filter exactly as before, so a transient
// committee-read error degrades to the pre-fallback behavior rather than failing the whole list.
func (r *natsOrgCommitteeSeatReader) resolveCommitteeProjectUID(ctx context.Context, committeeUID string, cache map[string]string) string {
	if committeeUID == "" || r.committees == nil {
		return ""
	}
	if projectUID, ok := cache[committeeUID]; ok {
		return projectUID
	}
	base, _, err := r.committees.GetBase(ctx, committeeUID)
	if err != nil || base == nil {
		slog.WarnContext(ctx, "could not recover project_uid from committee for a member with empty project_uid",
			"committee_uid", committeeUID,
			"error", err,
		)
		cache[committeeUID] = "" // negative-cache so a failing committee isn't re-fetched per seat
		return ""
	}
	cache[committeeUID] = base.ProjectUID
	return base.ProjectUID
}
