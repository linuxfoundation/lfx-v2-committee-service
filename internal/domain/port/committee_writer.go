// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeWriter provides access to committee writing operations
type CommitteeWriter interface {
	CommitteeBaseWriter
	CommitteeSettingsWriter
	CommitteeMemberWriter
	CommitteeInviteWriter
	CommitteeApplicationWriter
}

// CommitteeBaseWriter handles committee base data writing operations
// For create and delete, settings will be created or deleted as well
type CommitteeBaseWriter interface {
	Create(ctx context.Context, committee *model.Committee) error
	UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error
	Delete(ctx context.Context, uid string, revision uint64) error

	// UpdateHasMailingList atomically reads the committee, sets has_mailing_list, and writes back.
	// Returns (committee, true, nil) if the flag changed and was written; (nil, false, nil) if already correct (no write).
	UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error)

	// Checkers for uniqueness
	UniqueNameProject(ctx context.Context, committee *model.Committee) (string, error)
	UniqueSSOGroupName(ctx context.Context, committee *model.Committee) (string, error)
}

// CommitteeSettingsWriter handles committee settings writing operations
type CommitteeSettingsWriter interface {
	UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error
}
