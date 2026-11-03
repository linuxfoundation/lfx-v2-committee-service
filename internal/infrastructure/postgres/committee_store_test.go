// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// newTestStore returns a Store on a pool bound to a freshly migrated schema.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := migratedDB(t)
	cfg, err := pgxpool.ParseConfig(testDSN(t))
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = currentSchema(t, db)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return NewStore(pool)
}

func strPtr(s string) *string { return &s }

// testCommittee returns a committee with every field set, so a round trip shows any column that is
// not stored or read back.
func testCommittee(uid, project, name string) *model.Committee {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:              uid,
			ProjectUID:       project,
			ProjectName:      "Project Name",
			ProjectSlug:      "project-slug",
			Name:             name,
			Category:         "Technical Steering Committee",
			Description:      "A committee",
			Website:          strPtr("https://example.com"),
			MailingList:      strPtr("first-last@example.com"),
			ChatChannel:      strPtr("#first-last"),
			EnableVoting:     true,
			SSOGroupEnabled:  true,
			SSOGroupName:     "sso-" + uid,
			RequiresReview:   true,
			Public:           true,
			JoinMode:         "invite_only",
			Calendar:         model.Calendar{Public: true},
			DisplayName:      "Display Name",
			ParentUID:        strPtr("parent-uid"),
			Repository:       strPtr("https://example.com/repo"),
			Scope:            []string{"scope-a", "scope-b"},
			Deliverables:     []string{"deliverable"},
			KeyDates:         []model.KeyDate{{Date: "2026-06-01", Label: "Kickoff"}},
			ExternalSources:  []model.ExternalSource{{Provider: "provider", EntityType: "group", Label: "Label", URL: "https://example.com/source", ExternalID: "id"}},
			Charter:          &model.Charter{URL: "https://example.com/charter", Version: 3, UpdatedAt: created, UpdatedBy: &model.CommitteeUser{Name: "First Last", Username: "first-last", Email: "first.last@example.com"}},
			TotalMembers:     4,
			TotalVotingRepos: 2,
			HasMailingList:   true,
			CreatedAt:        created,
			UpdatedAt:        created.Add(time.Hour),
		},
		CommitteeSettings: &model.CommitteeSettings{
			BusinessEmailRequired: true,
			ShowMeetingAttendees:  true,
			MemberVisibility:      "basic_profile",
			ChatWebhookURL:        strPtr("https://example.com/hook"),
			HasChatWebhook:        true,
			LastReviewedAt:        strPtr("2026-05-01"),
			LastReviewedBy:        strPtr("first-last"),
			Writers:               []model.CommitteeUser{{Name: "First Last", Username: "first-last", Email: "first.last@example.com"}},
			Auditors:              []model.CommitteeUser{{Name: "First Last", Username: "first-last-2", Email: "first.last2@example.com"}},
			CreatedAt:             created,
			UpdatedAt:             created.Add(time.Hour),
		},
	}
}

func requireConflict(t *testing.T, err error) {
	t.Helper()
	var conflict errs.Conflict
	require.ErrorAs(t, err, &conflict)
}

func requireNotFound(t *testing.T, err error) {
	t.Helper()
	var notFound errs.NotFound
	require.ErrorAs(t, err, &notFound)
}

func TestStoreCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	want := testCommittee("uid-1", "project-1", "Committee One")
	require.NoError(t, store.Create(ctx, want))

	base, revision, err := store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.NotZero(t, revision)
	assert.Equal(t, want.CommitteeBase, *base)

	settings, settingsRevision, err := store.GetSettings(ctx, "uid-1")
	require.NoError(t, err)
	assert.NotZero(t, settingsRevision)
	assert.Equal(t, "uid-1", settings.UID, "settings take the committee uid")
	assert.Equal(t, *want.CommitteeSettings, *settings)

	t.Run("a committee with no optional fields round-trips as empty", func(t *testing.T) {
		minimal := &model.Committee{CommitteeBase: model.CommitteeBase{
			UID: "uid-min", ProjectUID: "project-1", Name: "Minimal", CreatedAt: want.CommitteeBase.CreatedAt, UpdatedAt: want.CommitteeBase.UpdatedAt,
		}}
		require.NoError(t, store.Create(ctx, minimal))
		got, _, err := store.GetBase(ctx, "uid-min")
		require.NoError(t, err)
		assert.Equal(t, minimal.CommitteeBase, *got)
		_, _, err = store.GetSettings(ctx, "uid-min")
		requireNotFound(t, err)
	})
}

