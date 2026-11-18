// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	jose "gopkg.in/go-jose/go-jose.v2"
	"gopkg.in/go-jose/go-jose.v2/jwt"
)

func auditJWTFixture(t *testing.T) (string, func(map[string]any, string, time.Time) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: string(jose.PS256), Use: "sig"}}}))
	}))
	t.Cleanup(server.Close)
	sign := func(claims map[string]any, audience string, expiry time.Time) string {
		t.Helper()
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.PS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
		require.NoError(t, err)
		registered := jwt.Claims{Issuer: defaultIssuer, Subject: "effective-user", Audience: jwt.Audience{audience}, IssuedAt: jwt.NewNumericDate(time.Now())}
		if !expiry.IsZero() {
			registered.Expiry = jwt.NewNumericDate(expiry)
		}
		token, err := jwt.Signed(signer).Claims(registered).Claims(claims).CompactSerialize()
		require.NoError(t, err)
		return token
	}
	return server.URL, sign
}

func TestJWTAuth_ParseIdentity(t *testing.T) {
	jwks, sign := auditJWTFixture(t)
	auth, err := NewJWTAuth(JWTAuthConfig{JWKSURL: jwks, Audience: defaultAudience})
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name      string
		actor     any
		present   bool
		wantError bool
	}{
		{"no delegation", nil, false, false},
		{"verified actor", map[string]any{"sub": "auth0|original-user", "username": "first-last"}, true, false},
		{"preserve signed whitespace", map[string]any{"sub": "auth0|original-user", "username": " first-last "}, true, false},
		{"null", nil, true, true},
		{"string", "forged", true, true},
		{"missing username", map[string]any{"sub": "auth0|original-user"}, true, true},
		{"wrong sub type", map[string]any{"sub": 123, "username": "first-last"}, true, true},
		{"nested", map[string]any{"sub": "auth0|original-user", "username": "first-last", "act": nil}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{"principal": "effective-user"}
			if tc.present {
				claims["act"] = tc.actor
			}
			token := sign(claims, defaultAudience, time.Now().Add(time.Minute))
			principal, _, err := auth.ParsePrincipal(context.Background(), token, logger)
			require.NoError(t, err, "legacy principal extraction must continue ignoring act")
			require.Equal(t, "effective-user", principal)
			identity, err := auth.ParseIdentity(context.Background(), token, logger)
			if tc.wantError {
				require.Error(t, err)
				require.Nil(t, identity)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "effective-user", identity.Principal, "delegation must not change authorization principal")
			if tc.present {
				require.Equal(t, "auth0|original-user", identity.Actor.Subject)
			} else {
				require.Nil(t, identity.Actor)
			}
		})
	}
	t.Run("tampered signature", func(t *testing.T) {
		token := sign(map[string]any{"principal": "effective-user"}, defaultAudience, time.Now().Add(time.Minute))
		_, err := auth.ParseIdentity(context.Background(), token[:len(token)-8]+"tampered", logger)
		require.Error(t, err)
	})
}

func TestAuditAuthorizationSource_Authorization(t *testing.T) {
	jwks, sign := auditJWTFixture(t)
	path := filepath.Join(t.TempDir(), "authorization")
	source, err := NewAuditAuthorizationSource(path, "test-relay@clients", "indexer-service", jwks)
	require.NoError(t, err)
	_, err = source.Authorization(context.Background())
	require.Error(t, err, "missing credential pauses delivery but does not invalidate the source configuration")
	for _, tc := range []struct {
		name, principal, audience string
		expiry                    time.Time
		wantError                 bool
	}{
		{"valid", "test-relay@clients", "indexer-service", time.Now().Add(time.Minute), false},
		{"wrong principal", "other-client@clients", "indexer-service", time.Now().Add(time.Minute), true},
		{"user token", "first-last", "indexer-service", time.Now().Add(time.Minute), true},
		{"wrong audience", "test-relay@clients", "committee-service", time.Now().Add(time.Minute), true},
		{"expired", "test-relay@clients", "indexer-service", time.Now().Add(-time.Minute), true},
		{"missing expiry", "test-relay@clients", "indexer-service", time.Time{}, true},
		{"rotated", "test-relay@clients", "indexer-service", time.Now().Add(2 * time.Minute), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := sign(map[string]any{"principal": tc.principal}, tc.audience, tc.expiry)
			require.NoError(t, os.WriteFile(path, []byte(token), 0600))
			got, err := source.Authorization(context.Background())
			if tc.wantError {
				require.Error(t, err)
				require.Empty(t, got)
				require.NotContains(t, err.Error(), token)
			} else {
				require.NoError(t, err)
				require.Equal(t, "Bearer "+token, got)
			}
		})
	}
	t.Run("transient JWKS failure recovers on retry", func(t *testing.T) {
		var available atomic.Bool
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !available.Load() {
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			http.Redirect(w, r, jwks, http.StatusFound)
		}))
		defer proxy.Close()
		token := sign(map[string]any{"principal": "test-relay@clients"}, "indexer-service", time.Now().Add(time.Minute))
		require.NoError(t, os.WriteFile(path, []byte(token), 0600))
		retrying, err := NewAuditAuthorizationSource(path, "test-relay@clients", "indexer-service", proxy.URL)
		require.NoError(t, err)
		_, err = retrying.Authorization(context.Background())
		require.Error(t, err)
		available.Store(true)
		got, err := retrying.Authorization(context.Background())
		require.NoError(t, err)
		require.Equal(t, "Bearer "+token, got)
	})
}
