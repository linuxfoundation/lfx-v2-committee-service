// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeInviteWriter provides access to committee invite writing operations
type CommitteeInviteWriter interface {
	// CreateInvite creates a new committee invite. At most one invite exists per
	// (committee, invitee email); a second create for the same pair returns errs.Conflict and
	// leaves storage unchanged, and so does a uid that another invite already holds. A nil invite, or one
	// without a UID or a CommitteeUID, returns errs.Validation.
	CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error
	// UpdateInvite updates an existing committee invite, using the revision as an optimistic-concurrency
	// token: a stale revision returns errs.Conflict and a missing invite errs.NotFound.
	UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error
}