func TestStoreCreateConflicts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, testCommittee("uid-1", "project-1", "Committee One")))

	t.Run("the same name in the same project", func(t *testing.T) {
		dup := testCommittee("uid-2", "project-1", "Committee One")
		dup.SSOGroupName = "sso-other"
		err := store.Create(ctx, dup)
		requireConflict(t, err)
		assert.NotErrorIs(t, err, errs.ErrSSOGroupNameTaken)
		_, _, errGet := store.GetBase(ctx, "uid-2")
		requireNotFound(t, errGet)
	})

	t.Run("the same name in another project is allowed", func(t *testing.T) {
		other := testCommittee("uid-3", "project-2", "Committee One")
		require.NoError(t, store.Create(ctx, other))
	})

	t.Run("the same SSO group name", func(t *testing.T) {
		dup := testCommittee("uid-5", "project-1", "Committee Five")
		dup.SSOGroupName = "sso-uid-1"
		err := store.Create(ctx, dup)
		requireConflict(t, err)
		assert.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)
	})

	t.Run("a failed settings insert leaves no committee behind", func(t *testing.T) {
		// Settings left over from earlier data (an orphan) make the second insert fail.
		_, err := store.pool.Exec(ctx, `INSERT INTO committee_settings (committee_uid, created_at, updated_at) VALUES ('uid-orphan', now(), now())`)
		require.NoError(t, err)

		err = store.Create(ctx, testCommittee("uid-orphan", "project-1", "Orphaned"))
		requireConflict(t, err)
		_, _, errGet := store.GetBase(ctx, "uid-orphan")
		requireNotFound(t, errGet)
		_, errFind := store.FindUIDByProjectAndName(ctx, "project-1", "Orphaned")
		requireNotFound(t, errFind)
	})
}

func TestStoreReads(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, testCommittee("uid-b", "project-1", "Beta")))
	require.NoError(t, store.Create(ctx, testCommittee("uid-a", "project-1", "Alpha")))

	t.Run("a missing committee is not found", func(t *testing.T) {
		_, _, err := store.GetBase(ctx, "missing")
		requireNotFound(t, err)
		_, _, err = store.GetSettings(ctx, "missing")
		requireNotFound(t, err)
	})

	t.Run("ListAllUIDs", func(t *testing.T) {
		uids, err := store.ListAllUIDs(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"uid-a", "uid-b"}, uids)
	})

	t.Run("FindUIDByProjectAndName is exact", func(t *testing.T) {
		uid, err := store.FindUIDByProjectAndName(ctx, "project-1", "Alpha")
		require.NoError(t, err)
		assert.Equal(t, "uid-a", uid)

		for _, tc := range []struct{ project, name string }{
			{"project-1", "alpha"}, {"project-2", "Alpha"}, {"project-1", "Gamma"},
		} {
			_, err := store.FindUIDByProjectAndName(ctx, tc.project, tc.name)
			requireNotFound(t, err)
		}
	})
}

