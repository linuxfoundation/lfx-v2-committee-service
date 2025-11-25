// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
)

// UserReader handles user data reading operations
type UserReader interface {
	// SubByEmail retrieves a user sub (username) by email address
	SubByEmail(ctx context.Context, email string) (string, error)
}
