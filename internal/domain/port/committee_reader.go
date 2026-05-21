// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeReader provides access to committee reading operations
type CommitteeReader interface {
	CommitteeBaseReader
	CommitteeSettingsReader
	CommitteeMemberReader
	CommitteeInviteReader
	CommitteeApplicationReader
}

// CommitteeBaseReader handles committee base data reading operations
type CommitteeBaseReader interface {
	GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
	GetRevision(ctx context.Context, uid string) (uint64, error)
	ListAllUIDs(ctx context.Context) ([]string, error)
}

// CommitteeSettingsReader handles committee settings reading operations
type CommitteeSettingsReader interface {
	GetSettings(ctx context.Context, committeeUID string) (*model.CommitteeSettings, uint64, error)
	// GetSettingsUIDByInviteUID looks up the committee UID for a given invite UID via the
	// secondary index written at invite-send time. Returns NotFound if the invite is not tracked
	// by this service.
	GetSettingsUIDByInviteUID(ctx context.Context, inviteUID string) (string, error)
}
