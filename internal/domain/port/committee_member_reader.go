// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeMemberReader provides access to committee member reading operations
type CommitteeMemberReader interface {
	// GetMember retrieves a committee member by committee UID and member UID
	GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error)
	// GetMemberRevision retrieves the revision number for a committee member
	GetMemberRevision(ctx context.Context, uid string) (uint64, error)
	// ListMembersByCommittee retrieves all members for a given committee UID using the secondary index.
	ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error)
	// ListMembersByOrganization retrieves all members held by an organization (by the SFID on
	// committee_member.organization.id) using the by-organization secondary index (Org Lens, LFXV2-1865).
	ListMembersByOrganization(ctx context.Context, orgSFID string) ([]*model.CommitteeMember, error)
	// ListAllMembers retrieves every member across all committees via a full bucket scan.
	// This is intended only for backfill/repair operations that must read all members
	// independently of the secondary index.
	ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error)
	// EachMember streams every committee member to fn one at a time via a full bucket scan, without
	// materializing the whole set in memory — for backfill/repair over large buckets. Per-member read
	// errors are skipped (logged); iteration stops and returns the first error fn returns.
	EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error
	// ListMembersByEmail retrieves all committee members whose normalized email matches the given
	// address, using the by-email secondary index. The email is normalized (TrimSpace+ToLower) and
	// SHA-256-hashed before the scan.
	ListMembersByEmail(ctx context.Context, email string) ([]*model.CommitteeMember, error)
}
