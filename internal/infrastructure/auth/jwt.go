// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
)

const (
	// PS256 is the default for Heimdall's JWT finalizer.
	signatureAlgorithm = validator.PS256
	defaultIssuer      = "heimdall"
	defaultAudience    = "lfx-v2-committee-service"
	defaultJWKSURL     = "http://heimdall:4457/.well-known/jwks"
)

// JWTAuthConfig holds the configuration parameters for JWT authentication.
type JWTAuthConfig struct {
	// JWKSURL is the URL to the JSON Web Key Set endpoint
	JWKSURL string
	// Audience is the intended audience for the JWT token
	Audience string
	// MockLocalPrincipal is used for local development to bypass JWT validation
	MockLocalPrincipal string
}

var (
	// Factory for custom JWT claims target.
	customClaims = func() validator.CustomClaims {
		return &HeimdallClaims{}
	}
)

// HeimdallClaims contains extra custom claims we want to parse from the JWT
// token.
type HeimdallClaims struct {
	Principal string `json:"principal"`
	Email     string `json:"email,omitempty"`
}

// Validate provides additional middleware validation of any claims defined in
// HeimdallClaims.
func (c *HeimdallClaims) Validate(_ context.Context) error {
	if c.Principal == "" {
		return errors.New("principal must be provided")
	}
	return nil
}

type JWTAuth struct {
	validator *validator.Validator
	config    JWTAuthConfig
}

// ParsePrincipal extracts the principal from the JWT claims.
func (j *JWTAuth) ParsePrincipal(ctx context.Context, token string, logger *slog.Logger) (string, error) {

	if j.validator == nil {
		return "", errors.New("JWT validator is not set up")
	}

	parsedJWT, err := j.validator.ValidateToken(ctx, token)
	if err != nil {
		// Drop tertiary (and deeper) nested errors for security reasons. This is
		// using colons as an approximation for error nesting, which may not
		// exactly match to error boundaries. Unwrapping the error twice, then
		// dropping the suffix of the 3rd error's String() method could be more
		// accurate to error boundaries, but could also expose tertiary errors if
		// errors are not wrapped with Go 1.13 `%w` semantics.
		logger.With("default_audience", defaultAudience).With("default_issuer", defaultIssuer).With("error", err).WarnContext(ctx, "authorization failed")
		errString := err.Error()
		firstColon := strings.Index(errString, ":")
		if firstColon != -1 && firstColon+1 < len(errString) {
			errString = strings.Replace(errString, ": go-jose/go-jose/jwt", "", 1)
			secondColon := strings.Index(errString[firstColon+1:], ":")
			if secondColon != -1 {
				// Error has two colons (which may be 3 or more errors), so drop the
				// second colon and everything after it.
				errString = errString[:firstColon+secondColon+1]
			}
		}
		return "", errors.New(errString)
	}

	claims, ok := parsedJWT.(*validator.ValidatedClaims)
	if !ok {
		// This should never happen.
		return "", errors.New("failed to get validated authorization claims")
	}

	customClaims, ok := claims.CustomClaims.(*HeimdallClaims)
	if !ok {
		// This should never happen.
		return "", errors.New("failed to get custom authorization claims")
	}

	return customClaims.Principal, nil
}

// NewJWTAuth creates a new JWT authentication service
func NewJWTAuth(config JWTAuthConfig) (*JWTAuth, error) {
	// Set up defaults if not provided
	jwksURLStr := config.JWKSURL
	if jwksURLStr == "" {
		jwksURLStr = defaultJWKSURL
	}
	audience := config.Audience
	if audience == "" {
		audience = defaultAudience
	}

	// Set up Heimdall JWKS key provider.
	jwksURL, err := url.Parse(jwksURLStr)
	if err != nil {
		slog.With("error", err).Error("invalid JWKS_URL")
		return nil, err
	}
	var issuer *url.URL
	issuer, err = url.Parse(defaultIssuer)
	if err != nil {
		// This shouldn't happen; a bare hostname is a valid URL.
		slog.Error("unexpected URL parsing of default issuer")
		return nil, err
	}
	provider := jwks.NewCachingProvider(issuer, 5*time.Minute, jwks.WithCustomJWKSURI(jwksURL))

	// Set up the JWT validator.
	jwtValidator, err := validator.New(
		provider.KeyFunc,
		signatureAlgorithm,
		issuer.String(),
		[]string{audience},
		validator.WithCustomClaims(customClaims),
		validator.WithAllowedClockSkew(5*time.Second),
	)
	if err != nil {
		slog.With("error", err).Error("failed to set up the Heimdall JWT validator")
		return nil, err
	}

	return &JWTAuth{
		validator: jwtValidator,
		config:    config,
	}, nil
}
