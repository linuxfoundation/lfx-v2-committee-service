// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// revisionCase describes one kind of record: how to create it, read its revision and update it, so
// the same checks run against every table. update writes with the revision the caller read.
type revisionCase struct {
	name     string
	create   func(t *testing.T, ctx context.Context, s *Store)
	revision func(t *testing.T, ctx context.Context, s *Store) uint64
	update   func(t *testing.T, ctx context.Context, s *Store, revision uint64) error
}

func mustWithRevision(t *testing.T, ctx context.Context, revision uint64) context.Context {
	t.Helper()
	ctx, err := WithRevision(ctx, revision)
	require.NoError(t, err)
	return ctx
}

func revisionCases() []revisionCase {
	window := testWindow
	return []revisionCase{
		{
			name: "committee",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "Committee One")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetBase(ctx, "c-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				base, _, err := s.GetBase(ctx, "c-1")
				require.NoError(t, err)
				return s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision)
			},
		},
		{
			name: "committee total_members counter",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "Committee One")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetBase(ctx, "c-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, _ uint64) error {
				base, _, err := s.GetBase(ctx, "c-1")
				require.NoError(t, err)
				_, _, err = s.UpdateTotalMembers(ctx, "c-1", base.TotalMembers+1)
				return err
			},
		},
		{
			name: "member",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.CreateMember(ctx, testMember("m-1", "c-1", "first.last@example.com")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetMember(ctx, "m-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				member, _, err := s.GetMember(ctx, "m-1")
				require.NoError(t, err)
				_, err = s.UpdateMember(ctx, member, revision)
				return err
			},
		},
		{
			name: "invite",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.CreateInvite(ctx, testInvite("i-1", "c-1", "first.last@example.com")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetInvite(ctx, "i-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				invite, _, err := s.GetInvite(ctx, "i-1")
				require.NoError(t, err)
				return s.UpdateInvite(ctx, invite, revision)
			},
		},
		{
			name: "application",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.CreateApplication(ctx, testApplication("a-1", "c-1", "first.last@example.com")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetApplication(ctx, "a-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				application, _, err := s.GetApplication(ctx, "a-1")
				require.NoError(t, err)
				return s.UpdateApplication(ctx, application, revision)
			},
		},
		{
			name: "link",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.CreateLink(ctx, testLink("l-1", "c-1")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetLink(ctx, "c-1", "l-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				link, _, err := s.GetLink(ctx, "c-1", "l-1")
				require.NoError(t, err)
				return s.UpdateLink(ctx, link, revision)
			},
		},
		{
			name: "folder",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.CreateLinkFolder(ctx, testFolder("f-1", "c-1", "Legal")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetLinkFolder(ctx, "c-1", "f-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				folder, _, err := s.GetLinkFolder(ctx, "c-1", "f-1")
				require.NoError(t, err)
				return s.UpdateLinkFolder(ctx, folder, revision)
			},
		},
		{
			name: "document",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				require.NoError(t, s.CreateDocumentMetadata(ctx, testDocument("d-1", "c-1", "Minutes")))
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				_, revision, err := s.GetDocumentMetadata(ctx, "c-1", "d-1")
				require.NoError(t, err)
				return revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				doc, _, err := s.GetDocumentMetadata(ctx, "c-1", "d-1")
				require.NoError(t, err)
				return s.UpdateDocumentMetadata(ctx, doc, revision)
			},
		},
		{
			name: "weekly brief",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				_, err := s.PutGroupWeeklyBrief(ctx, testBrief("b-1", "c-1", window))
				require.NoError(t, err)
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				brief, _, err := s.GetGroupWeeklyBriefForWindow(ctx, "c-1", model.GroupWeeklyBrief{WindowStart: window})
				require.NoError(t, err)
				return brief.Revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				brief, _, err := s.GetGroupWeeklyBriefForWindow(ctx, "c-1", model.GroupWeeklyBrief{WindowStart: window})
				require.NoError(t, err)
				brief.Revision = revision
				_, err = s.PutGroupWeeklyBrief(ctx, brief)
				return err
			},
		},
		{
			name: "weekly brief throttle",
			create: func(t *testing.T, ctx context.Context, s *Store) {
				_, err := s.PutGroupWeeklyBriefThrottle(ctx, testThrottle("c-1", window))
				require.NoError(t, err)
			},
			revision: func(t *testing.T, ctx context.Context, s *Store) uint64 {
				throttle, err := s.GetGroupWeeklyBriefThrottle(ctx, "c-1", window)
				require.NoError(t, err)
				return throttle.Revision
			},
			update: func(t *testing.T, ctx context.Context, s *Store, revision uint64) error {
				throttle, err := s.GetGroupWeeklyBriefThrottle(ctx, "c-1", window)
				require.NoError(t, err)
				throttle.Revision = revision
				_, err = s.PutGroupWeeklyBriefThrottle(ctx, throttle)
				return err
			},
		},
	}
}

func TestWithRevision(t *testing.T) {
	for _, c := range revisionCases() {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			store := newTestStore(t)

			c.create(t, mustWithRevision(t, ctx, 1000), store)
			assert.Equal(t, uint64(1000), c.revision(t, ctx, store), "a create stores the revision it is given")

			// The write is guarded by the revision that was read, and leaves the one it is given.
			require.NoError(t, c.update(t, mustWithRevision(t, ctx, 2000), store, 1000))
			assert.Equal(t, uint64(2000), c.revision(t, ctx, store), "an update stores the revision it is given")

			// A revision that is not above the one it replaces would keep a handed-out token valid for the
			// new content: it is rejected, whether it repeats the current revision (a context reused for a
			// second write) or goes below it, and nothing is written.
			var validation errs.Validation
			require.ErrorAs(t, c.update(t, mustWithRevision(t, ctx, 2000), store, 2000), &validation, "the same revision")
			require.ErrorAs(t, c.update(t, mustWithRevision(t, ctx, 1500), store, 2000), &validation, "a lower revision")
			assert.Equal(t, uint64(2000), c.revision(t, ctx, store))

			// Without the option the sequence decides again, and is not affected by the explicit values.
			require.NoError(t, c.update(t, ctx, store, 2000))
			fromSequence := c.revision(t, ctx, store)
			assert.NotEqual(t, uint64(2000), fromSequence)
			assert.Less(t, fromSequence, uint64(1000), "the sequence has not been advanced by explicit revisions")
		})
	}
}

