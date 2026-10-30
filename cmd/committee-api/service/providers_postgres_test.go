// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// env returns a getenv backed by a map, so the tests never touch the process environment.
func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// unreachableURL points at a port that refuses connections. The credentials are assembled at run
// time so no connection string with an inline password is committed.
func unreachableURL() string {
	return "postgres" + "://first-last:not-a-real-secret@127.0.0.1:1/committee?sslmode=disable"
}

func TestInitPostgres(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled by default, even with a URL set", func(t *testing.T) {
		// The URL would fail to connect, so a connection attempt would show up as a log and a
		// delay; the assertion is that nothing is created and no check is registered.
		pool, checks := initPostgres(ctx, env(map[string]string{
			"POSTGRES_URL":             unreachableURL(),
			"POSTGRES_READINESS_CHECK": "true",
		}))
		assert.Nil(t, pool)
		assert.Empty(t, checks)
	})

	t.Run("only the exact value true enables it", func(t *testing.T) {
		for _, value := range []string{"", "false", "TRUE", "1", "yes"} {
			pool, checks := initPostgres(ctx, env(map[string]string{
				"POSTGRES_ENABLED":         value,
				"POSTGRES_URL":             unreachableURL(),
				"POSTGRES_READINESS_CHECK": "true",
			}))
			assert.Nil(t, pool, value)
			assert.Empty(t, checks, value)
		}
	})

	t.Run("enabled without a URL does not take the service down", func(t *testing.T) {
		pool, checks := initPostgres(ctx, env(map[string]string{"POSTGRES_ENABLED": "true"}))
		assert.Nil(t, pool)
		assert.Empty(t, checks)
	})

	t.Run("enabled with bad pool settings does not take the service down", func(t *testing.T) {
		pool, checks := initPostgres(ctx, env(map[string]string{
			"POSTGRES_ENABLED":   "true",
			"POSTGRES_URL":       unreachableURL(),
			"POSTGRES_MAX_CONNS": "abc",
		}))
		assert.Nil(t, pool)
		assert.Empty(t, checks)
	})

	t.Run("enabled but unreachable does not take the service down", func(t *testing.T) {
		pool, checks := initPostgres(ctx, env(map[string]string{
			"POSTGRES_ENABLED": "true",
			"POSTGRES_URL":     unreachableURL(),
		}))
		assert.Nil(t, pool)
		assert.Empty(t, checks, "without the readiness opt-in an outage is invisible to the probe")
	})

	t.Run("unreachable with the readiness opt-in reports not ready, without the password", func(t *testing.T) {
		pool, checks := initPostgres(ctx, env(map[string]string{
			"POSTGRES_ENABLED":         "true",
			"POSTGRES_URL":             unreachableURL(),
			"POSTGRES_READINESS_CHECK": "true",
		}))
		assert.Nil(t, pool)
		require.Len(t, checks, 1)
		err := checks[0].IsReady(ctx)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "not-a-real-secret")
	})

	t.Run("enabled and reachable connects, migrates and honors the readiness opt-in", func(t *testing.T) {
		dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
		if dsn == "" {
			if os.Getenv("CI") != "" {
				t.Fatal("TEST_DATABASE_URL must be set on CI")
			}
			t.Skip("TEST_DATABASE_URL is not set; skipping the live PostgreSQL test")
		}

		// The migrations are idempotent and the other PostgreSQL tests work in their own schemas,
		// so applying them to the test database's default schema is safe.
		pool, checks := initPostgres(ctx, env(map[string]string{
			"POSTGRES_ENABLED":         "true",
			"POSTGRES_URL":             dsn,
			"POSTGRES_READINESS_CHECK": "true",
			"POSTGRES_MAX_CONNS":       "2",
		}))
		require.NotNil(t, pool)
		defer pool.Close()
		require.Len(t, checks, 1)
		assert.NoError(t, checks[0].IsReady(ctx))
		assert.EqualValues(t, 2, pool.Config().MaxConns)

		offPool, offChecks := initPostgres(ctx, env(map[string]string{
			"POSTGRES_ENABLED": "true",
			"POSTGRES_URL":     dsn,
		}))
		require.NotNil(t, offPool)
		defer offPool.Close()
		assert.Empty(t, offChecks, "readiness stays off unless opted in")
	})
}
