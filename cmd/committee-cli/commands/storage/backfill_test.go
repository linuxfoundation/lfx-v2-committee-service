// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// fakeSource is a KV store with the records it is given and the revisions they have.
type fakeSource struct {
	committees   []port.CommitteeExport
	members      []memberRecord
	committeeErr error // returned by the committee scan after the committees, like an aggregated read failure
	memberErr    error
}

func (f *fakeSource) ListAllUIDs(context.Context) ([]string, error) {
	var uids []string
	for _, c := range f.committees {
		uids = append(uids, c.Base.UID)
	}
	return uids, nil
}

func (f *fakeSource) EachCommitteeExport(_ context.Context, fn func(port.CommitteeExport) error) error {
	for _, c := range f.committees {
		if err := fn(c); err != nil {
			return err
		}
	}
	return f.committeeErr
}

func (f *fakeSource) EachMemberExport(_ context.Context, fn func(*model.CommitteeMember, uint64) error) error {
	for _, m := range f.members {
		if err := fn(m.member, m.revision); err != nil {
			return err
		}
	}
	return f.memberErr
}

// kvCommittee is a committee as KV holds it, with revisions of its own.
func kvCommittee(uid, name string, baseRevision, settingsRevision uint64) port.CommitteeExport {
	c := storetest.NewCommittee(uid, "project-1", name)
	export := port.CommitteeExport{Base: &c.CommitteeBase, BaseRevision: baseRevision}
	if settingsRevision != 0 {
		export.Settings, export.SettingsRevision = c.CommitteeSettings, settingsRevision
	}
	return export
}

func kvMember(uid, committeeUID, email string, revision uint64) memberRecord {
	return memberRecord{member: storetest.NewMember(uid, committeeUID, email), revision: revision}
}

func result(t *testing.T, results []backfillResult, entity string) backfillResult {
	t.Helper()
	for _, r := range results {
		if r.Entity == entity {
			return r
		}
	}
	require.Failf(t, "no result", "entity %s", entity)
	return backfillResult{}
}

func newSource() *fakeSource {
	return &fakeSource{
		committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 10, 11), kvCommittee("c-2", "Committee Two", 20, 0)},
		members: []memberRecord{
			kvMember("m-1", "c-1", "first.last@example.com", 30),
			kvMember("m-2", "c-1", "first.last2@example.com", 31),
			kvMember("m-orphan", "c-deleted", "first.last3@example.com", 32),
		},
	}
}

func TestRunBackfill_CopiesWithTheKVRevisions(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, newSource(), store, backfillOptions{})
	require.NoError(t, err, "an orphan is reported but is not an error: KV is expected to hold some")

	committees, settings, members := result(t, results, entityCommittees), result(t, results, resultSettings), result(t, results, entityMembers)
	assert.Equal(t, backfillResult{Entity: entityCommittees, Scanned: 2, Written: 2, HighestRevision: 20}, withoutProblems(committees))
	assert.Equal(t, backfillResult{Entity: resultSettings, Scanned: 1, Written: 1, HighestRevision: 11}, withoutProblems(settings))
	assert.EqualValues(t, 3, members.Scanned)
	assert.EqualValues(t, 2, members.Written)
	assert.EqualValues(t, 1, members.Orphans)
	assert.EqualValues(t, 32, members.HighestRevision, "the orphan's revision counts: it is in KV")
	assert.Equal(t, []backfillProblem{{UID: "m-orphan", Kind: "orphan", Message: "its committee does not exist in KV"}}, members.Problems)

	for uid, want := range map[string]struct {
		revision uint64
		name     string
	}{"c-1": {10, "Committee One"}, "c-2": {20, "Committee Two"}} {
		base, revision, err := store.GetBase(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, want.revision, revision, uid)
		assert.Equal(t, want.name, base.Name, uid)
	}
	_, settingsRevision, err := store.GetSettings(ctx, "c-1")
	require.NoError(t, err)
	assert.EqualValues(t, 11, settingsRevision)
	for uid, want := range map[string]uint64{"m-1": 30, "m-2": 31} {
		_, revision, err := store.GetMember(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, want, revision, uid)
	}
	_, _, err = store.GetMember(ctx, "m-orphan")
	assert.Error(t, err, "an orphan is not copied")
}

func withoutProblems(r backfillResult) backfillResult {
	r.Problems = nil
	return r
}

func TestRunBackfill_AgainChangesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	src.members = src.members[:2]

	_, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)
	results, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)

	for _, r := range results {
		assert.Zero(t, r.Written, r.Entity)
		assert.Equal(t, r.Scanned, r.Unchanged, r.Entity)
	}
	_, revision, err := store.GetBase(ctx, "c-1")
	require.NoError(t, err)
	assert.EqualValues(t, 10, revision, "a second run leaves the revision alone")
}

