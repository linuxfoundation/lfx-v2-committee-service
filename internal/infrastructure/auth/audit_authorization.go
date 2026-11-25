// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// AuditAuthorizationSource reads a refreshed service bearer from a projected
// credential file for each dispatch. It validates signature, audience, expiry
// and the admitted service identity before an event reaches the broker.
// Provisioning/refresh is external; no user JWT is retained or used as fallback.
type AuditAuthorizationSource struct {
	path      string
	principal string
	auth      *JWTAuth
}

// NewAuditAuthorizationSource configures a private, externally refreshed
// Heimdall credential read from a file. It does not mint tokens.
func NewAuditAuthorizationSource(path, principal, audience, jwksURL string) (*AuditAuthorizationSource, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errs.NewValidation("audit dispatch requires a token file, service principal, audience and JWKS URL")
	}
	auth, err := newAuditVerifier(principal, audience, jwksURL)
	if err != nil {
		return nil, err
	}
	return &AuditAuthorizationSource{path: path, principal: principal, auth: auth}, nil
}

// newAuditVerifier validates the relay's static identity configuration and
// builds the verifier every relay credential must pass.
func newAuditVerifier(principal, audience, jwksURL string) (*JWTAuth, error) {
	if strings.TrimSpace(audience) == "" ||
		strings.TrimSpace(principal) != principal || strings.ContainsAny(principal, " \r\n\t") ||
		!strings.HasSuffix(principal, "@clients") || strings.TrimSuffix(principal, "@clients") == "" {
		return nil, errs.NewValidation("audit dispatch requires a service principal, audience and JWKS URL")
	}
	parsed, err := url.Parse(jwksURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errs.NewValidation("audit dispatch requires a valid HTTP(S) JWKS URL without credentials")
	}
	auth, err := NewJWTAuth(JWTAuthConfig{JWKSURL: jwksURL, Audience: audience})
	if err != nil {
		return nil, errs.NewValidation("invalid audit token verification configuration")
	}
	return auth, nil
}

// Authorization reloads and validates the current credential without exposing
// its contents in logs, errors, tracing, or retained outbox state.
func (s *AuditAuthorizationSource) Authorization(ctx context.Context) (string, error) {
	file, err := os.Open(s.path)
	if err != nil {
		return "", errs.NewServiceUnavailable("audit service credential is unavailable")
	}
	defer func() { _ = file.Close() }()
	const maxCredentialBytes = model.CommitteeAuditMaxAuthorizationBytes
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || len(data) > maxCredentialBytes {
		return "", errs.NewServiceUnavailable("invalid audit service credential")
	}
	token := strings.TrimSpace(string(data))
	token = strings.TrimPrefix(token, "Bearer ")
	if strings.Count(token, ".") != 2 || strings.ContainsAny(token, " \r\n\t") {
		return "", errs.NewServiceUnavailable("invalid audit service credential")
	}
	if len("Bearer "+token) > maxCredentialBytes {
		return "", errs.NewServiceUnavailable("invalid audit service credential")
	}
	// JWTAuth sanitizes errors but logs validator errors itself. Use a discard
	// logger on this private relay path as an additional credential boundary.
	identity, err := s.auth.ParseIdentity(ctx, token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || identity.Principal != s.principal || identity.ExpiresAt.IsZero() {
		return "", errs.NewServiceUnavailable("audit service credential verification failed")
	}
	return "Bearer " + token, nil
}
