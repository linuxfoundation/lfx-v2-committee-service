// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// testApplication returns an application with every field set.
func testApplication(uid, committeeUID, email string) *model.CommitteeApplication {
	return &model.CommitteeApplication{
		UID:               uid,
		CommitteeUID:      committeeUID,
		ApplicantEmail:    email,
		ApplicantUsername: "first-last-" + uid,
		Message:           "Please let me join",
		Status:            "pending",
		ReviewerNotes:     "Looks good",
		CreatedAt:         time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Organization:      &model.CommitteeMemberOrganization{ID: "org-1", Name: "Example Org", Website: "https://example.com"},
	}
}

func applicationUIDs(applications []*model.CommitteeApplication) []string {
	uids := make([]string, 0, len(applications))
	for _, a := range applications {
		uids = append(uids, a.UID)
	}
	return uids
}

// seedApplications returns a store with two applications in committee-1 and one in committee-2.
func seedApplications(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	for _, a := range []*model.CommitteeApplication{
		testApplication("a-1", "committee-1", "first.last@example.com"),
		testApplication("a-2", "committee-1", "first.last2@example.com"),
		testApplication("a-3", "committee-2", "first.last@example.com"),
	} {
		require.NoError(t, store.CreateApplication(context.Background(), a))
	}
	return store
}

func TestStore_CreateApplication(t *testing.T) {
	ctx := context.Background()
	store := seedApplications(t)

	t.Run("round-trips every field", func(t *testing.T) {
		want := testApplication("a-new", "committee-1", "first.last3@example.com")
		require.NoError(t, store.CreateApplication(ctx, want))
		got, revision, err := store.GetApplication(ctx, "a-new")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, want, got)
	})

	t.Run("an application with no organization round-trips as empty", func(t *testing.T) {
		want := testApplication("a-bare", "committee-1", "first.last4@example.com")
		want.Organization = nil
		require.NoError(t, store.CreateApplication(ctx, want))
		got, _, err := store.GetApplication(ctx, "a-bare")
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("an email already applied from is a conflict, whatever its case or spacing", func(t *testing.T) {
		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com\t"} {
			requireConflict(t, store.CreateApplication(ctx, testApplication("a-dup", "committee-1", email)))
		}
		_, _, err := store.GetApplication(ctx, "a-dup")
		requireNotFound(t, err)
	})

	t.Run("the same email in another committee is allowed", func(t *testing.T) {
		require.NoError(t, store.CreateApplication(ctx, testApplication("a-other", "committee-3", "first.last@example.com")))
	})

	t.Run("the same uid is a conflict and a nil application is invalid", func(t *testing.T) {
		requireConflict(t, store.CreateApplication(ctx, testApplication("a-2", "committee-9", "first.last9@example.com")))
		var validation errs.Validation
		require.ErrorAs(t, store.CreateApplication(ctx, nil), &validation)
	})
}

func TestStore_GetApplication(t *testing.T) {
	ctx := context.Background()
	store := seedApplications(t)

	application, _, err := store.GetApplication(ctx, "a-1")
	require.NoError(t, err)
	assert.Equal(t, "first.last@example.com", application.ApplicantEmail)

	_, _, err = store.GetApplication(ctx, "missing")
	requireNotFound(t, err)

	var validation errs.Validation
	_, _, err = store.GetApplication(ctx, "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListApplications(t *testing.T) {
	ctx := context.Background()
	store := seedApplications(t)

	applications, err := store.ListApplications(ctx, "committee-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"a-1", "a-2"}, applicationUIDs(applications))

	applications, err = store.ListApplications(ctx, "no-such-committee")
	require.NoError(t, err)
	assert.Empty(t, applications)
}

func TestStore_ListAllApplications(t *testing.T) {
	applications, err := seedApplications(t).ListAllApplications(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"a-1", "a-2", "a-3"}, applicationUIDs(applications))
}

func TestStore_UpdateApplication(t *testing.T) {
	ctx := context.Background()
	store := seedApplications(t)

	current := func(t *testing.T, uid string) (*model.CommitteeApplication, uint64) {
		t.Helper()
		application, revision, err := store.GetApplication(ctx, uid)
		require.NoError(t, err)
		return application, revision
	}

	t.Run("updates the application and replaces the revision", func(t *testing.T) {
		application, revision := current(t, "a-1")
		application.Status = "approved"
		require.NoError(t, store.UpdateApplication(ctx, application, revision))
		got, newRevision := current(t, "a-1")
		assert.Equal(t, "approved", got.Status)
		assert.NotEqual(t, revision, newRevision)

		var updatedAt time.Time
		require.NoError(t, store.pool.QueryRow(ctx, "SELECT updated_at FROM committee_applications WHERE uid = 'a-1'").Scan(&updatedAt))
		assert.True(t, updatedAt.After(application.CreatedAt))
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		application, revision := current(t, "a-2")
		application.Status = "first writer"
		require.NoError(t, store.UpdateApplication(ctx, application, revision))

		application.Status = "second writer"
		requireConflict(t, store.UpdateApplication(ctx, application, revision))
		got, _ := current(t, "a-2")
		assert.Equal(t, "first writer", got.Status)
	})

	t.Run("a missing application is not found and a nil one is invalid", func(t *testing.T) {
		application, revision := current(t, "a-2")
		application.UID = "missing"
		requireNotFound(t, store.UpdateApplication(ctx, application, revision))

		var validation errs.Validation
		require.ErrorAs(t, store.UpdateApplication(ctx, nil, 1), &validation)
	})

	t.Run("taking another application's email in the committee is a conflict", func(t *testing.T) {
		application, revision := current(t, "a-2")
		application.ApplicantEmail = "First.Last@example.com"
		requireConflict(t, store.UpdateApplication(ctx, application, revision))
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, revision := current(t, "a-3")
		var wg sync.WaitGroup
		results := make([]error, 8)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				application := *original
				application.Status = fmt.Sprintf("writer %d", i)
				results[i] = store.UpdateApplication(ctx, &application, revision)
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

func TestStore_DeleteApplication(t *testing.T) {
	ctx := context.Background()
	store := seedApplications(t)

	application, _, err := store.GetApplication(ctx, "a-1")
	require.NoError(t, err)
	require.NoError(t, store.DeleteApplication(ctx, application))
	_, _, err = store.GetApplication(ctx, "a-1")
	requireNotFound(t, err)

	t.Run("the email is free again", func(t *testing.T) {
		require.NoError(t, store.CreateApplication(ctx, testApplication("a-again", "committee-1", "first.last@example.com")))
	})

	t.Run("an application that is already gone is not an error", func(t *testing.T) {
		require.NoError(t, store.DeleteApplication(ctx, application))
	})

	t.Run("a nil application is invalid", func(t *testing.T) {
		var validation errs.Validation
		require.ErrorAs(t, store.DeleteApplication(ctx, nil), &validation)
	})
}
