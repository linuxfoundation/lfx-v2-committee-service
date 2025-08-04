// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
)

// ProjectReader handles project data reading operations
type ProjectReader interface {
	Name(ctx context.Context, uid string) (string, error)
	Slug(ctx context.Context, uid string) (string, error)
}
