// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/stores"
)

// postgresConnector opens the PostgreSQL store and returns what closes it.
type postgresConnector func(ctx context.Context) (store *postgres.Store, closeStore func(), err error)

// commandStores are the stores the commands that write committee data go through, for the storage mode. The commands that
// repair or inspect the KV data itself (the index backfillers, the exports and storage migrate, verify and flip) do not use
// them and keep the KV storage in every mode. The audit syncs repair records people see, so in the other modes they go
// through the composite and reach both stores.
type commandStores struct {
	committees port.CommitteeReaderWriter
	briefs     port.GroupWeeklyBriefReader
	// documentAudit is what the audit-sync commands repair links, folders and documents through.
	documentAudit port.DocumentAuditSyncStorage
	// postgres is what the storage subcommands connect to PostgreSQL with.
	postgres postgresConnector
	// close releases what was opened for the mode.
	close func()
}

// selectStores returns the stores for the mode. In kv mode they are the KV storage that was passed in, PostgreSQL is not
// connected until a storage subcommand asks for it, and the command is as it has always been. The other modes connect to
// PostgreSQL first, because a mode that was set on purpose must not run as another one, and write through the composite
// stores, so a committee a command changes is changed in both stores the mode names; the storage subcommands then share that
// connection.
func selectStores(ctx context.Context, mode composite.Mode, client *nats.NATSClient, kvCommittees port.CommitteeReaderWriter,
	kvBriefs port.GroupWeeklyBriefReader, kvAudit port.DocumentAuditSyncStorage, connect postgresConnector) (*commandStores, error) {
	if mode == composite.ModeKV {
		return &commandStores{committees: kvCommittees, briefs: kvBriefs, documentAudit: kvAudit, postgres: connect, close: func() {}}, nil
	}
	opts, err := composite.OptionsFromEnv(mode, os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("invalid storage mode settings: %w", err)
	}
	pg, closePostgres, err := connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s=%s needs PostgreSQL: %w", composite.EnvStorageMode, mode, err)
	}
	built, err := stores.Build(mode, client, pg, append(opts, composite.WithMetrics(composite.NewMetrics()))...)
	if err != nil {
		closePostgres()
		return nil, fmt.Errorf("building the stores for %s=%s: %w", composite.EnvStorageMode, mode, err)
	}
	return &commandStores{
		committees:    built.Committees,
		briefs:        built.Briefs,
		documentAudit: built.DocumentAudit,
		postgres:      func(context.Context) (*postgres.Store, func(), error) { return pg, func() {}, nil },
		close:         closePostgres,
	}, nil
}
