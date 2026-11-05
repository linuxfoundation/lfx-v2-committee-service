// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"errors"
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

func rec[T any](record T, revision uint64) childRecord[T] {
	return childRecord[T]{record: record, revision: revision}
}

// kvBrief and kvThrottle are records as the exporter streams them: with Revision set to the revision they were read at.
func kvBrief(b *model.GroupWeeklyBrief, revision uint64) childRecord[*model.GroupWeeklyBrief] {
	b.Revision = revision
	return rec(b, revision)
}

func kvThrottle(t *model.GroupWeeklyBriefThrottle, revision uint64) childRecord[*model.GroupWeeklyBriefThrottle] {
	t.Revision = revision
	return rec(t, revision)
}

// childEntities are the entities that belong to a committee, besides the members.
var childEntities = []string{entityInvites, entityApplications, entityLinks, entityFolders, entityDocuments, entityBriefs, entityThrottles}

// childSource is newSource with one record of every other entity in committee c-1 and one in a committee KV no
// longer has. The revision of each is the entity's base, 40 for the invites up to 100 for the throttles, and
// the orphan's is one more.
func childSource() *fakeSource {
	src := newSource()
	window := storetest.FixtureWindow
	src.invites = append(src.invites, rec(storetest.NewInvite("i-1", "c-1", "first.last@example.com"), 40),
		rec(storetest.NewInvite("i-orphan", "c-deleted", "first.last2@example.com"), 41))
	src.applications = append(src.applications, rec(storetest.NewApplication("a-1", "c-1", "first.last@example.com"), 50),
		rec(storetest.NewApplication("a-orphan", "c-deleted", "first.last2@example.com"), 51))
	src.links = append(src.links, rec(storetest.NewLink("l-1", "c-1"), 60), rec(storetest.NewLink("l-orphan", "c-deleted"), 61))
	src.folders = append(src.folders, rec(storetest.NewFolder("f-1", "c-1", "Charters"), 70),
		rec(storetest.NewFolder("f-orphan", "c-deleted", "Charters"), 71))
	src.documents = append(src.documents, rec(storetest.NewDocument("d-1", "c-1", "Minutes"), 80),
		rec(storetest.NewDocument("d-orphan", "c-deleted", "Minutes"), 81))
	src.briefs = append(src.briefs, kvBrief(storetest.NewBrief("b-1", "c-1", window), 90),
		kvBrief(storetest.NewBrief("b-orphan", "c-deleted", window), 91))
	src.throttles = append(src.throttles, kvThrottle(storetest.NewThrottle("c-1", window), 100),
		kvThrottle(storetest.NewThrottle("c-deleted", window), 101))
	return src
}

// pgRevisions is the revision of the live record of every child entity in PostgreSQL, 0 when there is none.
func pgRevisions(ctx context.Context, t *testing.T, store *postgres.Store) map[string]uint64 {
	t.Helper()
	revisions := map[string]uint64{entityBriefs: 0, entityThrottles: 0}
	_, revisions[entityInvites], _ = store.GetInvite(ctx, "i-1")
	_, revisions[entityApplications], _ = store.GetApplication(ctx, "a-1")
	_, revisions[entityLinks], _ = store.GetLink(ctx, "c-1", "l-1")
	_, revisions[entityFolders], _ = store.GetLinkFolder(ctx, "c-1", "f-1")
	_, revisions[entityDocuments], _ = store.GetDocumentMetadata(ctx, "c-1", "d-1")
	if brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "c-1", *storetest.NewBrief("", "c-1", storetest.FixtureWindow)); err == nil && brief != nil {
		revisions[entityBriefs] = brief.Revision
	}
	if throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "c-1", storetest.FixtureWindow); err == nil && throttle != nil {
		revisions[entityThrottles] = throttle.Revision
	}
	return revisions
}