func TestRunBackfill_KVAheadUpdatesAndPostgreSQLAheadIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	src.members = src.members[:1]
	_, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)

	// KV moves on: a new revision and new content for every record.
	src.committees[0].Base.Description = "changed in KV"
	src.committees[0].BaseRevision = 100
	src.committees[0].Settings.Writers = nil
	src.committees[0].SettingsRevision = 101
	src.members[0].member.JobTitle = "Changed in KV"
	src.members[0].revision = 102
	results, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)

	assert.EqualValues(t, 1, result(t, results, entityCommittees).Written)
	assert.EqualValues(t, 1, result(t, results, resultSettings).Written)
	assert.EqualValues(t, 1, result(t, results, entityMembers).Written)
	base, revision, err := store.GetBase(ctx, "c-1")
	require.NoError(t, err)
	assert.Equal(t, "changed in KV", base.Description)
	assert.EqualValues(t, 100, revision)
	settings, settingsRevision, err := store.GetSettings(ctx, "c-1")
	require.NoError(t, err)
	assert.Empty(t, settings.Writers)
	assert.EqualValues(t, 101, settingsRevision)
	member, memberRevision, err := store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "Changed in KV", member.JobTitle)
	assert.EqualValues(t, 102, memberRevision)

	// PostgreSQL moves past KV: a migration cannot reconcile that and says so, with the uid.
	pgCtx, err := postgres.WithRevision(ctx, 500)
	require.NoError(t, err)
	require.NoError(t, store.UpdateBase(pgCtx, &model.Committee{CommitteeBase: *src.committees[0].Base}, 100))
	results, err = runBackfill(ctx, src, store, backfillOptions{Entities: []string{entityCommittees}})
	require.Error(t, err)
	committees := result(t, results, entityCommittees)
	assert.EqualValues(t, 1, committees.Errors)
	assert.Equal(t, []backfillProblem{{UID: "c-1", Kind: "ahead", Message: "PostgreSQL has a newer revision than KV"}}, committees.Problems)

	// The same for a member.
	member, memberRevision, err = store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	memberCtx, err := postgres.WithRevision(ctx, 600)
	require.NoError(t, err)
	_, err = store.UpdateMember(memberCtx, member, memberRevision)
	require.NoError(t, err)
	results, err = runBackfill(ctx, src, store, backfillOptions{Entities: []string{entityMembers}})
	require.Error(t, err)
	assert.Equal(t, []backfillProblem{{UID: "m-1", Kind: "ahead", Message: "PostgreSQL has a newer revision than KV"}}, result(t, results, entityMembers).Problems)
}

func TestRunBackfill_DryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, newSource(), store, backfillOptions{DryRun: true, IncludeOrphans: true})

	require.NoError(t, err)
	assert.EqualValues(t, 2, result(t, results, entityCommittees).Written, "what it would write")
	assert.EqualValues(t, 3, result(t, results, entityMembers).Written)
	uids, err := store.ListAllUIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, uids)
	_, _, err = store.GetMember(ctx, "m-1")
	assert.Error(t, err)
}

func TestRunBackfill_IncludeOrphansCopiesThem(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, newSource(), store, backfillOptions{IncludeOrphans: true})

	require.NoError(t, err)
	assert.Zero(t, result(t, results, entityMembers).Orphans)
	_, _, err = store.GetMember(ctx, "m-orphan")
	require.NoError(t, err)
}

func TestRunBackfill_ReportsWhatPostgreSQLRefusesWithItsUID(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	// Two members of one committee with the same email: KV never enforced it case-insensitively, the schema does.
	src.members = []memberRecord{
		kvMember("m-a", "c-1", "first.last@example.com", 30),
		kvMember("m-b", "c-1", "First.Last@Example.com", 31),
	}

	results, err := runBackfill(ctx, src, store, backfillOptions{Workers: 1})

	require.Error(t, err)
	members := result(t, results, entityMembers)
	assert.EqualValues(t, 1, members.Written)
	assert.EqualValues(t, 1, members.Errors)
	require.Len(t, members.Problems, 1)
	assert.Equal(t, "m-b", members.Problems[0].UID)
	assert.Equal(t, "constraint", members.Problems[0].Kind)
	assert.NotContains(t, members.Problems[0].Message, "example.com", "the problem names the record by uid, not by the personal data in it")
}

