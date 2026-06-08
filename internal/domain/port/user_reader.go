// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// UserReader handles user data reading operations
type UserReader interface {
	// UsernameByEmail resolves the registered LFID username for the given primary email address.
	UsernameByEmail(ctx context.Context, email string) (string, error)
	// EmailsByPrincipal retrieves all email addresses (primary and alternate) for a user
	// by sending their Auth0 sub (e.g. "auth0|abc123") as the auth_token to the auth-service.
	EmailsByPrincipal(ctx context.Context, principal string) (*model.UserEmails, error)
	// UserMetadataByPrincipal retrieves profile metadata for a user from the auth service by their principal.
	UserMetadataByPrincipal(ctx context.Context, principal string) (*model.UserMetadata, error)
}
