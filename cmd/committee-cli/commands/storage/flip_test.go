// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// flipRevisionOf is the revision PostgreSQL gives a committee created now: one from the sequence.
func flipRevisionOf(ctx context.Context, t *testing.T, store *postgres.Store, uid string) uint64 {
	t.Helper()
	require.NoError(t, store.Create(ctx, storetest.NewCommittee(uid, "project-new", "New "+uid)))
	_, revision, err := store.GetBase(ctx, uid)
	require.NoError(t, err)
	return revision
}

// highRevisions is where each entity of the KV store can hold the highest revision. Every one is copied to PostgreSQL
// with its revision, so the stores agree, except the orphan, which migrate leaves in KV.
var highRevisions = map[string]func(src *fakeSource, revision uint64){
	"committee base":        func(s *fakeSource, r uint64) { s.committees[0].BaseRevision = r },
	"committee settings":    func(s *fakeSource, r uint64) { s.committees[0].SettingsRevision = r },
	"member":                func(s *fakeSource, r uint64) { s.members[0].revision = r },
	"invite":                func(s *fakeSource, r uint64) { s.invites[0].revision = r },
	"application":           func(s *fakeSource, r uint64) { s.applications[0].revision = r },
	"link":                  func(s *fakeSource, r uint64) { s.links[0].revision = r },
	"folder":                func(s *fakeSource, r uint64) { s.folders[0].revision = r },
	"document":              func(s *fakeSource, r uint64) { s.documents[0].revision = r },
	"brief":                 func(s *fakeSource, r uint64) { s.briefs[0].revision, s.briefs[0].record.Revision = r, r },
	"throttle":              func(s *fakeSource, r uint64) { s.throttles[0].revision, s.throttles[0].record.Revision = r, r },
	"orphan member (in KV)": func(s *fakeSource, r uint64) { s.members[2].revision = r },
}

func TestRunFlip_TheSequenceIsAdvancedAboveTheHighestRevisionOfEveryEntity(t *testing.T) {
	ctx := context.Background()
	i := 0
	for name, set := range highRevisions {
		i++
		revision := uint64(5000 + i)
		t.Run(name, func(t *testing.T) {
			src := childSource()
			set(src, revision)
			store := postgrestest.NewStore(t)
			_, err := runBackfill(ctx, src, store, backfillOptions{})
			require.NoError(t, err)
			var out bytes.Buffer

			require.NoError(t, runFlip(ctx, src, store, true, &out))

			assert.Greater(t, flipRevisionOf(ctx, t, store, "c-new"), revision, "a revision issued after the flip is above every revision KV has")
			assert.Contains(t, out.String(), "is now above")
		})
	}
}

func TestRunFlip_WithoutConfirmNothingIsChanged(t *testing.T) {
	ctx := context.Background()
	src := childSource()
	src.throttles[0].revision, src.throttles[0].record.Revision = 7000, 7000
	store := postgrestest.NewStore(t)
	_, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)
	var out bytes.Buffer

	require.NoError(t, runFlip(ctx, src, store, false, &out))

	assert.Less(t, flipRevisionOf(ctx, t, store, "c-new"), uint64(7000), "the sequence was not advanced")
	assert.Contains(t, out.String(), "the highest revision in KV is 7000")
	assert.Contains(t, out.String(), "dry run: nothing changed")
}

func TestRunFlip_RefusesWhileTheStoresDiffer(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(src *fakeSource){
		"another content":  func(s *fakeSource) { s.invites[0].record.Role = "changed" },
		"another revision": func(s *fakeSource) { s.members[0].revision += 1000 },
		"a record only KV has": func(s *fakeSource) {
			s.members = append(s.members, kvMember("m-new", "c-1", "first.last9@example.com", 39))
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, store := migratedSource(t, backfillOptions{})
			change(src)
			var out bytes.Buffer

			err := runFlip(ctx, src, store, true, &out)

			require.ErrorContains(t, err, "refusing to flip")
			assert.Less(t, flipRevisionOf(ctx, t, store, "c-new"), uint64(1000), "the sequence was not advanced")
			assert.NotContains(t, out.String(), "is now above")
		})
	}
}

