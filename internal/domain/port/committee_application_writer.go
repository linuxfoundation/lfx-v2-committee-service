// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeApplicationWriter provides access to committee application writing operations
type CommitteeApplicationWriter interface {
	// CreateApplication creates a new committee application
	CreateApplication(ctx context.Context, application *model.CommitteeApplication) error
	// UpdateApplication updates an existing committee application
	UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error

	// Checkers for uniqueness
	UniqueApplication(ctx context.Context, application *model.CommitteeApplication) (string, error)
}
