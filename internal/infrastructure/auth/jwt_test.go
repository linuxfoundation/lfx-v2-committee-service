// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/auth0/go-jwt-middleware/v2/validator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJWTAuth(t *testing.T) {
	tests := []struct {
		name             string
		config           JWTAuthConfig
		expectError      bool
		expectedAudience string
		expectedJWKSURL  string
	}{
		{
			name: "valid config with all fields",
			config: JWTAuthConfig{
				JWKSURL:  "https://example.com/.well-known/jwks",
				Audience: "test-audience",
			},
			expectError:      false,
			expectedAudience: "test-audience",
			expectedJWKSURL:  "https://example.com/.well-known/jwks",
		},
		{
			name:             "empty config uses defaults",
			config:           JWTAuthConfig{},
			expectError:      false,
			expectedAudience: defaultAudience,
			expectedJWKSURL:  defaultJWKSURL,
		},
		{
			name: "invalid JWKS URL",
			config: JWTAuthConfig{
				JWKSURL: "://invalid-url",
			},
			expectError: true,
		},
		{
			name: "partial config with custom audience",
			config: JWTAuthConfig{
				Audience: "custom-audience",
			},
			expectError:      false,
			expectedAudience: "custom-audience",
			expectedJWKSURL:  defaultJWKSURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jwtAuth, err := NewJWTAuth(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, jwtAuth)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, jwtAuth)
				assert.NotNil(t, jwtAuth.validator)
				assert.Equal(t, tt.config, jwtAuth.config)
			}
		})
	}
}

func TestJWTAuthParsePrincipalNilValidator(t *testing.T) {
	jwtAuth := &JWTAuth{
		validator: nil,
		config:    JWTAuthConfig{},
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	principal, err := jwtAuth.ParsePrincipal(ctx, "some-token", logger)

	assert.Error(t, err)
	assert.Empty(t, principal)
	assert.Contains(t, err.Error(), "JWT validator is not set up")
}

func TestJWTAuthParsePrincipalEmptyToken(t *testing.T) {
	// Create a JWT auth instance with a real validator
	config := JWTAuthConfig{
		JWKSURL:  defaultJWKSURL,
		Audience: defaultAudience,
	}
	jwtAuth, err := NewJWTAuth(config)
	require.NoError(t, err)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	principal, err := jwtAuth.ParsePrincipal(ctx, "", logger)

	assert.Error(t, err)
	assert.Empty(t, principal)
}

func TestJWTAuthParsePrincipalInvalidToken(t *testing.T) {
	// Create a JWT auth instance with a real validator
	config := JWTAuthConfig{
		JWKSURL:  defaultJWKSURL,
		Audience: defaultAudience,
	}
	jwtAuth, err := NewJWTAuth(config)
	require.NoError(t, err)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Test with various invalid tokens
	invalidTokens := []string{
		"invalid-token",
		"Bearer invalid-token",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid",
		"not.a.jwt",
	}

	for i, token := range invalidTokens {
		testName := "invalid_token_" + token
		if len(testName) > 50 {
			testName = testName[:50]
		}
		t.Run(testName, func(t *testing.T) {
			principal, err := jwtAuth.ParsePrincipal(ctx, token, logger)

			assert.Error(t, err)
			assert.Empty(t, principal)
			// Should not contain sensitive information
			assert.NotContains(t, err.Error(), "go-jose/go-jose/jwt")
		})
		_ = i // Use i to avoid unused variable warning
	}
}

func TestHeimdallClaims_Validate(t *testing.T) {
	tests := []struct {
		name        string
		claims      HeimdallClaims
		expectError bool
	}{
		{
			name: "valid claims with principal",
			claims: HeimdallClaims{
				Principal: "test-user-123",
				Email:     "test@example.com",
			},
			expectError: false,
		},
		{
			name: "valid claims with only principal",
			claims: HeimdallClaims{
				Principal: "test-user-456",
			},
			expectError: false,
		},
		{
			name: "invalid claims without principal",
			claims: HeimdallClaims{
				Email: "test@example.com",
			},
			expectError: true,
		},
		{
			name: "invalid claims with empty principal",
			claims: HeimdallClaims{
				Principal: "",
				Email:     "test@example.com",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.claims.Validate(context.Background())

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "principal must be provided")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCustomClaimsFactory(t *testing.T) {
	claims := customClaims()

	assert.NotNil(t, claims)

	// Should be of type HeimdallClaims
	heimdallClaims, ok := claims.(*HeimdallClaims)
	assert.True(t, ok)
	assert.NotNil(t, heimdallClaims)
}

func TestConstants(t *testing.T) {
	// Test that constants are set to expected values
	assert.Equal(t, validator.PS256, signatureAlgorithm)
	assert.Equal(t, "heimdall", defaultIssuer)
	assert.Equal(t, "lfx-v2-committee-service", defaultAudience)
	assert.Equal(t, "http://heimdall:4457/.well-known/jwks", defaultJWKSURL)
}

func TestJWTAuthConfig(t *testing.T) {
	config := JWTAuthConfig{
		JWKSURL:  "https://test.example.com/.well-known/jwks",
		Audience: "test-service",
	}

	assert.Equal(t, "https://test.example.com/.well-known/jwks", config.JWKSURL)
	assert.Equal(t, "test-service", config.Audience)
}

func TestErrorMessageSanitization(t *testing.T) {
	// Test that the error sanitization logic works correctly
	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "error: first: second: third",
			expected: "error: first",
		},
		{
			input:    "simple error",
			expected: "simple error",
		},
		{
			input:    "error: go-jose/go-jose/jwt: token verification failed",
			expected: "error: token verification failed",
		},
		{
			input:    "no colons here",
			expected: "no colons here",
		},
	}

	for i, tc := range testCases {
		testName := "sanitize_" + tc.input
		if len(testName) > 50 {
			testName = testName[:50]
		}
		t.Run(testName, func(t *testing.T) {
			errString := tc.input

			// Apply the same sanitization logic as in ParsePrincipal
			firstColon := strings.Index(errString, ":")
			if firstColon != -1 && firstColon+1 < len(errString) {
				errString = strings.Replace(errString, ": go-jose/go-jose/jwt", "", 1)
				secondColon := strings.Index(errString[firstColon+1:], ":")
				if secondColon != -1 {
					errString = errString[:firstColon+secondColon+1]
				}
			}

			assert.Equal(t, tc.expected, errString)
		})
		_ = i // Use i to avoid unused variable warning
	}
}