func TestRunFlip_RefusesWhenAScanOfKVDoesNotComplete(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	src.memberErr = errors.New("a record could not be read")

	err := runFlip(ctx, src, store, true, &bytes.Buffer{})

	require.Error(t, err)
	assert.Less(t, flipRevisionOf(ctx, t, store, "c-new"), uint64(1000), "the sequence was not advanced")
}

func TestHighestKVRevision_AScanThatDoesNotCompleteIsAnError(t *testing.T) {
	src := childSource()
	src.memberErr = errors.New("a record could not be read")

	_, err := highestKVRevision(context.Background(), src)

	require.ErrorContains(t, err, "reading the members in KV")
}

func TestFlipSubcommand_Run(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	closed := false
	rc := func(args ...string) commands.RunContext {
		return commands.RunContext{
			KVExport:      src,
			PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() { closed = true }, nil },
			Args:          args,
		}
	}

	t.Run("without --confirm it is a dry run", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&flipSubcommand{out: &out}).Run(ctx, rc()))
		assert.Contains(t, out.String(), "dry run")
		assert.True(t, closed, "the connection is released")
	})

	t.Run("with --confirm it advances the sequence", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&flipSubcommand{out: &out}).Run(ctx, rc("--confirm")))
		assert.Contains(t, out.String(), "is now above")
	})

	t.Run("an argument that is not a flag is refused", func(t *testing.T) {
		err := (&flipSubcommand{out: &bytes.Buffer{}}).Run(ctx, rc("now"))
		require.ErrorContains(t, err, `unexpected argument "now"`)
	})

	t.Run("without the wiring it fails", func(t *testing.T) {
		err := (&flipSubcommand{}).Run(ctx, commands.RunContext{})
		require.Error(t, err)
	})
}

func TestVerify_ByContentIgnoresTheRevisionsAndWhatAStoreSetsItself(t *testing.T) {
	ctx := context.Background()

	t.Run("a record at another revision is a difference, unless the comparison is by content", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.members[0].revision += 1000

		_, err := runVerify(ctx, src, store, verifyOptions{})
		require.ErrorContains(t, err, "differ")

		results, err := runVerify(ctx, src, store, verifyOptions{ByContent: true})
		require.NoError(t, err)
		member := verifyResultOf(t, results, entityMembers)
		assert.Zero(t, member.differences())
	})

	t.Run("another content is a difference either way", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.invites[0].record.Role = "changed"

		results, err := runVerify(ctx, src, store, verifyOptions{ByContent: true})

		require.Error(t, err)
		assert.EqualValues(t, 1, verifyResultOf(t, results, entityInvites).Mismatched)
	})

	t.Run("the update time of a brief is ignored by content and is a difference otherwise", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.briefs[0].record.UpdatedAt = src.briefs[0].record.UpdatedAt.Add(1e9)

		_, err := runVerify(ctx, src, store, verifyOptions{})
		require.Error(t, err)

		results, err := runVerify(ctx, src, store, verifyOptions{ByContent: true})
		require.NoError(t, err)
		brief := verifyResultOf(t, results, entityBriefs)
		assert.Zero(t, brief.differences())
	})

	t.Run("another content of a brief is still a difference by content", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.briefs[0].record.BriefText = "changed"

		results, err := runVerify(ctx, src, store, verifyOptions{ByContent: true})

		require.Error(t, err)
		assert.EqualValues(t, 1, verifyResultOf(t, results, entityBriefs).Mismatched)
	})

	t.Run("what only PostgreSQL has is still extra, which the way back needs", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		require.NoError(t, store.CreateInvite(ctx, storetest.NewInvite("i-pg-only", "c-1", "first.last8@example.com")))

		results, err := runVerify(ctx, src, store, verifyOptions{ByContent: true})

		require.Error(t, err)
		assert.EqualValues(t, 1, verifyResultOf(t, results, entityInvites).Extra)
	})

	t.Run("the flag reaches the comparison", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.members[0].revision += 1000
		rc := commands.RunContext{
			KVExport:      src,
			PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() {}, nil },
		}
		rc.Args = []string{"--by-content"}
		require.NoError(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, rc))
		rc.Args = nil
		require.Error(t, (&verifySubcommand{out: &bytes.Buffer{}}).Run(ctx, rc))
	})
}
