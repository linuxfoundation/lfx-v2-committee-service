// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
)

// ProjectRetriever handles project data reading operations
type ProjectRetriever interface {
	Slug(ctx context.Context, uid string) (string, error)
}
