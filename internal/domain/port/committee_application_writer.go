// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeApplicationWriter provides access to committee application writing operations
type CommitteeApplicationWriter interface {
	// CreateApplication creates a new committee application. At most one application exists per
	// (committee, applicant email); a second create for the same pair returns errs.Conflict and
	// leaves storage unchanged, and so does a uid that another application already holds. A nil
	// application, or one without a UID or a CommitteeUID, returns errs.Validation.
	CreateApplication(ctx context.Context, application *model.CommitteeApplication) error
	// UpdateApplication updates an existing committee application, using the revision as an
	// optimistic-concurrency token: a stale revision returns errs.Conflict and a missing application
	// errs.NotFound.
	UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error
	// DeleteApplication removes a committee application record and releases its uniqueness from storage.
	// Intended for cascade deletion when the parent committee is deleted; no revision check is performed,
	// and deleting an application that is already gone is not an error.
	DeleteApplication(ctx context.Context, application *model.CommitteeApplication) error
}
