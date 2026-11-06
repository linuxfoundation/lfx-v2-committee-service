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
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// postgresHolding is a PostgreSQL store holding childSource, as it is after a migration or in dual-pg mode.
func postgresHolding(t *testing.T) *postgres.Store {
	t.Helper()
	_, store := migratedSource(t, backfillOptions{})
	return store
}

// verifyByContent compares KV with PostgreSQL by content, as the checklist for the way back says to.
func verifyByContent(ctx context.Context, kv natstest.RepairBackend, store *postgres.Store) ([]verifyResult, error) {
	return runVerify(ctx, kv, store, verifyOptions{ByContent: true})
}

func TestRunReverse_CopiesEveryEntityAndKVThenMatchesByContent(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)

	results, err := runReverse(ctx, store, kv, backfillOptions{})

	require.NoError(t, err)
	committees, settings := result(t, results, entityCommittees), result(t, results, resultSettings)
	assert.EqualValues(t, 2, committees.Written)
	assert.EqualValues(t, 1, settings.Written)
	for _, entity := range append([]string{entityMembers}, childEntities...) {
		r := result(t, results, entity)
		assert.Zero(t, r.Errors, entity)
		assert.Zero(t, r.Unchanged, entity)
		assert.NotZero(t, r.Written, entity)
	}
	verified, err := verifyByContent(ctx, kv, store)
	require.NoError(t, err, "KV holds what PostgreSQL holds: %+v", verified)
	for _, v := range verified {
		assert.Zero(t, v.differences(), v.Entity)
	}
}

func TestRunReverse_AgainChangesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)
	_, err := runReverse(ctx, store, kv, backfillOptions{})
	require.NoError(t, err)

	results, err := runReverse(ctx, store, kv, backfillOptions{})

	require.NoError(t, err)
	for _, r := range results {
		assert.Zero(t, r.Written, r.Entity)
		assert.NotZero(t, r.Unchanged, r.Entity)
	}
}

func TestRunReverse_DryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)

	results, err := runReverse(ctx, store, kv, backfillOptions{DryRun: true})

	require.NoError(t, err)
	assert.EqualValues(t, 2, result(t, results, entityCommittees).Written, "what a run would write")
	uids, err := kv.ListAllUIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, uids)
}

func TestRunReverse_DryRunWritesNothingForChildrenToo(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)
	_, err := runReverse(ctx, store, kv, backfillOptions{Entities: []string{entityCommittees}})
	require.NoError(t, err)
	link, revision, err := store.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	_, err = runReverse(ctx, store, kv, backfillOptions{Entities: []string{entityLinks}})
	require.NoError(t, err)
	link.Name = "Renamed"
	require.NoError(t, store.UpdateLink(ctx, link, revision))

	results, err := runReverse(ctx, store, kv, backfillOptions{DryRun: true})

	require.NoError(t, err)
	assert.EqualValues(t, 1, result(t, results, entityLinks).Written, "what a run would replace")
	assert.EqualValues(t, 2, result(t, results, entityMembers).Written, "what a run would create")
	held, _, err := kv.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	assert.NotEqual(t, "Renamed", held.Name, "a dry run replaces nothing")
	_, _, err = kv.GetMember(ctx, "m-1")
	assert.Error(t, err, "a dry run creates nothing")
}

func TestRunReverse_ARecordKVHoldsWithOtherContentIsReplacedAndOneOnlyKVHoldsStays(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)
	_, err := runReverse(ctx, store, kv, backfillOptions{})
	require.NoError(t, err)

	// KV falls behind PostgreSQL on a member and a link, and holds a member PostgreSQL does not have.
	member, revision, err := store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	member.Role.Name = "Observer"
	_, err = store.UpdateMember(ctx, member, revision)
	require.NoError(t, err)
	link, linkRevision, err := store.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	link.Name = "Renamed"
	require.NoError(t, store.UpdateLink(ctx, link, linkRevision))
	require.NoError(t, kv.CreateMember(ctx, storetest.NewMember("m-kv-only", "c-1", "first.last9@example.com")))

	results, err := runReverse(ctx, store, kv, backfillOptions{})

	require.NoError(t, err)
	assert.EqualValues(t, 1, result(t, results, entityMembers).Written)
	assert.EqualValues(t, 1, result(t, results, entityLinks).Written)
	held, _, err := kv.GetMember(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "Observer", held.Role.Name)
	heldLink, _, err := kv.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	assert.Equal(t, "Renamed", heldLink.Name)
	_, _, err = kv.GetMember(ctx, "m-kv-only")
	assert.NoError(t, err, "a copy back never deletes")
	verified, err := verifyByContent(ctx, kv, store)
	require.Error(t, err, "the member only KV holds is a difference")
	assert.EqualValues(t, 1, verifyResultOf(t, verified, entityMembers).Missing)
}

