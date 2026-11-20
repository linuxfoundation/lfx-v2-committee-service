// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// Authenticator defines the interface for authentication operations
type Authenticator interface {
	// ParsePrincipal parses and validates a JWT token, returning the principal and email (if present in claims).
	ParsePrincipal(ctx context.Context, token string, logger *slog.Logger) (principal, email string, err error)
}

// AuditAuthenticator retains verified delegation without reparsing an untrusted JWT.
type AuditAuthenticator interface {
	ParseIdentity(context.Context, string, *slog.Logger) (*model.AuthenticatedIdentity, error)
}
