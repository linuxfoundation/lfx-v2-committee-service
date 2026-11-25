// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	jose "gopkg.in/go-jose/go-jose.v2"
	"gopkg.in/go-jose/go-jose.v2/jwt"
)

// signerJWKS serves the published keys; tests can change them at any time.
type signerJWKS struct {
	mu   sync.Mutex
	keys map[string]*rsa.PrivateKey
	down bool
	URL  string
}

func newSignerJWKS(t *testing.T) *signerJWKS {
	t.Helper()
	j := &signerJWKS{keys: map[string]*rsa.PrivateKey{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		set := jose.JSONWebKeySet{}
		for kid, key := range j.keys {
			set.Keys = append(set.Keys, jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: string(jose.PS256), Use: "sig"})
		}
		require.NoError(t, json.NewEncoder(w).Encode(set))
	}))
	t.Cleanup(server.Close)
	j.URL = server.URL
	return j
}

func (j *signerJWKS) publish(kid string, key *rsa.PrivateKey) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.keys[kid] = key
}

func newSigningKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// writeSigningKey writes key as PEM (PKCS#1 or PKCS#8) to path, replacing it.
func writeSigningKey(t *testing.T, path string, key *rsa.PrivateKey, pkcs8 bool) {
	t.Helper()
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	}
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
}

// relayIdentity verifies bearer with an independent verifier and returns its
// principal and claims.
func relayIdentity(t *testing.T, jwksURL, bearer string) (string, map[string]any, string) {
	t.Helper()
	require.True(t, strings.HasPrefix(bearer, "Bearer "))
	token := strings.TrimPrefix(bearer, "Bearer ")
	verifier, err := NewJWTAuth(JWTAuthConfig{JWKSURL: jwksURL, Audience: "lfx-v2-indexer-service"})
	require.NoError(t, err)
	identity, err := verifier.ParseIdentity(context.Background(), token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	parsed, err := jwt.ParseSigned(token)
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, parsed.UnsafeClaimsWithoutVerification(&claims))
	return identity.Principal, claims, parsed.Headers[0].KeyID
}

func TestNewAuditSigningSource(t *testing.T) {
	jwks := newSignerJWKS(t)
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	writeSigningKey(t, keyPath, newSigningKey(t), false)
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, data, 0o600))
		return path
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	require.NoError(t, err)

	for _, tc := range []struct {
		name, path, principal, audience, jwks, keyID string
		wantErr                                      bool
	}{
		{name: "valid", path: keyPath, principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL},
		{name: "valid with key ID", path: keyPath, principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, keyID: "heimdall-key"},
		{name: "missing key file", path: filepath.Join(dir, "private-marker.pem"), principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "not PEM", path: write("garbage.pem", []byte("private-marker")), principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "not RSA", path: write("ec.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER})), principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "empty key path", principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "user principal", path: keyPath, principal: "first-last", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "padded principal", path: keyPath, principal: " committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "bare @clients", path: keyPath, principal: "@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, wantErr: true},
		{name: "missing audience", path: keyPath, principal: "committee-relay@clients", jwks: jwks.URL, wantErr: true},
		{name: "credentialed JWKS URL", path: keyPath, principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: (&url.URL{Scheme: "https", Host: "example.com", Path: "/jwks", User: url.UserPassword("user", "private-marker")}).String(), wantErr: true},
		{name: "padded key ID", path: keyPath, principal: "committee-relay@clients", audience: "lfx-v2-indexer-service", jwks: jwks.URL, keyID: " heimdall-key", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := NewAuditSigningSource(tc.path, tc.principal, tc.audience, tc.jwks, tc.keyID)
			if !tc.wantErr {
				require.NoError(t, err)
				require.NotNil(t, source)
				return
			}
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
			require.NotContains(t, err.Error(), "token file", "signing-key errors name the signing-key settings")
		})
	}
}

