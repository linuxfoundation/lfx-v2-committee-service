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
	// Create persists the committee and its optional settings. A committee name is unique per
	// project and an SSO group name (when set) is unique across committees. A duplicate name
	// returns errs.Conflict; a duplicate SSO group name returns an errs.Conflict that wraps
	// errs.ErrSSOGroupNameTaken so the caller can retry with another generated name. A failed
	// create leaves nothing reserved behind.
	Create(ctx context.Context, committee *model.Committee) error
	// UpdateBase updates the committee base using the revision as an optimistic-concurrency token.
	// When the project, name or SSO group name changed, the new uniqueness is enforced as in Create
	// and the old reservations are released after a successful update.
	UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error
	// Delete removes the committee and its settings and releases its name and SSO group name.
	Delete(ctx context.Context, uid string, revision uint64) error

	// UpdateHasMailingList atomically reads the committee, sets has_mailing_list, and writes back.
	// Returns (committee, true, nil) if the flag changed and was written; (nil, false, nil) if already correct (no write).
	UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error)

	// UpdateTotalMembers atomically reads the committee, sets total_members, and writes back.
	// Returns (committee, true, nil) if the count changed and was written; (nil, false, nil) if already correct (no write).
	UpdateTotalMembers(ctx context.Context, uid string, totalMembers int) (*model.CommitteeBase, bool, error)
}

// CommitteeSettingsWriter handles committee settings writing operations
type CommitteeSettingsWriter interface {
	UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error
}
