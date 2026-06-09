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
	members port.CommitteeMemberReader
}

// NewNATSOrgCommitteeSeatReader builds the NATS-KV org-committee-seat reader over any
// CommitteeMemberReader (in production, the NATS storage adapter).
func NewNATSOrgCommitteeSeatReader(members port.CommitteeMemberReader) port.OrgCommitteeSeatReader {
	return &natsOrgCommitteeSeatReader{members: members}
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

	filtered := make([]*model.CommitteeMember, 0, len(members))
	for _, m := range members {
		if m == nil {
			continue
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
	)

	return filtered, nil
}
