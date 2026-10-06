// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeApplicationReader provides access to committee application reading operations
type CommitteeApplicationReader interface {
	// GetApplication retrieves a committee application by UID
	GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error)
	// ListApplications retrieves all applications for a given committee UID
	ListApplications(ctx context.Context, committeeUID string) ([]*model.CommitteeApplication, error)
	// ListAllApplications retrieves every application across all committees.
	// Intended only for backfill/repair operations (e.g. the reindex-applications CLI subcommand).
	ListAllApplications(ctx context.Context) ([]*model.CommitteeApplication, error)
}
