// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// MemberIndexBackfiller writes the NATS KV member secondary indexes directly. It exists only for the
// data-repair subcommands that backfill index entries missing from members created before the indexes
// existed. Create, update, and delete maintain these indexes themselves, so no other code needs it, and
// only the NATS KV storage implements it; it goes away with the KV member storage.
type MemberIndexBackfiller interface {
	// IndexMemberByCommittee writes the secondary index entry mapping
	// committee_uid → member_uid so that ListMembersByCommittee can use a server-side
	// filtered scan instead of a full bucket scan.
	// Returns the written key and nil on success. Treats ErrKeyExists as idempotent success.
	IndexMemberByCommittee(ctx context.Context, member *model.CommitteeMember) (string, error)

	// IndexMemberByOrganization writes the secondary index entry mapping the holding org SFID
	// (committee_member.organization.id, normalized to 18 chars) → member_uid so that
	// ListMembersByOrganization can use a server-side filtered scan (Org Lens, LFXV2-1865).
	// Returns the written key, or an empty key (no-op) when the member has no organization.id.
	// Treats ErrKeyExists as idempotent success.
	IndexMemberByOrganization(ctx context.Context, member *model.CommitteeMember) (string, error)

	// IndexMemberByEmail writes the secondary index entry mapping the member's normalized email
	// (SHA-256 hex of strings.TrimSpace+strings.ToLower) → member_uid into the committee-members
	// bucket, so ListMembersByEmail can use a server-side filtered scan rather than a full bucket
	// scan. Returns the written key, or an empty key (no-op) when the member has no email. Treats
	// ErrKeyExists as idempotent success.
	IndexMemberByEmail(ctx context.Context, member *model.CommitteeMember) (string, error)

	// IndexMemberByUsername writes the secondary index entry mapping the member's normalized username
	// (SHA-256 hex of strings.TrimSpace+strings.ToLower) → member_uid into the committee-members
	// bucket, so ListMembersByUsername can use a server-side filtered scan rather than a full bucket
	// scan. Returns the written key, or an empty key (no-op) when the member has no username. Treats
	// ErrKeyExists as idempotent success.
	IndexMemberByUsername(ctx context.Context, member *model.CommitteeMember) (string, error)
}
