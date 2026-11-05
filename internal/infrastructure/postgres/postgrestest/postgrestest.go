// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package postgrestest gives the tests of other packages a PostgreSQL store on a schema of its own.
// It is imported only from _test.go files, so it never links into the binaries. The tests of the
// postgres package itself use their own helpers, which this one mirrors: that package cannot import it.
package postgrestest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// EnvDatabaseURL names the variable that points the tests at a PostgreSQL server.
const EnvDatabaseURL = "TEST_DATABASE_URL"

// NewStore returns a store on a freshly migrated schema that is dropped when the test ends. The test is
// skipped when EnvDatabaseURL is not set, except on CI, where a missing database fails it: a harness that
// silently skips everywhere proves nothing.
func NewStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(EnvDatabaseURL))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s must be set on CI so the code is tested against a real PostgreSQL", EnvDatabaseURL)
		}
		t.Skipf("%s is not set; skipping the live PostgreSQL tests", EnvDatabaseURL)
	}

	suffix := make([]byte, 6)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	schema := "t_" + hex.EncodeToString(suffix)

	ctx := context.Background()
	admin := openDB(t, dsn, "public")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	require.NoError(t, postgres.Migrate(ctx, openDB(t, dsn, schema)))

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return postgres.NewStore(pool)
}

func openDB(t *testing.T, dsn, schema string) *sql.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
