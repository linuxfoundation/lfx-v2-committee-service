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
	// leaves storage unchanged.
	CreateApplication(ctx context.Context, application *model.CommitteeApplication) error
	// UpdateApplication updates an existing committee application
	UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error
	// DeleteApplication removes a committee application record and releases its uniqueness from storage.
	// Intended for cascade deletion when the parent committee is deleted; no revision check is performed.
	DeleteApplication(ctx context.Context, application *model.CommitteeApplication) error
}
