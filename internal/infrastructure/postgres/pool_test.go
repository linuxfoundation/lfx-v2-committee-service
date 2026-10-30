// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test credentials are assembled at run time so no connection string with an inline password
// is committed (secret scanners flag those, even for obvious placeholders).
const fakePassword = "not-a-real-secret"

func urlWithPassword(host string) string {
	return "postgres" + "://first-last:" + fakePassword + "@" + host + "/committee?sslmode=disable"
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"url the parser rejects", "postgres://first-last:" + fakePassword + "@db.example.com:port/committee", "postgres://first-last:xxxxx@db.example.com:port/committee"},
		{"url form", urlWithPassword("db.example.com:5432"), "postgres://first-last:xxxxx@db.example.com:5432/committee?sslmode=disable"},
		{"url without a password is unchanged", "postgres://first-last@db.example.com/committee", "postgres://first-last@db.example.com/committee"},
		{"password as a query parameter", "postgres://db.example.com/committee?password=" + fakePassword, "postgres://db.example.com/committee?password=xxxxx"},
		{"keyword form", "host=db.example.com user=first-last password=" + fakePassword + " dbname=committee", "host=db.example.com user=first-last password=xxxxx dbname=committee"},
		{"quoted keyword form", "host=db.example.com password='" + fakePassword + " with space' dbname=committee", "host=db.example.com password=xxxxx dbname=committee"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactURL(tt.dsn)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, fakePassword)
		})
	}
}

func TestNewPool(t *testing.T) {
	t.Run("requires a URL", func(t *testing.T) {
		_, err := NewPool(context.Background(), Config{URL: "  "})
		require.Error(t, err)
	})

	t.Run("a malformed URL error does not leak the password", func(t *testing.T) {
		// An invalid port makes the URL unparseable, which is the path where drivers echo the input.
		_, err := NewPool(context.Background(), Config{URL: "postgres://first-last:" + fakePassword + "@db.example.com:port/committee"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), fakePassword)
	})

	t.Run("an unreachable database error does not leak the password", func(t *testing.T) {
		// Port 1 refuses connections, so this exercises the connect failure path.
		_, err := NewPool(context.Background(), Config{URL: urlWithPassword("127.0.0.1:1")})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), fakePassword)
		assert.True(t, strings.HasPrefix(err.Error(), "connect to postgres:"), err.Error())
	})

	t.Run("connects, shares its connections and reports ready", func(t *testing.T) {
		pool, err := NewPool(context.Background(), Config{URL: testDSN(t), MaxConns: 2})
		require.NoError(t, err)
		defer pool.Close()
		assert.EqualValues(t, 2, pool.Config().MaxConns)

		require.NoError(t, NewReady(pool).IsReady(context.Background()))

		db := OpenDB(pool)
		require.NoError(t, db.PingContext(context.Background()))
		// Closing the migration handle must not close the shared pool.
		require.NoError(t, db.Close())
		require.NoError(t, NewReady(pool).IsReady(context.Background()))
	})

	t.Run("readiness fails once the pool is closed", func(t *testing.T) {
		pool, err := NewPool(context.Background(), Config{URL: testDSN(t)})
		require.NoError(t, err)
		assert.EqualValues(t, DefaultMaxConns, pool.Config().MaxConns)
		pool.Close()
		assert.Error(t, NewReady(pool).IsReady(context.Background()))
	})
}

// pgx hides the password in its own errors today; redactError is the guard for a driver or a
// wrapped error that echoes the connection string or the password anyway.
func TestRedactError(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{"url form", urlWithPassword("db.example.com:5432")},
		{"url the parser rejects", "postgres://first-last:" + fakePassword + "@db.example.com:port/committee"},
		{"percent-encoded password", "postgres://first-last:p%40ss-" + fakePassword + "@db.example.com/committee"},
		{"password query parameter", "postgres://db.example.com/committee?password=" + fakePassword},
		{"keyword form", "host=db.example.com user=first-last password=" + fakePassword},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets := passwords(tt.dsn)
			require.NotEmpty(t, secrets)

			echoed := errors.New("cannot use " + tt.dsn + " (password " + secrets[0] + " rejected)")
			got := redactError(tt.dsn, "connect to postgres", echoed)
			require.Error(t, got)
			for _, secret := range secrets {
				assert.NotContains(t, got.Error(), secret)
			}
			assert.True(t, strings.HasPrefix(got.Error(), "connect to postgres: "), got.Error())

			// An error that echoes only the raw password fragment is scrubbed too.
			fragment := errors.New("password " + secrets[0] + " rejected")
			assert.NotContains(t, redactError(tt.dsn, "x", fragment).Error(), secrets[0])
		})
	}
}
