// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeInviteWriter provides access to committee invite writing operations
type CommitteeInviteWriter interface {
	// CreateInvite creates a new committee invite
	CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error
	// UpdateInvite updates an existing committee invite
	UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error

	// Checkers for uniqueness
	UniqueInvite(ctx context.Context, invite *model.CommitteeInvite) (string, error)
}
