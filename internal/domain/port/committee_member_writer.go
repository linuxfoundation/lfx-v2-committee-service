// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeMemberWriter provides access to committee member writing operations
type CommitteeMemberWriter interface {
	// CreateMember creates a new committee member. A committee holds at most one member per email; a
	// second create for the same email returns errs.Conflict and leaves storage unchanged. A failed
	// create leaves nothing behind.
	CreateMember(ctx context.Context, member *model.CommitteeMember) error
	// UpdateMember updates an existing committee member using the revision as an optimistic-concurrency
	// token (a stale revision returns errs.Conflict). When the member's email changed, the new email is
	// enforced as in CreateMember; the member's lookups follow its email, organization, and username.
	UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error)
	// DeleteMember removes a committee member and everything that identifies it for lookup.
	DeleteMember(ctx context.Context, uid string, revision uint64) error
}
