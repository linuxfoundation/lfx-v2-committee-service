// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

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
	store := postgres.NewStore(pool)
	// Any answer but "not found" for a uid that cannot exist means the tables are not there.
	var notFound errs.NotFound
	if _, _, err := store.GetBase(ctx, "schema-check"); err != nil && !errors.As(err, &notFound) {
		pool.Close()
		return nil, nil, fmt.Errorf("the PostgreSQL schema is not ready (start the API once with POSTGRES_ENABLED=true to apply the migrations): %w", err)
	}
	return store, pool.Close, nil
}
