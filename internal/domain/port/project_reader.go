// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// ProjectReader handles project data reading operations
type ProjectReader interface {
	Name(ctx context.Context, uid string) (string, error)
	Slug(ctx context.Context, uid string) (string, error)
	// Writers returns the LFID writers list for the given project UID.
	// Returns an empty slice (no error) when the project has no writers configured.
	Writers(ctx context.Context, uid string) ([]model.CommitteeUser, error)
}
