// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package postgres holds the PostgreSQL schema and connection plumbing for the committee service.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrationsFS holds the schema migrations, embedded so the binary carries exactly the schema it
// was tested against.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies every pending schema migration. It is safe to call from several processes at
// once (for example pods starting together): a session-level advisory lock lets one apply the
// migrations while the others wait, then find nothing left to do.
//
// Each migration runs in its own transaction, so a failed migration leaves the schema at the last
// version that completed. Migrations must stay additive (new tables, columns and indexes only) so
// the previous release keeps working while a new one migrates the shared database.
func Migrate(ctx context.Context, db *sql.DB) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	for _, result := range results {
		slog.InfoContext(ctx, "applied database migration",
			"version", result.Source.Version,
			"file", result.Source.Path,
			"duration", result.Duration,
		)
	}
	if err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}

// CheckMigrated returns an error unless every embedded migration has been applied, naming the ones that have
// not. It only reads: it never creates the version table or applies anything, so it can run against a
// database nobody has migrated. The tables come from one migration and the unique indexes that make a
// duplicate a conflict from a later one, each in its own transaction, so a database that stopped between
// them has the tables and none of the constraints.
func CheckMigrated(ctx context.Context, db *sql.DB) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, "SELECT version_id FROM goose_db_version WHERE is_applied")
	if err != nil {
		return fmt.Errorf("read the applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[int64]bool{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("read the applied migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read the applied migrations: %w", err)
	}

	var missing []int64
	for _, source := range provider.ListSources() {
		if !applied[source.Version] {
			missing = append(missing, source.Version)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("migrations %v have not been applied", missing)
	}
	return nil
}

// newProvider builds the goose provider over the embedded migrations.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