func TestRunBackfill_CopiesEveryEntityWithTheKVRevisions(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, childSource(), store, backfillOptions{})

	require.NoError(t, err, "orphans are reported but are not an error")
	for _, entity := range childEntities {
		r := result(t, results, entity)
		assert.EqualValues(t, 2, r.Scanned, entity)
		assert.EqualValues(t, 1, r.Written, entity)
		assert.EqualValues(t, 1, r.Orphans, entity)
		assert.Zero(t, r.Errors, entity)
	}
	assert.Equal(t, map[string]uint64{
		entityInvites: 40, entityApplications: 50, entityLinks: 60, entityFolders: 70, entityDocuments: 80, entityBriefs: 90, entityThrottles: 100,
	}, pgRevisions(ctx, t, store), "each row has the revision its KV record had")

	invite, _, err := store.GetInvite(ctx, "i-1")
	require.NoError(t, err)
	assert.Equal(t, "first.last@example.com", invite.InviteeEmail)
	document, _, err := store.GetDocumentMetadata(ctx, "c-1", "d-1")
	require.NoError(t, err)
	assert.Equal(t, "minutes.pdf", document.FileName, "the metadata is copied; the file stays in the Object Store")
	brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "c-1", *storetest.NewBrief("", "c-1", storetest.FixtureWindow))
	require.NoError(t, err)
	assert.Equal(t, "The group met twice.", brief.BriefText)
	assert.True(t, storetest.NewBrief("", "", storetest.FixtureWindow).UpdatedAt.Equal(brief.UpdatedAt), "the update time KV stamped is kept, not replaced by the time of the copy")
	throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "c-1", storetest.FixtureWindow)
	require.NoError(t, err)
	assert.EqualValues(t, 2, throttle.RegenerationsUsed)

	for _, uid := range []string{"i-orphan"} {
		_, _, err := store.GetInvite(ctx, uid)
		assert.Error(t, err, "an orphan is not copied")
	}
	_, _, err = store.GetDocumentMetadata(ctx, "c-deleted", "d-orphan")
	assert.Error(t, err)
}

func TestRunBackfill_ChildrenAgainChangeNothing(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := childSource()
	_, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)

	results, err := runBackfill(ctx, src, store, backfillOptions{})

	require.NoError(t, err)
	for _, entity := range childEntities {
		r := result(t, results, entity)
		assert.EqualValues(t, 1, r.Unchanged, entity)
		assert.Zero(t, r.Written, entity)
	}
}

func TestRunBackfill_ChildrenKVAheadUpdatesAndPostgreSQLAheadIsReported(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := childSource()
	_, err := runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)

	// Every live record changes in KV, at a higher revision.
	src.invites[0] = rec(src.invites[0].record, 140)
	src.invites[0].record.Status = "accepted"
	src.applications[0] = rec(src.applications[0].record, 150)
	src.applications[0].record.Message = "Updated"
	src.links[0] = rec(src.links[0].record, 160)
	src.links[0].record.Name = "Renamed"
	src.folders[0] = rec(src.folders[0].record, 170)
	src.folders[0].record.Name = "Renamed"
	src.documents[0] = rec(src.documents[0].record, 180)
	src.documents[0].record.Description = "Updated"
	src.briefs[0] = kvBrief(src.briefs[0].record, 190)
	src.briefs[0].record.BriefText = "Updated"
	src.throttles[0] = kvThrottle(src.throttles[0].record, 200)
	src.throttles[0].record.GeneratesUsed = 5

	results, err := runBackfill(ctx, src, store, backfillOptions{})

	require.NoError(t, err)
	for _, entity := range childEntities {
		assert.EqualValues(t, 1, result(t, results, entity).Written, entity)
	}
	assert.Equal(t, map[string]uint64{
		entityInvites: 140, entityApplications: 150, entityLinks: 160, entityFolders: 170, entityDocuments: 180, entityBriefs: 190, entityThrottles: 200,
	}, pgRevisions(ctx, t, store))
	invite, _, err := store.GetInvite(ctx, "i-1")
	require.NoError(t, err)
	assert.EqualValues(t, "accepted", invite.Status)
	link, _, err := store.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	assert.Equal(t, "Renamed", link.Name)
	brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "c-1", *storetest.NewBrief("", "c-1", storetest.FixtureWindow))
	require.NoError(t, err)
	assert.Equal(t, "Updated", brief.BriefText)
	assert.True(t, storetest.NewBrief("", "", storetest.FixtureWindow).UpdatedAt.Equal(brief.UpdatedAt), "the update path keeps the KV update time too")
	throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "c-1", storetest.FixtureWindow)
	require.NoError(t, err)
	assert.EqualValues(t, 5, throttle.GeneratesUsed)

	// KV now holds an older revision than PostgreSQL: nothing is overwritten, and each is reported.
	behind := childSource()
	results, err = runBackfill(ctx, behind, store, backfillOptions{})
	require.Error(t, err)
	for _, entity := range childEntities {
		r := result(t, results, entity)
		assert.EqualValues(t, 1, r.Errors, entity)
		assert.Zero(t, r.Written, entity)
		require.NotEmpty(t, r.Problems, entity)
		assert.Contains(t, r.Problems, backfillProblem{UID: r.Problems[len(r.Problems)-1].UID, Kind: "ahead", Message: "PostgreSQL has a newer revision than KV"}, entity)
	}
	assert.Equal(t, 200, int(pgRevisions(ctx, t, store)[entityThrottles]), "PostgreSQL was left as it was")
	invite, _, err = store.GetInvite(ctx, "i-1")
	require.NoError(t, err)
	assert.EqualValues(t, "accepted", invite.Status)
}

