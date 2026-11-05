// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package backfill

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

func result(t *testing.T, results []Result, entity string) Result {
	t.Helper()
	for _, r := range results {
		if r.Entity == entity {
			return r
		}
	}
	require.Failf(t, "no result", "entity %s", entity)
	return Result{}
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

func TestRun_CopiesWithTheKVRevisions(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := Run(ctx, newSource(), store, Options{})
	require.NoError(t, err, "an orphan is reported but is not an error: KV is expected to hold some")

	committees, settings, members := result(t, results, EntityCommittees), result(t, results, resultSettings), result(t, results, EntityMembers)
	assert.Equal(t, Result{Entity: EntityCommittees, Scanned: 2, Written: 2, HighestRevision: 20}, withoutProblems(committees))
	assert.Equal(t, Result{Entity: resultSettings, Scanned: 1, Written: 1, HighestRevision: 11}, withoutProblems(settings))
	assert.EqualValues(t, 3, members.Scanned)
	assert.EqualValues(t, 2, members.Written)
	assert.EqualValues(t, 1, members.Orphans)
	assert.EqualValues(t, 32, members.HighestRevision, "the orphan's revision counts: it is in KV")
	assert.Equal(t, []Problem{{UID: "m-orphan", Kind: "orphan", Message: "its committee does not exist in KV"}}, members.Problems)

	for uid, want := range map[string]uint64{"c-1": 10, "c-2": 20} {
		base, revision, err := store.GetBase(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, want, revision, uid)
		assert.Equal(t, storetest.NewCommittee(uid, "project-1", base.Name).Name, base.Name)
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

func withoutProblems(r Result) Result {
	r.Problems = nil
	return r
}

func TestRun_AgainChangesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	src.members = src.members[:2]

	_, err := Run(ctx, src, store, Options{})
	require.NoError(t, err)
	results, err := Run(ctx, src, store, Options{})
	require.NoError(t, err)

	for _, r := range results {
		assert.Zero(t, r.Written, r.Entity)
		assert.Equal(t, r.Scanned, r.Unchanged, r.Entity)
	}
	_, revision, err := store.GetBase(ctx, "c-1")
	require.NoError(t, err)
	assert.EqualValues(t, 10, revision, "a second run leaves the revision alone")
}

func TestRun_KVAheadUpdatesAndPostgreSQLAheadIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	src.members = src.members[:1]
	_, err := Run(ctx, src, store, Options{})
	require.NoError(t, err)

	// KV moves on: a new revision and new content for every record.
	src.committees[0].Base.Description = "changed in KV"
	src.committees[0].BaseRevision = 100
	src.committees[0].Settings.Writers = nil
	src.committees[0].SettingsRevision = 101
	src.members[0].member.JobTitle = "Changed in KV"
	src.members[0].revision = 102
	results, err := Run(ctx, src, store, Options{})
	require.NoError(t, err)

	assert.EqualValues(t, 1, result(t, results, EntityCommittees).Written)
	assert.EqualValues(t, 1, result(t, results, resultSettings).Written)
	assert.EqualValues(t, 1, result(t, results, EntityMembers).Written)
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
	results, err = Run(ctx, src, store, Options{Entities: []string{EntityCommittees}})
	require.Error(t, err)
	committees := result(t, results, EntityCommittees)
	assert.EqualValues(t, 1, committees.Errors)
	assert.Equal(t, []Problem{{UID: "c-1", Kind: "ahead", Message: "PostgreSQL has a newer revision than KV"}}, committees.Problems)

	// The same for a member.
	member, memberRevision, err = store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	memberCtx, err := postgres.WithRevision(ctx, 600)
	require.NoError(t, err)
	_, err = store.UpdateMember(memberCtx, member, memberRevision)
	require.NoError(t, err)
	results, err = Run(ctx, src, store, Options{Entities: []string{EntityMembers}})
	require.Error(t, err)
	assert.Equal(t, []Problem{{UID: "m-1", Kind: "ahead", Message: "PostgreSQL has a newer revision than KV"}}, result(t, results, EntityMembers).Problems)
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := Run(ctx, newSource(), store, Options{DryRun: true, IncludeOrphans: true})

	require.NoError(t, err)
	assert.EqualValues(t, 2, result(t, results, EntityCommittees).Written, "what it would write")
	assert.EqualValues(t, 3, result(t, results, EntityMembers).Written)
	uids, err := store.ListAllUIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, uids)
	_, _, err = store.GetMember(ctx, "m-1")
	assert.Error(t, err)
}