func TestWithRevision_settings(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	withBase := mustWithRevision(t, ctx, 1000)
	withBoth, err := WithSettingsRevision(withBase, 3000)
	require.NoError(t, err)
	require.NoError(t, store.Create(withBoth, testCommittee("c-1", "project-1", "Committee One")))

	_, baseRevision, err := store.GetBase(ctx, "c-1")
	require.NoError(t, err)
	settings, settingsRevision, err := store.GetSettings(ctx, "c-1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1000), baseRevision, "the committee row takes WithRevision")
	assert.Equal(t, uint64(3000), settingsRevision, "the settings row takes WithSettingsRevision")

	// Updating the settings is a write of its own record, so WithRevision applies to it.
	require.NoError(t, store.UpdateSetting(mustWithRevision(t, ctx, 4000), settings, 3000))
	_, settingsRevision, err = store.GetSettings(ctx, "c-1")
	require.NoError(t, err)
	assert.Equal(t, uint64(4000), settingsRevision)
}

func TestWithRevision_invalid(t *testing.T) {
	var validation errs.Validation
	for _, revision := range []uint64{0, math.MaxInt64 + 1, math.MaxUint64} {
		_, err := WithRevision(context.Background(), revision)
		require.ErrorAs(t, err, &validation, "revision %d", revision)
		_, err = WithSettingsRevision(context.Background(), revision)
		require.ErrorAs(t, err, &validation, "settings revision %d", revision)
	}
	_, err := WithRevision(context.Background(), math.MaxInt64)
	require.NoError(t, err)
}

func TestStore_AdvanceRevisionSequence(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	next := func(uid string) uint64 {
		t.Helper()
		require.NoError(t, store.CreateLink(ctx, testLink(uid, "c-1")))
		_, revision, err := store.GetLink(ctx, "c-1", uid)
		require.NoError(t, err)
		return revision
	}

	assert.Less(t, next("l-1"), uint64(1000))
	require.NoError(t, store.AdvanceRevisionSequence(ctx, 5000))
	assert.Equal(t, uint64(5001), next("l-2"), "the next revision is above the highest one given")

	// A lower value never lowers the sequence, so a revision is not issued twice.
	require.NoError(t, store.AdvanceRevisionSequence(ctx, 10))
	assert.Equal(t, uint64(5002), next("l-3"))

	var validation errs.Validation
	require.ErrorAs(t, store.AdvanceRevisionSequence(ctx, math.MaxInt64+1), &validation)
}

// The Go check in front of a counter update is not atomic: the statement itself must refuse a revision
// that is not above the current one, so a write that raced past the check changes nothing.
func TestWithRevision_counterStatementRefusesLowerRevision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(mustWithRevision(t, ctx, 1000), testCommittee("c-1", "project-1", "Committee One")))

	err := store.pool.QueryRow(ctx, updateTotalMembers.update, "c-1", 7, time.Now(), int64(1000)).Scan(new(any), new(any))
	require.ErrorIs(t, err, pgx.ErrNoRows, "an equal revision matches no row")
	err = store.pool.QueryRow(ctx, updateTotalMembers.update, "c-1", 7, time.Now(), int64(999)).Scan(new(any), new(any))
	require.ErrorIs(t, err, pgx.ErrNoRows, "a lower revision matches no row")

	base, _, err := store.GetBase(ctx, "c-1")
	require.NoError(t, err)
	assert.NotEqual(t, 7, base.TotalMembers)
}