func TestRunBackfill_ChildrenDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, childSource(), store, backfillOptions{DryRun: true})

	require.NoError(t, err)
	for _, entity := range childEntities {
		assert.EqualValues(t, 1, result(t, results, entity).Written, "%s: what it would write", entity)
	}
	assert.Equal(t, map[string]uint64{
		entityInvites: 0, entityApplications: 0, entityLinks: 0, entityFolders: 0, entityDocuments: 0, entityBriefs: 0, entityThrottles: 0,
	}, pgRevisions(ctx, t, store))

	// Over rows KV has moved past, a dry run says it would update them and leaves them as they are.
	src := childSource()
	_, err = runBackfill(ctx, src, store, backfillOptions{})
	require.NoError(t, err)
	src.invites[0] = rec(src.invites[0].record, 140)
	src.briefs[0] = kvBrief(src.briefs[0].record, 190)
	src.throttles[0] = kvThrottle(src.throttles[0].record, 200)
	results, err = runBackfill(ctx, src, store, backfillOptions{DryRun: true})
	require.NoError(t, err)
	for _, entity := range []string{entityInvites, entityBriefs, entityThrottles} {
		assert.EqualValues(t, 1, result(t, results, entity).Written, entity)
	}
	revisions := pgRevisions(ctx, t, store)
	assert.EqualValues(t, 40, revisions[entityInvites])
	assert.EqualValues(t, 90, revisions[entityBriefs])
	assert.EqualValues(t, 100, revisions[entityThrottles])
}

func TestRunBackfill_ChildrenIncludeOrphansCopiesThem(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, childSource(), store, backfillOptions{IncludeOrphans: true})

	require.NoError(t, err)
	for _, entity := range childEntities {
		r := result(t, results, entity)
		assert.EqualValues(t, 2, r.Written, entity)
		assert.Zero(t, r.Orphans, entity)
	}
	_, revision, err := store.GetInvite(ctx, "i-orphan")
	require.NoError(t, err)
	assert.EqualValues(t, 41, revision)
}