func TestRunReverse_SettingsKVLacksNextToAnExistingCommitteeAreReported(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)
	c := storetest.NewCommittee("c-1", "project-1", "Committee One")
	require.NoError(t, kv.Create(ctx, &model.Committee{CommitteeBase: c.CommitteeBase}))

	results, err := runReverse(ctx, store, kv, backfillOptions{Entities: []string{entityCommittees}})

	require.Error(t, err)
	settings := result(t, results, resultSettings)
	assert.Equal(t, []backfillProblem{{UID: "c-1", Kind: "missing", Message: "the committee exists in KV without settings, which can only be created with a committee"}}, settings.Problems)
}

func TestRunReverse_AWindowKVHoldsForAnotherBriefIsAConstraint(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)
	_, err := runReverse(ctx, store, kv, backfillOptions{Entities: []string{entityCommittees}})
	require.NoError(t, err)
	_, err = kv.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-other", "c-1", storetest.FixtureWindow))
	require.NoError(t, err)

	results, err := runReverse(ctx, store, kv, backfillOptions{Entities: []string{entityBriefs}})

	require.Error(t, err)
	assert.Equal(t, []backfillProblem{{UID: "b-1", Kind: "constraint", Message: "the window already has another brief, uid b-other"}}, result(t, results, entityBriefs).Problems)
}

// hidingCommittee is a PostgreSQL store that does not list one committee, so the children of that committee are orphans.
type hidingCommittee struct {
	*postgres.Store
	uid string
}

func (h hidingCommittee) ListAllUIDs(ctx context.Context) ([]string, error) {
	uids, err := h.Store.ListAllUIDs(ctx)
	var kept []string
	for _, uid := range uids {
		if uid != h.uid {
			kept = append(kept, uid)
		}
	}
	return kept, err
}

func TestRunReverse_OrphansAreReportedAndCopiedOnlyOnRequest(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	src := hidingCommittee{Store: store, uid: "c-1"}

	kv := natstest.NewRepairBackend(t)
	results, err := runReverse(ctx, src, kv, backfillOptions{Entities: []string{entityMembers}})
	require.NoError(t, err, "an orphan is reported and is not an error")
	members := result(t, results, entityMembers)
	assert.EqualValues(t, 2, members.Orphans)
	assert.EqualValues(t, 0, members.Written)
	assert.Equal(t, "its committee does not exist in PostgreSQL", members.Problems[0].Message)

	results, err = runReverse(ctx, src, kv, backfillOptions{Entities: []string{entityMembers}, IncludeOrphans: true})
	require.NoError(t, err)
	assert.EqualValues(t, 2, result(t, results, entityMembers).Written)
}

func TestRunReverse_OnlyTheEntitiesAskedForAndAnUnknownOneIsRefused(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)

	results, err := runReverse(ctx, store, kv, backfillOptions{Entities: []string{entityLinks}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, entityLinks, results[0].Entity)

	_, err = runReverse(ctx, store, kv, backfillOptions{Entities: []string{"widgets"}})
	require.ErrorContains(t, err, `unknown entity "widgets"`)
}

func TestMigrate_RunDirectionPGToKV(t *testing.T) {
	ctx := context.Background()
	store := postgresHolding(t)
	kv := natstest.NewRepairBackend(t)
	rc := func(args ...string) commands.RunContext {
		return commands.RunContext{
			KVRepair:      kv,
			PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() {}, nil },
			Args:          args,
		}
	}

	t.Run("a run copies and says nothing was deleted", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, (&migrateSubcommand{out: &out}).Run(ctx, rc("--direction=pg-to-kv")))
		assert.Contains(t, out.String(), "committees scanned=2 written=2")
		assert.Contains(t, out.String(), "nothing was deleted")
		assert.NotContains(t, out.String(), "highest KV revision")
		_, err := verifyByContent(ctx, kv, store)
		require.NoError(t, err)
	})

	t.Run("an unknown direction is refused before anything connects", func(t *testing.T) {
		err := (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, rc("--direction=sideways"))
		require.ErrorContains(t, err, `unknown direction "sideways"`)
	})

	t.Run("without the KV store it says so", func(t *testing.T) {
		err := (&migrateSubcommand{out: &bytes.Buffer{}}).Run(ctx, commands.RunContext{Args: []string{"--direction=pg-to-kv"}})
		require.ErrorContains(t, err, "not wired")
	})
}
