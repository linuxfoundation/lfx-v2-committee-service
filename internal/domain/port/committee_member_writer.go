// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeMemberWriter provides access to committee member writing operations
type CommitteeMemberWriter interface {
	// CreateMember creates a new committee member
	CreateMember(ctx context.Context, member *model.CommitteeMember) error
	// UpdateMember updates an existing committee member
	UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error)
	// DeleteMember removes a committee member
	DeleteMember(ctx context.Context, uid string, revision uint64) error

	// Checkers for uniqueness
	UniqueMember(ctx context.Context, member *model.CommitteeMember) (string, error)
}