func TestRunBackfill_ReportsACommitteePostgreSQLRefusesWithItsUID(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	// Two committees of one project with the same name: the schema enforces the name, KV only held a lock.
	src := &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-a", "Same Name", 10, 11), kvCommittee("c-b", "Same Name", 20, 21)}}

	results, err := runBackfill(ctx, src, store, backfillOptions{Workers: 1})

	require.Error(t, err)
	committees := result(t, results, entityCommittees)
	assert.EqualValues(t, 1, committees.Written)
	require.Len(t, committees.Problems, 1)
	assert.Equal(t, "c-b", committees.Problems[0].UID)
	assert.Equal(t, "constraint", committees.Problems[0].Kind)

	// The settings of the committee that was refused are accounted for: scanned = written + unchanged + errors.
	settings := result(t, results, resultSettings)
	assert.EqualValues(t, 2, settings.Scanned)
	assert.EqualValues(t, 1, settings.Written)
	assert.EqualValues(t, 1, settings.Errors)
	assert.Equal(t, []backfillProblem{{UID: "c-b", Kind: "skipped", Message: "its committee could not be copied"}}, settings.Problems)
}

// failingGetSink is the PostgreSQL store with a committee read that fails for one uid.
type failingGetSink struct {
	*postgres.Store
	uid string
}

func (f failingGetSink) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	if uid == f.uid {
		return nil, 0, errors.New("simulated read failure")
	}
	return f.Store.GetBase(ctx, uid)
}

func TestRunBackfill_ACommitteeThatCannotBeReadSkipsItsSettingsToo(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	sink := failingGetSink{Store: store, uid: "c-1"}

	results, err := runBackfill(ctx, newSource(), sink, backfillOptions{})

	require.Error(t, err)
	committees := result(t, results, entityCommittees)
	assert.Equal(t, []backfillProblem{{UID: "c-1", Kind: "read", Message: "simulated read failure"}}, committees.Problems)
	settings := result(t, results, resultSettings)
	assert.EqualValues(t, 1, settings.Scanned)
	assert.EqualValues(t, 1, settings.Errors, "c-1 is the committee with settings")
	assert.Equal(t, []backfillProblem{{UID: "c-1", Kind: "skipped", Message: "its committee could not be copied"}}, settings.Problems)
}

func TestRunBackfill_SettingsMissingInPostgreSQLIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	bare := storetest.NewCommittee("c-1", "project-1", "Committee One")
	bare.CommitteeSettings = nil
	withRevision, err := postgres.WithRevision(ctx, 10)
	require.NoError(t, err)
	require.NoError(t, store.Create(withRevision, bare))

	results, err := runBackfill(ctx, &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 10, 11)}}, store, backfillOptions{Entities: []string{entityCommittees}})

	require.Error(t, err)
	settings := result(t, results, resultSettings)
	require.Len(t, settings.Problems, 1)
	assert.Equal(t, backfillProblem{UID: "c-1", Kind: "missing", Message: "the committee exists in PostgreSQL without settings, which can only be created with a committee"}, settings.Problems[0])
}

func TestRunBackfill_AScanThatEndsWithAnErrorIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	src.committeeErr = errors.New("failed_reads=2")

	results, err := runBackfill(ctx, src, store, backfillOptions{Entities: []string{entityCommittees}})

	require.Error(t, err)
	committees := result(t, results, entityCommittees)
	assert.EqualValues(t, 2, committees.Written, "the records that could be read were still copied")
	assert.Equal(t, []backfillProblem{{Kind: "source", Message: "failed_reads=2"}}, committees.Problems)
}

func TestRunBackfill_OnlyTheEntitiesAskedFor(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, newSource(), store, backfillOptions{Entities: []string{entityMembers}, IncludeOrphans: true})

	require.NoError(t, err)
	require.Len(t, results, 1)
	uids, err := store.ListAllUIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, uids, "no committee was copied")

	_, err = runBackfill(ctx, newSource(), store, backfillOptions{Entities: []string{"invites"}})
	var validation errs.Validation
	require.ErrorAs(t, err, &validation)
	assert.Contains(t, err.Error(), `unknown entity "invites"`)
}

func TestRunBackfill_ManyRecordsThroughSmallBuffersAndManyWorkers(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 1, 2)}}
	for i := range 200 {
		src.members = append(src.members, kvMember(fmt.Sprintf("m-%03d", i), "c-1", fmt.Sprintf("first.last%03d@example.com", i), uint64(1000+i)))
	}

	results, err := runBackfill(ctx, src, store, backfillOptions{Workers: 8, BatchSize: 3, ProgressEvery: 50})

	require.NoError(t, err)
	members := result(t, results, entityMembers)
	assert.EqualValues(t, 200, members.Written)
	assert.EqualValues(t, 1199, members.HighestRevision)
}