func TestStoreUpdateBase(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, testCommittee("uid-1", "project-1", "Committee One")))
	require.NoError(t, store.Create(ctx, testCommittee("uid-2", "project-1", "Committee Two")))

	current := func(t *testing.T, uid string) (*model.Committee, uint64) {
		t.Helper()
		base, revision, err := store.GetBase(ctx, uid)
		require.NoError(t, err)
		return &model.Committee{CommitteeBase: *base}, revision
	}

	t.Run("updates the base and replaces the revision", func(t *testing.T) {
		committee, revision := current(t, "uid-1")
		committee.Description = "changed"
		committee.Scope = nil
		require.NoError(t, store.UpdateBase(ctx, committee, revision))

		got, newRevision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "changed", got.Description)
		assert.Nil(t, got.Scope)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		committee, revision := current(t, "uid-1")
		committee.Description = "first writer"
		require.NoError(t, store.UpdateBase(ctx, committee, revision))

		committee.Description = "second writer"
		requireConflict(t, store.UpdateBase(ctx, committee, revision))
		got, _ := current(t, "uid-1")
		assert.Equal(t, "first writer", got.Description)
	})

	t.Run("a missing committee is not found", func(t *testing.T) {
		committee, revision := current(t, "uid-1")
		committee.CommitteeBase.UID = "missing"
		requireNotFound(t, store.UpdateBase(ctx, committee, revision))
	})

	t.Run("renaming onto an existing name is a conflict and changes nothing", func(t *testing.T) {
		committee, revision := current(t, "uid-2")
		committee.Name = "Committee One"
		requireConflict(t, store.UpdateBase(ctx, committee, revision))
		got, gotRevision := current(t, "uid-2")
		assert.Equal(t, "Committee Two", got.Name)
		assert.Equal(t, revision, gotRevision)
	})

	t.Run("taking another committee's SSO group name is a conflict", func(t *testing.T) {
		committee, revision := current(t, "uid-2")
		committee.SSOGroupName = "sso-uid-1"
		err := store.UpdateBase(ctx, committee, revision)
		requireConflict(t, err)
		assert.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)
	})

	t.Run("the old name is free after a rename", func(t *testing.T) {
		committee, revision := current(t, "uid-2")
		committee.Name = "Renamed"
		require.NoError(t, store.UpdateBase(ctx, committee, revision))

		_, err := store.FindUIDByProjectAndName(ctx, "project-1", "Committee Two")
		requireNotFound(t, err)
		reuse := testCommittee("uid-3", "project-1", "Committee Two")
		require.NoError(t, store.Create(ctx, reuse))
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		_, revision := current(t, "uid-3")
		var wg sync.WaitGroup
		results := make([]error, 8)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				committee, _ := current(t, "uid-3")
				committee.Description = fmt.Sprintf("writer %d", i)
				results[i] = store.UpdateBase(ctx, committee, revision)
			}()
		}
		wg.Wait()

		wins := 0
		for _, err := range results {
			if err == nil {
				wins++
				continue
			}
			requireConflict(t, err)
		}
		assert.Equal(t, 1, wins)
	})
}

func TestStoreUpdateSetting(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, testCommittee("uid-1", "project-1", "Committee One")))

	t.Run("updates the settings and replaces the revision", func(t *testing.T) {
		settings, revision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		settings.MemberVisibility = "hidden"
		settings.Writers = nil
		require.NoError(t, store.UpdateSetting(ctx, settings, revision))

		got, newRevision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "hidden", got.MemberVisibility)
		assert.Nil(t, got.Writers)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		settings, revision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		settings.MemberVisibility = "first writer"
		require.NoError(t, store.UpdateSetting(ctx, settings, revision))

		settings.MemberVisibility = "second writer"
		requireConflict(t, store.UpdateSetting(ctx, settings, revision))
		got, _, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.MemberVisibility)
	})

	t.Run("missing settings are not found", func(t *testing.T) {
		settings, revision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		settings.UID = "missing"
		requireNotFound(t, store.UpdateSetting(ctx, settings, revision))
	})
}

func TestStoreDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.Create(ctx, testCommittee("uid-1", "project-1", "Committee One")))

	t.Run("a stale revision is a conflict and deletes nothing", func(t *testing.T) {
		_, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		requireConflict(t, store.Delete(ctx, "uid-1", revision+1))
		_, _, err = store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		_, _, err = store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
	})

	t.Run("deletes the committee and its settings and frees its names", func(t *testing.T) {
		base, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		require.NoError(t, store.Delete(ctx, "uid-1", revision))

		_, _, err = store.GetBase(ctx, "uid-1")
		requireNotFound(t, err)
		_, _, err = store.GetSettings(ctx, "uid-1")
		requireNotFound(t, err)

		again := testCommittee("uid-2", base.ProjectUID, base.Name)
		again.SSOGroupName = base.SSOGroupName
		require.NoError(t, store.Create(ctx, again))
	})

	t.Run("a missing committee is not found", func(t *testing.T) {
		requireNotFound(t, store.Delete(ctx, "missing", 1))
	})
}

func TestStoreIsReady(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.IsReady(context.Background()))

	store.pool.Close()
	assert.Error(t, store.IsReady(context.Background()))
}