// A put without a revision replaces the brief with the same uid; the revision it is given is the one left behind.
func TestWithRevision_replaceByUID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_, err := store.PutGroupWeeklyBrief(ctx, testBrief("b-1", "c-1", testWindow))
	require.NoError(t, err)

	_, err = store.PutGroupWeeklyBrief(mustWithRevision(t, ctx, 3000), testBrief("b-1", "c-1", testWindow))
	require.NoError(t, err)
	brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "c-1", model.GroupWeeklyBrief{WindowStart: testWindow})
	require.NoError(t, err)
	assert.Equal(t, uint64(3000), brief.Revision)

	// A replacement must move the brief past the revision it replaces, or a token already handed out
	// would match the new content: the same or a lower revision is refused and nothing is written.
	var validation errs.Validation
	for _, revision := range []uint64{3000, 500} {
		replacement := testBrief("b-1", "c-1", testWindow)
		replacement.BriefText = "Must not be stored."
		_, err = store.PutGroupWeeklyBrief(mustWithRevision(t, ctx, revision), replacement)
		require.ErrorAs(t, err, &validation, "revision %d", revision)
	}
	brief, _, err = store.GetGroupWeeklyBriefForWindow(ctx, "c-1", model.GroupWeeklyBrief{WindowStart: testWindow})
	require.NoError(t, err)
	assert.Equal(t, uint64(3000), brief.Revision)
	assert.NotEqual(t, "Must not be stored.", brief.BriefText)

	// A higher one is accepted, and so is a replacement with no explicit revision at all.
	_, err = store.PutGroupWeeklyBrief(mustWithRevision(t, ctx, 3001), testBrief("b-1", "c-1", testWindow))
	require.NoError(t, err)
	_, err = store.PutGroupWeeklyBrief(ctx, testBrief("b-1", "c-1", testWindow))
	require.NoError(t, err)
}

// When a counter statement matches no row, the caller must be told why. The committee being missing,
// already having the value, and the statement refusing the revision (another write moved it after the
// check in Go) are three different answers; the last cannot be forced through the public methods, so the
// classification is tested here.
func TestStore_counterNotChanged(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(mustWithRevision(t, ctx, 1000), testCommittee("c-1", "project-1", "Committee One")))
	base, _, err := store.GetBase(ctx, "c-1")
	require.NoError(t, err)
	var validation errs.Validation

	t.Run("a missing committee is not found", func(t *testing.T) {
		_, _, err := store.counterNotChanged(ctx, "missing", updateTotalMembers, 5, "failed")
		requireNotFound(t, err)
	})

	t.Run("a column that already has the value is an unchanged no-op", func(t *testing.T) {
		got, changed, err := store.counterNotChanged(mustWithRevision(t, ctx, 900), "c-1", updateTotalMembers, base.TotalMembers, "failed")
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, got)
	})

	t.Run("a refused change with an explicit revision is an error, not a no-op", func(t *testing.T) {
		_, changed, err := store.counterNotChanged(mustWithRevision(t, ctx, 900), "c-1", updateTotalMembers, base.TotalMembers+1, "failed")
		require.ErrorAs(t, err, &validation)
		assert.False(t, changed)
	})

	t.Run("a refused change without one is a conflict", func(t *testing.T) {
		_, _, err := store.counterNotChanged(ctx, "c-1", updateTotalMembers, base.TotalMembers+1, "failed")
		requireConflict(t, err)
	})
}

// An explicit revision does not weaken the guard: a stale token is still rejected and nothing is written.
func TestWithRevision_staleStillRejected(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.CreateLink(mustWithRevision(t, ctx, 1000), testLink("l-1", "c-1")))

	link, _, err := store.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	requireConflict(t, store.UpdateLink(mustWithRevision(t, ctx, 2000), link, 999))
	_, revision, err := store.GetLink(ctx, "c-1", "l-1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1000), revision)
}

// The PostgreSQL store runs the same behavioral suites as the NATS KV store (see
// internal/infrastructure/nats/storage_test.go).
func TestContract_Committee(t *testing.T) {
	storetest.RunCommitteeContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) port.CommitteeReaderWriter {
		return newTestStore(t)
	})
}

func TestContract_Members(t *testing.T) {
	storetest.RunMemberContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) port.CommitteeReaderWriter {
		return newTestStore(t)
	})
}

func TestContract_Invites(t *testing.T) {
	storetest.RunInviteContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) port.CommitteeReaderWriter {
		return newTestStore(t)
	})
}

func TestContract_Applications(t *testing.T) {
	storetest.RunApplicationContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) port.CommitteeReaderWriter {
		return newTestStore(t)
	})
}

func TestContract_Links(t *testing.T) {
	storetest.RunLinkContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) storetest.LinkStore {
		return newTestStore(t)
	})
}

func TestContract_LinkFolders(t *testing.T) {
	storetest.RunFolderContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) storetest.LinkStore {
		return newTestStore(t)
	})
}
