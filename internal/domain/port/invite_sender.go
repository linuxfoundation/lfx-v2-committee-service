// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
	"time"

	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
)

// InviteResult holds the invite metadata returned by the invite service.
type InviteResult struct {
	InviteUID      string
	RecipientEmail string
	ExpiresAt      time.Time
}

// InviteSender sends LFID invite requests via the invite service for users who
// do not yet have an LFID account.
type InviteSender interface {
	SendInvite(ctx context.Context, req inviteapi.SendInviteRequest) (InviteResult, error)
}
