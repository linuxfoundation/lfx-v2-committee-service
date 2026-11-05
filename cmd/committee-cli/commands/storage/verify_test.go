// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

func TestVerify_Run(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	closed := false
	rc := func(src *fakeSource, args ...string) commands.RunContext {
		return commands.RunContext{
			KVExport:      src,
			PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() { closed = true }, nil },
			Args:          args,
		}
	}

	t.Run("after a migrate it reports no differences and exits cleanly", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&verifySubcommand{out: &out}).Run(ctx, rc(src)))

		for _, want := range []string{
			"committees   kv=2 postgresql=2 matched=2 missing=0 extra=0 mismatched=0 errors=0 orphans(kv)=0 orphans(postgresql)=0",
			"members      kv=3 postgresql=2 matched=2 missing=0 extra=0 mismatched=0 errors=0 orphans(kv)=1",
			"  members kv-orphan uid=m-orphan: its committee does not exist in KV",
			"no differences between KV and PostgreSQL",
		} {
			assert.Contains(t, out.String(), want)
		}
		assert.True(t, closed, "the connection is released")
	})

	t.Run("a difference is listed with its uid and the command fails", func(t *testing.T) {
		original := src.invites[0].record.Role
		src.invites[0].record.Role = "changed"
		defer func() { src.invites[0].record.Role = original }()
		var out bytes.Buffer

		err := (&verifySubcommand{out: &out}).Run(ctx, rc(src))

		require.ErrorContains(t, err, "KV and PostgreSQL differ: 1 differences")
		assert.Contains(t, out.String(), "invites      kv=2 postgresql=1 matched=0 missing=0 extra=0 mismatched=1")
		assert.Contains(t, out.String(), "  invites mismatch uid=i-1: content differs")
		assert.NotContains(t, out.String(), "no differences")
	})

	t.Run("--include-orphans expects the orphans to have been copied", func(t *testing.T) {
		var out bytes.Buffer

		err := (&verifySubcommand{out: &out}).Run(ctx, rc(src, "--include-orphans"))

		require.Error(t, err)
		assert.Contains(t, out.String(), "  members missing uid=m-orphan: in KV and not in PostgreSQL")
	})

	t.Run("entities may be separated by a comma and spaces", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&verifySubcommand{out: &out}).Run(ctx, rc(src, "--entity=members, links")))
		assert.Contains(t, out.String(), "members      kv=3")
		assert.Contains(t, out.String(), "links        kv=2")
		assert.NotContains(t, out.String(), "invites")
	})

	t.Run("a bad flag or entity is an error and nothing is connected", func(t *testing.T) {
		connected := false
		bad := commands.RunContext{KVExport: src, PostgresStore: func(context.Context) (*postgres.Store, func(), error) {
			connected = true
			return store, func() {}, nil
		}}
		bad.Args = []string{"--no-such-flag"}
		require.Error(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, bad))
		bad.Args = []string{"--entity=widgets"}
		require.ErrorContains(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, bad), `unknown entity "widgets"`)
		bad.Args = []string{"members"}
		require.ErrorContains(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, bad), `unexpected argument "members"`)
		assert.False(t, connected, "a typo is found before anything connects")
	})

	t.Run("without the KV export or the store it says so", func(t *testing.T) {
		require.ErrorContains(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, commands.RunContext{}), "not wired")
	})

	t.Run("help is not an error", func(t *testing.T) {
		require.NoError(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, commands.RunContext{Args: []string{"--help"}}))
	})
}

func TestVerify_ListsAtMostMaxPrintedProblems(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	for range maxPrintedProblems + 5 {
		src.invites = append(src.invites, rec(src.invites[0].record, 999))
	}
	var out bytes.Buffer
	rc := commands.RunContext{KVExport: src, PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() {}, nil }}

	require.Error(t, (&verifySubcommand{out: &out}).Run(ctx, rc))

	assert.Contains(t, out.String(), "... more are in the log")
	assert.Equal(t, 1, bytes.Count(out.Bytes(), []byte("... more are in the log")))
}

func TestVerify_ListsTheDifferencesBeforeTheOrphans(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	for i := range maxPrintedProblems + 20 {
		src.members = append(src.members, kvMember(fmt.Sprintf("m-orphan-%d", i), "c-deleted", "first.last@example.com", 600))
	}
	src.throttles[0].record.GeneratesUsed++ // an entity after the members, whose difference must not be pushed out
	var out bytes.Buffer
	rc := commands.RunContext{KVExport: src, PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() {}, nil }}

	require.Error(t, (&verifySubcommand{out: &out}).Run(ctx, rc))

	assert.Contains(t, out.String(), "  throttles mismatch uid=c-1.20260104: content differs")
	assert.Contains(t, out.String(), "... more are in the log", "the orphans fill what is left of the lines")
	assert.Less(t, bytes.Index(out.Bytes(), []byte("throttles mismatch")), bytes.Index(out.Bytes(), []byte("kv-orphan")))
}

func TestVerify_BothSortsOfOrphanAreListedAfterTheDifferences(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{IncludeOrphans: true}) // the orphans are in both stores
	src.members = nil                                                      // every member PostgreSQL has is now extra, and the orphan among them is still an orphan
	var out bytes.Buffer
	rc := commands.RunContext{KVExport: src, PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() {}, nil }}

	require.Error(t, (&verifySubcommand{out: &out}).Run(ctx, rc))

	assert.Less(t, bytes.Index(out.Bytes(), []byte("members extra")), bytes.Index(out.Bytes(), []byte("pg-orphan")), "a PostgreSQL orphan is an orphan: it comes after the differences")
}

func TestIsOrphan(t *testing.T) {
	assert.True(t, isOrphan(verifyKVOrphan))
	assert.True(t, isOrphan(verifyPGOrphan))
	for _, kind := range []string{verifyMissing, verifyExtra, verifyMismatch, verifyScan} {
		assert.False(t, isOrphan(kind), kind)
	}
}

func TestStorageCommand_HasMigrateAndVerify(t *testing.T) {
	subcommands := NewCommand().Subcommands()
	assert.Contains(t, subcommands, "migrate")
	assert.Contains(t, subcommands, "verify")
}
