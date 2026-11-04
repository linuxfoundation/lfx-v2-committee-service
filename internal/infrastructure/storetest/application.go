// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

func applicationUIDs(applications []*model.CommitteeApplication) []string {
	uids := make([]string, 0, len(applications))
	for _, a := range applications {
		uids = append(uids, a.UID)
	}
	return uids
}

// seedApplications creates three applications: two in committee-1 and one in committee-2 from the first
// applicant.
func seedApplications(t *testing.T, store port.CommitteeApplicationWriter) {
	t.Helper()
	for _, a := range []*model.CommitteeApplication{
		NewApplication("a-1", "committee-1", "first.last@example.com"),
		NewApplication("a-2", "committee-1", "first.last2@example.com"),
		NewApplication("a-3", "committee-2", "first.last@example.com"),
	} {
		require.NoError(t, store.CreateApplication(context.Background(), a))
	}
}

// RunApplicationContract runs the committee application cases against the backend: create, get, update,
// delete, the applicant email uniqueness per committee and the listings.
func RunApplicationContract(t *testing.T, backend Backend, newStore CommitteeFactory) {
	t.Run(backend.Name, func(t *testing.T) { runApplicationContract(t, newStore) })
}

func runApplicationContract(t *testing.T, newStore CommitteeFactory) {
	ctx := context.Background()

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := NewApplication("a-1", "committee-1", "first.last@example.com")
		require.NoError(t, store.CreateApplication(ctx, want))
		got, revision, err := store.GetApplication(ctx, "a-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeApplication(want), normalizeApplication(got))
	})

	t.Run("an application with no organization reads back empty", func(t *testing.T) {
		store := newStore(t)
		want := NewApplication("a-bare", "committee-1", "first.last@example.com")
		want.Organization = nil
		require.NoError(t, store.CreateApplication(ctx, want))
		got, _, err := store.GetApplication(ctx, "a-bare")
		require.NoError(t, err)
		assert.Equal(t, normalizeApplication(want), normalizeApplication(got))
		assert.Nil(t, got.Organization)
	})

	t.Run("a missing application is not found, and an empty uid is invalid", func(t *testing.T) {
		store := newStore(t)
		_, _, err := store.GetApplication(ctx, "missing")
		requireNotFound(t, err)
		_, _, err = store.GetApplication(ctx, "")
		requireValidation(t, err)
	})

	t.Run("create rejects a nil application and missing uids", func(t *testing.T) {
		store := newStore(t)
		requireValidation(t, store.CreateApplication(ctx, nil))
		requireValidation(t, store.CreateApplication(ctx, NewApplication("", "committee-1", "first.last@example.com")))
		requireValidation(t, store.CreateApplication(ctx, NewApplication("a-x", "", "first.last@example.com")))
	})

	t.Run("an applicant email is unique per committee, whatever its case, spacing or status", func(t *testing.T) {
		store := newStore(t)
		seedApplications(t, store)
		rejected, revision, err := store.GetApplication(ctx, "a-1")
		require.NoError(t, err)
		rejected.Status = "rejected"
		require.NoError(t, store.UpdateApplication(ctx, rejected, revision))

		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com\t"} {
			requireConflict(t, store.CreateApplication(ctx, NewApplication("a-dup", "committee-1", email)))
		}
		_, _, err = store.GetApplication(ctx, "a-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateApplication(ctx, NewApplication("a-other", "committee-3", "first.last@example.com")), "the same email in another committee")
	})

	t.Run("the same uid is a conflict, whatever else the second application holds", func(t *testing.T) {
		store := newStore(t)
		seedApplications(t, store)
		requireConflict(t, store.CreateApplication(ctx, NewApplication("a-1", "committee-9", "first.last9@example.com")))
		// A rejected create reserves nothing: the same applicant can still apply to that committee.
		require.NoError(t, store.CreateApplication(ctx, NewApplication("a-new", "committee-9", "first.last9@example.com")))

		// The same committee and email as the application that holds the uid, but other content: it must not
		// be mistaken for a create that already landed.
		impostor := NewApplication("a-1", "committee-1", " First.Last@example.com ")
		impostor.Message = "Impostor"
		requireConflict(t, store.CreateApplication(ctx, impostor))
		got, _, err := store.GetApplication(ctx, "a-1")
		require.NoError(t, err)
		assert.Equal(t, "Please let me join", got.Message)
	})

	t.Run("ListApplications lists the committee's applications, and ListAllApplications every one", func(t *testing.T) {
		store := newStore(t)
		applications, err := store.ListAllApplications(ctx)
		require.NoError(t, err)
		assert.Empty(t, applications)

		seedApplications(t, store)
		applications, err = store.ListApplications(ctx, "committee-1")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"a-1", "a-2"}, applicationUIDs(applications))
		applications, err = store.ListApplications(ctx, "no-such-committee")
		require.NoError(t, err)
		assert.Empty(t, applications)
		applications, err = store.ListAllApplications(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"a-1", "a-2", "a-3"}, applicationUIDs(applications))
	})

	t.Run("UpdateApplication replaces the application and the revision", func(t *testing.T) {
		store := newStore(t)
		seedApplications(t, store)
		application, revision, err := store.GetApplication(ctx, "a-1")
		require.NoError(t, err)
		application.Status = "approved"
		require.NoError(t, store.UpdateApplication(ctx, application, revision))

		got, newRevision, err := store.GetApplication(ctx, "a-1")
		require.NoError(t, err)
		assert.Equal(t, "approved", got.Status)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("UpdateApplication with a stale revision fails and changes nothing", func(t *testing.T) {
		store := newStore(t)
		seedApplications(t, store)
		application, revision, err := store.GetApplication(ctx, "a-2")
		require.NoError(t, err)
		application.Status = "first writer"
		require.NoError(t, store.UpdateApplication(ctx, application, revision))

		application.Status = "second writer"
		requireConflict(t, store.UpdateApplication(ctx, application, revision))
		got, _, err := store.GetApplication(ctx, "a-2")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.Status)
	})

	t.Run("UpdateApplication of a missing application is not found, and a nil one is invalid", func(t *testing.T) {
		store := newStore(t)
		requireNotFound(t, store.UpdateApplication(ctx, NewApplication("ghost", "committee-1", "first.last@example.com"), 1))
		requireValidation(t, store.UpdateApplication(ctx, nil, 1))
	})

	t.Run("DeleteApplication removes the application and frees the email, and is not an error when it is gone", func(t *testing.T) {
		store := newStore(t)
		seedApplications(t, store)
		application, _, err := store.GetApplication(ctx, "a-1")
		require.NoError(t, err)

		require.NoError(t, store.DeleteApplication(ctx, application))
		_, _, err = store.GetApplication(ctx, "a-1")
		requireNotFound(t, err)
		applications, err := store.ListApplications(ctx, "committee-1")
		require.NoError(t, err)
		assert.Equal(t, []string{"a-2"}, applicationUIDs(applications))

		require.NoError(t, store.DeleteApplication(ctx, application), "a delete of an application that is gone")
		requireValidation(t, store.DeleteApplication(ctx, nil))
		require.NoError(t, store.CreateApplication(ctx, NewApplication("a-4", "committee-1", "first.last@example.com")), "the email is free again")
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := newStore(t)
		seedApplications(t, store)
		application, revision, err := store.GetApplication(ctx, "a-3")
		require.NoError(t, err)

		wins := race(t, func(writer int) error {
			update := *application
			update.Status = fmt.Sprintf("writer %d", writer)
			return store.UpdateApplication(ctx, &update, revision)
		}, requireConflict)
		assert.Equal(t, 1, wins)
	})
}
