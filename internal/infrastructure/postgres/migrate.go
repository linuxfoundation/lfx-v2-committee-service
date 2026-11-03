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
