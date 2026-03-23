// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeInviteReader provides access to committee invite reading operations
type CommitteeInviteReader interface {
	// GetInvite retrieves a committee invite by UID
	GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error)
	// ListInvites retrieves all invites for a given committee UID
	ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error)
}
