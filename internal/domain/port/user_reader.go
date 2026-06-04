// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// UserReader handles user data reading operations
type UserReader interface {
	// SubByEmail retrieves a user sub (username) by email address
	SubByEmail(ctx context.Context, email string) (string, error)
	// EmailsByUserToken retrieves all email addresses (primary and alternate) for the
	// authenticated caller. authToken must be the raw JWT only — no "Bearer " prefix.
	EmailsByUserToken(ctx context.Context, authToken string) (*model.UserEmails, error)
	// UserMetadataByPrincipal retrieves profile metadata for a user from the auth service by their principal.
	UserMetadataByPrincipal(ctx context.Context, principal string) (*model.UserMetadata, error)
}
