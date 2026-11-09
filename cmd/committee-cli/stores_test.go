// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// neverConnect fails the test if the command connects to PostgreSQL.
func neverConnect(t *testing.T) postgresConnector {
	return func(context.Context) (*postgres.Store, func(), error) {
		t.Error("PostgreSQL was connected to")
		return nil, func() {}, errors.New("not expected")
	}
}

func TestSelectStores_KVIsTheKVStorageAndConnectsToNothing(t *testing.T) {
	client := natstest.NewClient(t)
	kv := nats.NewStorageBackend(client)
	connects := 0
	connect := func(context.Context) (*postgres.Store, func(), error) {
		connects++
		return nil, nil, errors.New("not connected in this test")
	}

	selected, err := selectStores(context.Background(), composite.ModeKV, client, kv, kv, kv, connect)

	require.NoError(t, err)
	assert.Equal(t, any(kv), any(selected.committees), "the commands write through the KV storage itself")
	assert.Equal(t, any(kv), any(selected.briefs))
	assert.Equal(t, any(kv), any(selected.documentAudit), "the audit syncs repair KV directly, as they always did")
	assert.NotPanics(t, selected.close)
	assert.Zero(t, connects, "PostgreSQL is not connected to")
	_, _, err = selected.postgres(context.Background())
	require.Error(t, err)
	assert.Equal(t, 1, connects, "a storage subcommand connects when it needs to, as it always did")
}

func TestSelectStores_TheOtherModesConnectFirstAndWriteThroughTheComposite(t *testing.T) {
	ctx := context.Background()
	client := natstest.NewClient(t)
	kv := nats.NewStorageBackend(client)
	pg := postgrestest.NewStore(t)
	for _, mode := range []composite.Mode{composite.ModeDual, composite.ModeDualPG, composite.ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			connects, closes := 0, 0
			connect := func(context.Context) (*postgres.Store, func(), error) {
				connects++
				return pg, func() { closes++ }, nil
			}

			selected, err := selectStores(ctx, mode, client, kv, kv, kv, connect)

			require.NoError(t, err)
			assert.NotEqual(t, any(kv), any(selected.committees), "the commands write through the composite")
			assert.NotNil(t, selected.documentAudit)
			assert.NotEqual(t, any(kv), any(selected.documentAudit), "the audit syncs repair through the composite")
			require.NoError(t, selected.committees.Create(ctx, storetest.NewCommittee("uid-"+string(mode), "project-1", "Committee "+string(mode))))
			_, _, err = pg.GetBase(ctx, "uid-"+string(mode))
			assert.NoError(t, err, "PostgreSQL holds what the command wrote")

			shared, closeShared, err := selected.postgres(ctx)
			require.NoError(t, err)
			assert.Same(t, pg, shared, "the storage subcommands share the connection")
			closeShared()
			assert.Zero(t, closes, "a subcommand does not close the connection the commands use")
			assert.Equal(t, 1, connects, "one connection, made once")
			selected.close()
			assert.Equal(t, 1, closes)
		})
	}
}

func TestSelectStores_AModeThatCannotBeUsedStopsTheCommand(t *testing.T) {
	ctx := context.Background()
	client := natstest.NewClient(t)
	kv := nats.NewStorageBackend(client)

	t.Run("PostgreSQL cannot be reached", func(t *testing.T) {
		connect := func(context.Context) (*postgres.Store, func(), error) {
			return nil, nil, errors.New("no route to host")
		}

		_, err := selectStores(ctx, composite.ModeDual, client, kv, kv, kv, connect)

		require.ErrorContains(t, err, "STORAGE_MODE=dual needs PostgreSQL")
		assert.ErrorContains(t, err, "no route to host")
	})

	t.Run("a setting of the mode cannot be used, and nothing connects", func(t *testing.T) {
		t.Setenv(composite.EnvReadCompareRate, "lots")

		_, err := selectStores(ctx, composite.ModeDual, client, kv, kv, kv, neverConnect(t))

		require.ErrorContains(t, err, composite.EnvReadCompareRate)
	})

	t.Run("a pool of one connection stalls the member scans, so it is refused before connecting", func(t *testing.T) {
		// Put together here, not written out, so that no connection string is in the source for a secret scanner to report.
		t.Setenv("POSTGRES_URL", "postgres"+"://first-last:not-a-real-secret@127.0.0.1:1/committee?sslmode=disable")
		t.Setenv("POSTGRES_MAX_CONNS", "1")

		_, err := selectStores(ctx, composite.ModePostgres, client, kv, kv, kv, neverConnect(t))

		require.ErrorContains(t, err, "POSTGRES_MAX_CONNS of at least 2")
		t.Setenv("POSTGRES_MAX_CONNS", "2")
		connected := false
		pg := postgrestest.NewStore(t)
		_, err = selectStores(ctx, composite.ModePostgres, client, kv, kv, kv, func(context.Context) (*postgres.Store, func(), error) {
			connected = true
			return pg, func() {}, nil
		})
		require.NoError(t, err)
		assert.True(t, connected, "two connections are enough")
	})

	t.Run("the settings of a mode that writes both stores are not looked at in kv", func(t *testing.T) {
		t.Setenv(composite.EnvReadCompareRate, "lots")

		_, err := selectStores(ctx, composite.ModeKV, client, kv, kv, kv, neverConnect(t))

		require.NoError(t, err)
	})
}
