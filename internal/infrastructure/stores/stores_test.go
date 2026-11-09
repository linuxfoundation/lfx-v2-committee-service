// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/stores"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// In kv mode nothing is composed: the stores are the ones the service has always used, and PostgreSQL is not needed, and not
// touched if it is given.
func TestBuild_KVIsTheKVStoragesAndNothingElse(t *testing.T) {
	client := natstest.NewClient(t)
	for name, pgGiven := range map[string]bool{"without PostgreSQL": false, "with PostgreSQL": true} {
		t.Run(name, func(t *testing.T) {
			var pg *postgres.Store
			if pgGiven {
				pg = postgrestest.NewStore(t)
			}

			built, err := stores.Build(composite.ModeKV, client, pg)

			require.NoError(t, err)
			assert.IsType(t, nats.NewStorage(client), built.Committees, "the committees are the KV storage")
			assert.Equal(t, any(built.Committees), any(built.Links), "the links and the briefs are served by the same KV storage")
			assert.Equal(t, any(built.Committees), any(built.Briefs))
			assert.IsType(t, nats.NewDocumentStorage(client), built.Documents)
		})
	}
}

func TestBuild_TheOtherModesAreComposites(t *testing.T) {
	client := natstest.NewClient(t)
	pg := postgrestest.NewStore(t)
	for mode, cascading := range map[composite.Mode]bool{
		composite.ModeDual: false, composite.ModeDualPG: true, composite.ModePostgres: true,
	} {
		t.Run(string(mode), func(t *testing.T) {
			built, err := stores.Build(mode, client, pg)

			require.NoError(t, err)
			if cascading {
				assert.IsType(t, &composite.CascadingStore{}, built.Committees, "a committee delete removes what belongs to it")
			} else {
				assert.IsType(t, &composite.Store{}, built.Committees, "kv decides, and the delete is the one the service has always had")
			}
			assert.IsType(t, &composite.Links{}, built.Links)
			assert.IsType(t, &composite.Briefs{}, built.Briefs)
			assert.IsType(t, &composite.Documents{}, built.Documents)
		})
	}
}

func TestBuild_RefusesWhatItCannotBuild(t *testing.T) {
	client := natstest.NewClient(t)

	_, err := stores.Build(composite.ModeDual, client, nil)
	require.ErrorContains(t, err, "needs the PostgreSQL store")

	_, err = stores.Build(composite.ModePostgres, client, nil)
	require.ErrorContains(t, err, "needs the PostgreSQL store")

	_, err = stores.Build(composite.ModeKV, nil, nil)
	require.ErrorContains(t, err, "NATS client")

	_, err = stores.Build(composite.Mode("dual-write"), client, postgrestest.NewStore(t))
	require.Error(t, err)
}

// What is built for dual mode writes both stores, the committees and the records that belong to them.
func TestBuild_DualWritesBothStores(t *testing.T) {
	ctx := context.Background()
	client := natstest.NewClient(t)
	pg := postgrestest.NewStore(t)
	built, err := stores.Build(composite.ModeDual, client, pg)
	require.NoError(t, err)

	require.NoError(t, built.Committees.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	require.NoError(t, built.Links.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
	require.NoError(t, built.Documents.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))

	kv := nats.NewStorageBackend(client)
	for name, read := range map[string]func() error{
		"committee in KV":       func() error { _, _, err := kv.GetBase(ctx, "uid-1"); return err },
		"committee in PG":       func() error { _, _, err := pg.GetBase(ctx, "uid-1"); return err },
		"link in KV":            func() error { _, _, err := kv.GetLink(ctx, "uid-1", "l-1"); return err },
		"link in PG":            func() error { _, _, err := pg.GetLink(ctx, "uid-1", "l-1"); return err },
		"document in KV":        func() error { _, _, err := built.Documents.GetDocumentMetadata(ctx, "uid-1", "d-1"); return err },
		"document metadata, PG": func() error { _, _, err := pg.GetDocumentMetadata(ctx, "uid-1", "d-1"); return err },
	} {
		assert.NoError(t, read(), name)
	}
}

// In postgres mode the KV stores are not written for the committees, but the files of the documents are in the Object Store.
func TestBuild_PostgresDoesNotWriteKV(t *testing.T) {
	ctx := context.Background()
	client := natstest.NewClient(t)
	pg := postgrestest.NewStore(t)
	built, err := stores.Build(composite.ModePostgres, client, pg)
	require.NoError(t, err)

	require.NoError(t, built.Committees.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	_, _, err = pg.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	_, _, err = nats.NewStorageBackend(client).GetBase(ctx, "uid-1")
	assert.Error(t, err, "KV was not written")
}