func TestAuditSigningSource_Authorization(t *testing.T) {
	const principal, audience = "committee-relay@clients", "lfx-v2-indexer-service"
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey)
	}{
		{name: "mints a token for exactly the relay (PKCS#1)", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			assertMintsRelayToken(t, jwks, keyPath, principal, audience)
		}},
		{name: "mints a token for exactly the relay (PKCS#8)", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			writeSigningKey(t, keyPath, key, true)
			assertMintsRelayToken(t, jwks, keyPath, principal, audience)
		}},
		{name: "reuses the token until a minute before expiry", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "")
			require.NoError(t, err)
			// The verifier uses real time, so every minted token must be valid now:
			// start 4m30s back (expiry 30s from now), then step forward to renewal.
			clock := time.Now().Add(-4*time.Minute - 30*time.Second)
			source.now = func() time.Time { return clock }
			first, err := source.Authorization(context.Background())
			require.NoError(t, err)
			clock = clock.Add(3 * time.Minute)
			again, err := source.Authorization(context.Background())
			require.NoError(t, err)
			require.Equal(t, first, again)
			clock = clock.Add(90 * time.Second)
			renewed, err := source.Authorization(context.Background())
			require.NoError(t, err)
			require.NotEqual(t, first, renewed)
		}},
		{name: "a wrong configured key ID fails verification", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "other-key")
			require.NoError(t, err)
			_, err = source.Authorization(context.Background())
			require.ErrorContains(t, err, "verification failed")
		}},
		{name: "a key Heimdall does not publish", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			writeSigningKey(t, keyPath, newSigningKey(t), false)
			source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "")
			require.NoError(t, err)
			_, err = source.Authorization(context.Background())
			require.ErrorContains(t, err, "not published")
		}},
		{name: "JWKS unavailable", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			jwks.mu.Lock()
			jwks.down = true
			jwks.mu.Unlock()
			source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "")
			require.NoError(t, err)
			_, err = source.Authorization(context.Background())
			require.ErrorContains(t, err, "unavailable")
		}},
		{name: "the key file removed after startup", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "")
			require.NoError(t, err)
			require.NoError(t, os.Remove(keyPath))
			_, err = source.Authorization(context.Background())
			require.ErrorContains(t, err, "unavailable")
		}},
		{name: "a key rotated in the mounted Secret is picked up without a restart", run: func(t *testing.T, jwks *signerJWKS, keyPath string, key *rsa.PrivateKey) {
			source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "")
			require.NoError(t, err)
			clock := time.Now().Add(-4*time.Minute - 30*time.Second)
			source.now = func() time.Time { return clock }
			before, err := source.Authorization(context.Background())
			require.NoError(t, err)
			_, _, kid := relayIdentity(t, jwks.URL, before)
			require.Equal(t, "heimdall-key", kid)

			// Heimdall publishes the new key alongside the old, then the Secret changes.
			rotated := newSigningKey(t)
			jwks.publish("heimdall-key-2", rotated)
			writeSigningKey(t, keyPath, rotated, false)
			clock = clock.Add(4*time.Minute + 30*time.Second)
			after, err := source.Authorization(context.Background())
			require.NoError(t, err)
			gotPrincipal, _, kid := relayIdentity(t, jwks.URL, after)
			require.Equal(t, "heimdall-key-2", kid)
			require.Equal(t, principal, gotPrincipal)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jwks := newSignerJWKS(t)
			key := newSigningKey(t)
			jwks.publish("other-key", newSigningKey(t))
			jwks.publish("heimdall-key", key)
			keyPath := filepath.Join(t.TempDir(), "key.pem")
			writeSigningKey(t, keyPath, key, false)
			tc.run(t, jwks, keyPath, key)
		})
	}
}

func assertMintsRelayToken(t *testing.T, jwks *signerJWKS, keyPath, principal, audience string) {
	t.Helper()
	source, err := NewAuditSigningSource(keyPath, principal, audience, jwks.URL, "")
	require.NoError(t, err)
	bearer, err := source.Authorization(context.Background())
	require.NoError(t, err)
	gotPrincipal, claims, kid := relayIdentity(t, jwks.URL, bearer)
	require.Equal(t, principal, gotPrincipal)
	require.Equal(t, "heimdall-key", kid, "the key ID is resolved by matching the public key")
	require.Equal(t, "heimdall", claims["iss"])
	require.Equal(t, principal, claims["sub"])
	require.Equal(t, principal, claims["principal"])
	require.Equal(t, audience, claims["aud"])
	require.InDelta(t, auditSignedTokenLifetime.Seconds(), claims["exp"].(float64)-claims["iat"].(float64), 1)
	require.NotContains(t, claims, "email", "a relay token carries no user identity")
	require.NotContains(t, claims, "act")
}

func TestAuditSigningSource_LogValue(t *testing.T) {
	jwks := newSignerJWKS(t)
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	key := newSigningKey(t)
	writeSigningKey(t, keyPath, key, false)
	source, err := NewAuditSigningSource(keyPath, "committee-relay@clients", "lfx-v2-indexer-service", jwks.URL, "")
	require.NoError(t, err)
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("relay", "source", source)
	printed := fmt.Sprintf("%v %+v %s", source, source, source)
	for _, out := range []string{logged.String(), printed} {
		require.Contains(t, out, "[audit signer]")
		require.NotContains(t, out, key.D.String()[:12], "the private exponent is never printed")
	}
}
