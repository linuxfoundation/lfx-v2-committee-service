// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	jose "gopkg.in/go-jose/go-jose.v2"
	"gopkg.in/go-jose/go-jose.v2/jwt"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	// auditSignedTokenLifetime matches the short-lived service tokens the v1 sync
	// helper signs; a minted token is reused until auditSignedTokenRenewal before expiry.
	auditSignedTokenLifetime = 5 * time.Minute
	auditSignedTokenRenewal  = time.Minute
	auditSignerMaxKeyBytes   = 64 << 10
	auditSignerJWKSTimeout   = 5 * time.Second
)

// AuditSigningSource mints the relay's own short-lived Heimdall service token
// with the Heimdall signing key, the way the v1 sync helper authenticates to
// v2 services. It signs only the configured service principal: never another
// subject, a user identity or actor evidence. Every minted token passes the
// same signature, audience, expiry and principal checks as a file credential.
type AuditSigningSource struct {
	principal string
	audience  string
	jwksURL   string
	keyPath   string
	// configuredKeyID is COMMITTEE_AUDIT_SIGNER_KEY_ID; when empty, keyID is
	// resolved from the JWKS and re-resolved after a key change or failure.
	configuredKeyID string
	auth            *JWTAuth
	client          *http.Client
	now             func() time.Time

	mu      sync.Mutex
	key     *rsa.PrivateKey
	keyID   string
	token   string
	expires time.Time
}

// LogValue and String never expose the signing key if the source is logged.
func (s *AuditSigningSource) LogValue() slog.Value { return slog.StringValue("[audit signer]") }

func (s *AuditSigningSource) String() string { return "[audit signer]" }

// NewAuditSigningSource loads the Heimdall signing key from keyPath. keyID is
// the JWKS key ID for it; when empty, it is found by matching the key's public
// half against the JWKS at the first delivery.
func NewAuditSigningSource(keyPath, principal, audience, jwksURL, keyID string) (*AuditSigningSource, error) {
	if strings.TrimSpace(keyPath) == "" || strings.TrimSpace(keyID) != keyID {
		return nil, errs.NewValidation("audit dispatch requires a signing key file, service principal, audience and JWKS URL")
	}
	auth, err := newAuditVerifier(principal, audience, jwksURL)
	if err != nil {
		return nil, err
	}
	key, err := readAuditSigningKey(keyPath)
	if err != nil {
		return nil, err
	}
	return &AuditSigningSource{principal: principal, audience: audience, jwksURL: jwksURL, keyPath: keyPath,
		configuredKeyID: keyID, keyID: keyID, key: key, auth: auth,
		client: &http.Client{Timeout: auditSignerJWKSTimeout}, now: time.Now}, nil
}

// readAuditSigningKey reads an RSA private key in PKCS#1 or PKCS#8 PEM form.
// Errors never include the path or key material.
func readAuditSigningKey(path string) (*rsa.PrivateKey, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errs.NewValidation("audit signing key is unavailable")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, auditSignerMaxKeyBytes+1))
	if err != nil || len(data) > auditSignerMaxKeyBytes {
		return nil, errs.NewValidation("invalid audit signing key")
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errs.NewValidation("invalid audit signing key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errs.NewValidation("invalid audit signing key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errs.NewValidation("audit signing key must be RSA")
	}
	return key, nil
}

// Authorization returns a valid service bearer for the configured principal,
// minting a new one when the cached token is near expiry. Each mint rereads the
// key file, so a key rotated in the mounted Secret is picked up without a restart.
func (s *AuditSigningSource) Authorization(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Add(auditSignedTokenRenewal).Before(s.expires) {
		return "Bearer " + s.token, nil
	}
	key, err := readAuditSigningKey(s.keyPath)
	if err != nil {
		return "", errs.NewServiceUnavailable("audit signing key is unavailable")
	}
	if !key.Equal(s.key) {
		s.key, s.keyID = key, s.configuredKeyID
	}
	if s.keyID == "" {
		keyID, err := s.resolveKeyID(ctx)
		if err != nil {
			return "", err
		}
		s.keyID = keyID
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.PS256, Key: s.key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", s.keyID))
	if err != nil {
		return "", errs.NewServiceUnavailable("audit service credential signing failed")
	}
	expires := now.Add(auditSignedTokenLifetime)
	registered := jwt.Claims{
		Issuer: defaultIssuer, Subject: s.principal, ID: uuid.NewString(),
		IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)), Expiry: jwt.NewNumericDate(expires),
	}
	// A single-string audience, as Heimdall and the v1 sync helper issue it.
	token, err := jwt.Signed(signer).Claims(registered).Claims(map[string]any{"aud": s.audience, "principal": s.principal}).CompactSerialize()
	if err != nil || len("Bearer "+token) > model.CommitteeAuditMaxAuthorizationBytes {
		return "", errs.NewServiceUnavailable("audit service credential signing failed")
	}
	// Verify against Heimdall's published keys exactly as a file credential is
	// verified, so a wrong key or key ID fails here rather than at the indexer.
	if !s.verify(ctx, token) {
		// The verifier caches Heimdall's keys, which may predate a rotation:
		// retry once with a fresh one before failing.
		if auth, err := newAuditVerifier(s.principal, s.audience, s.jwksURL); err == nil {
			s.auth = auth
		}
		if !s.verify(ctx, token) {
			// Look the key ID up again next time, in case Heimdall rotated it.
			s.keyID = s.configuredKeyID
			return "", errs.NewServiceUnavailable("audit service credential verification failed")
		}
	}
	s.token, s.expires = token, expires
	return "Bearer " + token, nil
}

// verify applies the checks every relay credential must pass.
func (s *AuditSigningSource) verify(ctx context.Context, token string) bool {
	identity, err := s.auth.ParseIdentity(ctx, token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return err == nil && identity.Principal == s.principal && !identity.ExpiresAt.IsZero()
}

// resolveKeyID finds the JWKS key whose public half matches the signing key.
func (s *AuditSigningSource) resolveKeyID(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.jwksURL, nil)
	if err != nil {
		return "", errs.NewServiceUnavailable("audit signing key ID is unavailable")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return "", errs.NewServiceUnavailable("audit signing key ID is unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	var set jose.JSONWebKeySet
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&set) != nil {
		return "", errs.NewServiceUnavailable("audit signing key ID is unavailable")
	}
	for _, candidate := range set.Keys {
		public, ok := candidate.Key.(*rsa.PublicKey)
		if ok && candidate.KeyID != "" && public.E == s.key.E && public.N.Cmp(s.key.N) == 0 {
			return candidate.KeyID, nil
		}
	}
	return "", errs.NewServiceUnavailable("audit signing key is not published by Heimdall")
}