func TestRun_IncludeOrphansCopiesThem(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := Run(ctx, newSource(), store, Options{IncludeOrphans: true})

	require.NoError(t, err)
	assert.Zero(t, result(t, results, EntityMembers).Orphans)
	_, _, err = store.GetMember(ctx, "m-orphan")
	require.NoError(t, err)
}

func TestRun_ReportsWhatPostgreSQLRefusesWithItsUID(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	// Two members of one committee with the same email: KV never enforced it case-insensitively, the schema does.
	src.members = []memberRecord{
		kvMember("m-a", "c-1", "first.last@example.com", 30),
		kvMember("m-b", "c-1", "First.Last@Example.com", 31),
	}

	results, err := Run(ctx, src, store, Options{Workers: 1})

	require.Error(t, err)
	members := result(t, results, EntityMembers)
	assert.EqualValues(t, 1, members.Written)
	assert.EqualValues(t, 1, members.Errors)
	require.Len(t, members.Problems, 1)
	assert.Equal(t, "m-b", members.Problems[0].UID)
	assert.Equal(t, "constraint", members.Problems[0].Kind)
}

func TestRun_ReportsACommitteePostgreSQLRefusesWithItsUID(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	// Two committees of one project with the same name: the schema enforces the name, KV only held a lock.
	src := &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-a", "Same Name", 10, 11), kvCommittee("c-b", "Same Name", 20, 21)}}

	results, err := Run(ctx, src, store, Options{Workers: 1})

	require.Error(t, err)
	committees := result(t, results, EntityCommittees)
	assert.EqualValues(t, 1, committees.Written)
	require.Len(t, committees.Problems, 1)
	assert.Equal(t, "c-b", committees.Problems[0].UID)
	assert.Equal(t, "constraint", committees.Problems[0].Kind)
}

func TestRun_SettingsMissingInPostgreSQLIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	bare := storetest.NewCommittee("c-1", "project-1", "Committee One")
	bare.CommitteeSettings = nil
	withRevision, err := postgres.WithRevision(ctx, 10)
	require.NoError(t, err)
	require.NoError(t, store.Create(withRevision, bare))

	results, err := Run(ctx, &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 10, 11)}}, store, Options{Entities: []string{EntityCommittees}})

	require.Error(t, err)
	settings := result(t, results, resultSettings)
	require.Len(t, settings.Problems, 1)
	assert.Equal(t, Problem{UID: "c-1", Kind: "missing", Message: "the committee exists in PostgreSQL without settings, which can only be created with a committee"}, settings.Problems[0])
}

func TestRun_AScanThatEndsWithAnErrorIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := newSource()
	src.committeeErr = errors.New("failed_reads=2")

	results, err := Run(ctx, src, store, Options{Entities: []string{EntityCommittees}})

	require.Error(t, err)
	committees := result(t, results, EntityCommittees)
	assert.EqualValues(t, 2, committees.Written, "the records that could be read were still copied")
	assert.Equal(t, []Problem{{Kind: "source", Message: "failed_reads=2"}}, committees.Problems)
}

func TestRun_OnlyTheEntitiesAskedFor(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := Run(ctx, newSource(), store, Options{Entities: []string{EntityMembers}, IncludeOrphans: true})

	require.NoError(t, err)
	require.Len(t, results, 1)
	uids, err := store.ListAllUIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, uids, "no committee was copied")

	_, err = Run(ctx, newSource(), store, Options{Entities: []string{"invites"}})
	var validation interface{ Error() string }
	require.ErrorAs(t, err, &validation)
	assert.Contains(t, err.Error(), `unknown entity "invites"`)
}

func TestRun_ManyRecordsThroughSmallBuffersAndManyWorkers(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 1, 2)}}
	for i := range 200 {
		src.members = append(src.members, kvMember(fmt.Sprintf("m-%03d", i), "c-1", fmt.Sprintf("first.last%03d@example.com", i), uint64(1000+i)))
	}

	results, err := Run(ctx, src, store, Options{Workers: 8, BatchSize: 3, ProgressEvery: 50})

	require.NoError(t, err)
	members := result(t, results, EntityMembers)
	assert.EqualValues(t, 200, members.Written)
	assert.EqualValues(t, 1199, members.HighestRevision)
}
