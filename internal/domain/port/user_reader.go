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
	// EmailsByPrincipal retrieves all email addresses (primary and alternate) for a user by their
	// principal (subject identifier or username) via the NATS subject lfx.auth-service.user_emails.read.
	EmailsByPrincipal(ctx context.Context, principal string) (*model.UserEmails, error)
}