func TestRunBackfill_ReportsWhatPostgreSQLRefusesForEachEntity(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := &fakeSource{committees: []port.CommitteeExport{kvCommittee("c-1", "Committee One", 1, 2)}}
	// Each pair breaks a unique index the schema has and KV did not: the same email in another case, the same name,
	// or the same window with another uid (at a lower revision, which must not be mistaken for a row that is ahead).
	src.invites = []childRecord[*model.CommitteeInvite]{
		rec(storetest.NewInvite("i-a", "c-1", "first.last@example.com"), 10), rec(storetest.NewInvite("i-b", "c-1", "First.Last@Example.com"), 11)}
	src.applications = []childRecord[*model.CommitteeApplication]{
		rec(storetest.NewApplication("a-a", "c-1", "first.last@example.com"), 20), rec(storetest.NewApplication("a-b", "c-1", "First.Last@Example.com"), 21)}
	src.documents = []childRecord[*model.CommitteeDocument]{
		rec(storetest.NewDocument("d-a", "c-1", "Minutes"), 30), rec(storetest.NewDocument("d-b", "c-1", "Minutes"), 31)}
	src.briefs = []childRecord[*model.GroupWeeklyBrief]{
		kvBrief(storetest.NewBrief("b-a", "c-1", storetest.FixtureWindow), 40), kvBrief(storetest.NewBrief("b-b", "c-1", storetest.FixtureWindow), 35)}

	results, err := runBackfill(ctx, src, store, backfillOptions{Workers: 1})

	require.Error(t, err)
	for entity, uid := range map[string]string{entityInvites: "i-b", entityApplications: "a-b", entityDocuments: "d-b", entityBriefs: "b-b"} {
		r := result(t, results, entity)
		assert.EqualValues(t, 1, r.Written, entity)
		require.Len(t, r.Problems, 1, entity)
		assert.Equal(t, uid, r.Problems[0].UID, entity)
		assert.Equal(t, "constraint", r.Problems[0].Kind, entity)
		assert.NotContains(t, r.Problems[0].Message, "example.com", "%s: the problem names the record by uid, not by the personal data in it", entity)
	}
}

func TestRunBackfill_WhenTheCommitteesCannotBeListedNoChildIsCopied(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := childSource()
	src.listErr = errors.New("nats down")

	results, err := runBackfill(ctx, src, store, backfillOptions{Entities: []string{entityInvites, entityBriefs}})

	require.Error(t, err)
	for _, entity := range []string{entityInvites, entityBriefs} {
		r := result(t, results, entity)
		assert.Equal(t, []backfillProblem{{Kind: "source", Message: "nats down"}}, r.Problems, entity)
		assert.Zero(t, r.Scanned, entity, "nothing is copied without the committees to check against")
	}
}

// racingSink is a store whose weekly-brief writes lose to another writer: the retryable error the store gives for a
// taken window or a lost swap.
type racingSink struct{ *postgres.Store }

func (racingSink) PutGroupWeeklyBrief(context.Context, *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	return nil, errs.NewServiceUnavailable("weekly brief CAS conflict — retry", nil)
}

func (racingSink) PutGroupWeeklyBriefThrottle(context.Context, *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	return nil, errs.NewServiceUnavailable("weekly-brief throttle CAS conflict — retry", nil)
}

func TestRunBackfill_ALostBriefWriteIsReportedAsAConstraintWithItsKey(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)

	results, err := runBackfill(ctx, childSource(), racingSink{store}, backfillOptions{Entities: []string{entityBriefs, entityThrottles}})

	require.Error(t, err)
	briefs, throttles := result(t, results, entityBriefs), result(t, results, entityThrottles)
	assert.Equal(t, "b-1", briefs.Problems[len(briefs.Problems)-1].UID)
	for _, r := range []backfillResult{briefs, throttles} {
		assert.EqualValues(t, 1, r.Errors, r.Entity)
		assert.Zero(t, r.Written, r.Entity)
	}
	assert.Equal(t, backfillProblem{UID: "b-1", Kind: "constraint", Message: "the weekly brief window is held by another record or was changed during the run"}, briefs.Problems[len(briefs.Problems)-1])
	assert.Equal(t, "c-1.20260104", throttles.Problems[len(throttles.Problems)-1].UID, "a throttle entry is named by its committee and window")
	assert.Equal(t, "constraint", throttles.Problems[len(throttles.Problems)-1].Kind)
}

func TestRunBackfill_TheCommitteesAreListedOnceForAllTheChildren(t *testing.T) {
	ctx := context.Background()
	store := postgrestest.NewStore(t)
	src := childSource()

	_, err := runBackfill(ctx, src, store, backfillOptions{})

	require.NoError(t, err)
	assert.Equal(t, 1, src.listCalls)
}
