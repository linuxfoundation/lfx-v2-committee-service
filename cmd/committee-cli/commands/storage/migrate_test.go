// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
)

// migrateSource is a KV store holding one committee with settings, a member of it and an orphan.
func migrateSource() *fakeSource {
	return &fakeSource{
		committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 10, 11)},
		members: []memberRecord{
			kvMember("m-1", "c-1", "first.last@example.com", 30),
			kvMember("m-orphan", "c-deleted", "first.last2@example.com", 31),
		},
	}
}

func TestMigrate_Run(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	closed := false
	rc := func(args ...string) commands.RunContext {
		return commands.RunContext{
			KVExport:      migrateSource(),
			PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() { closed = true }, nil },
			Args:          args,
		}
	}

	t.Run("a dry run reports what it would write and writes nothing", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&migrateSubcommand{out: &out}).Run(ctx, rc("--dry-run")))

		assert.Contains(t, out.String(), "dry run: nothing was written")
		assert.Contains(t, out.String(), "committees scanned=1 written=1")
		uids, err := store.ListAllUIDs(ctx)
		require.NoError(t, err)
		assert.Empty(t, uids)
		assert.True(t, closed, "the connection is released")
	})

	t.Run("a run copies, keeps the revisions and reports the orphan with its uid and the highest revision", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&migrateSubcommand{out: &out}).Run(ctx, rc()))

		for _, want := range []string{
			"committees scanned=1 written=1 unchanged=0 orphans=0 errors=0",
			"settings   scanned=1 written=1",
			"members    scanned=2 written=1 unchanged=0 orphans=1 errors=0",
			"members orphan uid=m-orphan",
			"highest KV revision seen: 31",
		} {
			assert.Contains(t, out.String(), want)
		}
		_, revision, err := store.GetBase(ctx, "c-1")
		require.NoError(t, err)
		assert.EqualValues(t, 10, revision)
	})

	t.Run("a second run changes nothing", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&migrateSubcommand{out: &out}).Run(ctx, rc()))
		assert.Contains(t, out.String(), "committees scanned=1 written=0 unchanged=1")
		assert.Contains(t, out.String(), "members    scanned=2 written=0 unchanged=1 orphans=1")
	})

	t.Run("a bad flag or entity is an error and nothing is connected", func(t *testing.T) {
		connected := false
		bad := commands.RunContext{KVExport: migrateSource(), PostgresStore: func(context.Context) (*postgres.Store, func(), error) {
			connected = true
			return store, func() {}, nil
		}}
		for _, args := range [][]string{{"--workers=0"}, {"--batch-size=-1"}, {"--no-such-flag"}} {
			bad.Args = args
			require.Error(t, (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, bad), args)
		}
		bad.Args = []string{"--entity=widgets"}
		require.ErrorContains(t, (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, bad), `unknown entity "widgets"`)
		assert.False(t, connected, "a typo is found before anything connects")
	})

	t.Run("a stray argument is an error and does not start a live run", func(t *testing.T) {
		connected := false
		stray := commands.RunContext{KVExport: migrateSource(), Args: []string{"members", "--dry-run"}, PostgresStore: func(context.Context) (*postgres.Store, func(), error) {
			connected = true
			return store, func() {}, nil
		}}

		err := (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, stray)

		require.ErrorContains(t, err, `unexpected argument "members"`)
		assert.False(t, connected)
	})

	t.Run("entities may be separated by a comma and spaces", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&migrateSubcommand{out: &out}).Run(ctx, rc("--dry-run", "--entity=committees, members")))
		assert.Contains(t, out.String(), "committees scanned=1")
		assert.Contains(t, out.String(), "members    scanned=2")
	})

	t.Run("without the KV export or the store it says so", func(t *testing.T) {
		err := (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, commands.RunContext{})
		require.ErrorContains(t, err, "not wired")
	})

	t.Run("help is not an error", func(t *testing.T) {
		require.NoError(t, (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, commands.RunContext{Args: []string{"--help"}}))
	})
}
