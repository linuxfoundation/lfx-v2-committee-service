// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	"github.com/stretchr/testify/require"
)

func TestStartCommitteeAuditRelay(t *testing.T) {
	oldPool, oldNATS := postgresPool, natsClient
	postgresPool, natsClient = &pgxpool.Pool{}, &nats.NATSClient{}
	t.Cleanup(func() { postgresPool, natsClient = oldPool, oldNATS })
	t.Setenv("COMMITTEE_AUDIT_ENABLED", "true")
	t.Setenv("COMMITTEE_AUDIT_DISPATCH_ENABLED", "true")
	t.Setenv("COMMITTEE_AUDIT_OUTBOX_RETENTION", "24h")
	t.Setenv("STORAGE_MODE", "postgres")
	t.Setenv("AUTH_SOURCE", "")
	t.Setenv("JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL", "")
	t.Setenv("REPOSITORY_SOURCE", "")
	t.Setenv("MESSAGING_SOURCE", "")
	t.Setenv("COMMITTEE_AUDIT_PRINCIPAL", "test-relay@clients")
	t.Setenv("COMMITTEE_AUDIT_AUDIENCE", "indexer-service")
	t.Setenv("JWKS_URL", "http://127.0.0.1:1/.well-known/jwks")
	path := filepath.Join(t.TempDir(), "credential")
	t.Setenv("COMMITTEE_AUDIT_TOKEN_FILE", path)
	t.Setenv("COMMITTEE_AUDIT_SIGNER_KEY_FILE", "")
	t.Setenv("COMMITTEE_AUDIT_SIGNER_KEY_ID", "")
	// Canceled lifecycle prevents workers using these sentinel infrastructure
	// pointers, while still exercising all synchronous startup validation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct{ name, contents string }{
		{"missing credential and unreachable JWKS", ""}, {"unusable credential and unreachable JWKS", "invalid-short-lived-credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.contents != "" {
				require.NoError(t, os.WriteFile(path, []byte(tc.contents), 0600))
			}
			var wg sync.WaitGroup
			require.NoError(t, StartCommitteeAuditRelay(ctx, &wg), "runtime credential availability must not gate API startup")
			wg.Wait()
		})
	}
	t.Run("signer mode", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		keyPath := filepath.Join(t.TempDir(), "key.pem")
		require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
		t.Setenv("COMMITTEE_AUDIT_TOKEN_FILE", "")
		t.Setenv("COMMITTEE_AUDIT_SIGNER_KEY_FILE", keyPath)
		var wg sync.WaitGroup
		require.NoError(t, StartCommitteeAuditRelay(ctx, &wg), "the key ID is resolved at delivery; JWKS is not needed to start")
		wg.Wait()

		for name, env := range map[string][2]string{
			"both credential sources": {"COMMITTEE_AUDIT_TOKEN_FILE", path},
			"missing signing key":     {"COMMITTEE_AUDIT_SIGNER_KEY_FILE", filepath.Join(t.TempDir(), "missing.pem")},
			"invalid signing key":     {"COMMITTEE_AUDIT_SIGNER_KEY_FILE", path},
			"user principal":          {"COMMITTEE_AUDIT_PRINCIPAL", "first-last"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Setenv(env[0], env[1])
				if name == "invalid signing key" {
					require.NoError(t, os.WriteFile(path, []byte("not-a-key"), 0600))
				}
				var wg sync.WaitGroup
				require.Error(t, StartCommitteeAuditRelay(ctx, &wg))
				wg.Wait()
			})
		}
	})
	t.Run("dispatch drains retained events with capture disabled", func(t *testing.T) {
		t.Setenv("COMMITTEE_AUDIT_ENABLED", "false")
		var wg sync.WaitGroup
		require.NoError(t, StartCommitteeAuditRelay(ctx, &wg))
		wg.Wait()
	})
	for _, tc := range []struct{ name, key, value string }{
		{"missing path", "COMMITTEE_AUDIT_TOKEN_FILE", ""},
		{"user principal", "COMMITTEE_AUDIT_PRINCIPAL", "first-last"},
		{"empty client", "COMMITTEE_AUDIT_PRINCIPAL", "@clients"},
		{"missing audience", "COMMITTEE_AUDIT_AUDIENCE", ""},
		{"invalid JWKS URL", "JWKS_URL", "not-a-jwks-url"},
		{"invalid dispatch flag", "COMMITTEE_AUDIT_DISPATCH_ENABLED", "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			var wg sync.WaitGroup
			require.Error(t, StartCommitteeAuditRelay(ctx, &wg), "invalid static configuration must still fail startup")
			wg.Wait()
		})
	}
}
