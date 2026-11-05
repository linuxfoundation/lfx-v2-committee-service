// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// schemaCheckTimeout bounds the query that checks the migrations have been applied.
const schemaCheckTimeout = 10 * time.Second

// connectPostgres opens the pool the storage subcommands write through. It does not migrate the schema:
// the API applies the migrations when it starts with POSTGRES_ENABLED=true, and a CLI run against a
// database nobody has migrated fails here, with a message that says so, instead of part way through.
func connectPostgres(ctx context.Context) (*postgres.Store, func(), error) {
	cfg, err := postgres.ConfigFromEnv(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	pool, err := postgres.NewPool(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	// Every migration has to be applied, not just the tables: the unique indexes come from a later one, and
	// without them the backfill would import duplicates silently. The pool's timeout covers connecting, not
	// this query, so a database that accepts connections and then stalls must not hang the command.
	checkCtx, cancel := context.WithTimeout(ctx, schemaCheckTimeout)
	defer cancel()
	db := postgres.OpenDB(pool)
	defer func() { _ = db.Close() }()
	if err := postgres.CheckMigrated(checkCtx, db); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("the PostgreSQL schema is not ready (start the API once with POSTGRES_ENABLED=true to apply the migrations): %w", err)
	}
	store := postgres.NewStore(pool)
	return store, pool.Close, nil
}
