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
	// ListMembers retrieves all members for a given committee UID using the secondary index.
	ListMembers(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error)
	// ListAllMembers retrieves every member across all committees via a full bucket scan.
	// This is intended only for backfill/repair operations that must read all members
	// independently of the secondary index.
	ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error)
}
