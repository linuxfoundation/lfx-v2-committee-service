// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

func inviteUIDs(invites []*model.CommitteeInvite) []string {
	uids := make([]string, 0, len(invites))
	for _, i := range invites {
		uids = append(uids, i.UID)
	}
	return uids
}

// seedInvites creates three invites: two in committee-1 and one in committee-2 for the first invitee.
func seedInvites(t *testing.T, store port.CommitteeInviteWriter) {
	t.Helper()
	for _, i := range []*model.CommitteeInvite{
		NewInvite("i-1", "committee-1", "first.last@example.com"),
		NewInvite("i-2", "committee-1", "first.last2@example.com"),
		NewInvite("i-3", "committee-2", "first.last@example.com"),
	} {
		require.NoError(t, store.CreateInvite(context.Background(), i))
	}
}

// RunInviteContract runs the committee invite cases against the backend: create, get, update, the
// email uniqueness per committee and the listings.
func RunInviteContract(t *testing.T, backend Backend, newStore CommitteeFactory) {
	t.Run(backend.Name, func(t *testing.T) { runInviteContract(t, newStore) })
}

func runInviteContract(t *testing.T, newStore CommitteeFactory) {
	ctx := context.Background()

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := NewInvite("i-1", "committee-1", "first.last@example.com")
		require.NoError(t, store.CreateInvite(ctx, want))
		got, revision, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeInvite(want), normalizeInvite(got))
	})

	t.Run("an invite with no organization, inviter or expiry reads back empty", func(t *testing.T) {
		store := newStore(t)
		want := NewInvite("i-bare", "committee-1", "first.last@example.com")
		want.Organization, want.Inviter, want.ExpiresAt = nil, nil, time.Time{}
		require.NoError(t, store.CreateInvite(ctx, want))
		got, _, err := store.GetInvite(ctx, "i-bare")
		require.NoError(t, err)
		assert.Equal(t, normalizeInvite(want), normalizeInvite(got))
		assert.Nil(t, got.Organization)
		assert.Nil(t, got.Inviter)
		assert.True(t, got.ExpiresAt.IsZero())
	})

	t.Run("a missing invite is not found, and an empty uid is invalid", func(t *testing.T) {
		store := newStore(t)
		_, _, err := store.GetInvite(ctx, "missing")
		requireNotFound(t, err)
		_, _, err = store.GetInvite(ctx, "")
		requireValidation(t, err)
	})

	t.Run("create rejects a nil invite and missing uids", func(t *testing.T) {
		store := newStore(t)
		requireValidation(t, store.CreateInvite(ctx, nil))
		requireValidation(t, store.CreateInvite(ctx, NewInvite("", "committee-1", "first.last@example.com")))
		requireValidation(t, store.CreateInvite(ctx, NewInvite("i-x", "", "first.last@example.com")))
	})

	t.Run("an invitee email is unique per committee, whatever its case, spacing or status", func(t *testing.T) {
		store := newStore(t)
		seedInvites(t, store)
		declined, revision, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		declined.Status = "declined"
		require.NoError(t, store.UpdateInvite(ctx, declined, revision))

		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com\t"} {
			requireConflict(t, store.CreateInvite(ctx, NewInvite("i-dup", "committee-1", email)))
		}
		_, _, err = store.GetInvite(ctx, "i-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateInvite(ctx, NewInvite("i-other", "committee-3", "first.last@example.com")), "the same email in another committee")
	})

	t.Run("the same uid is a conflict, whatever else the second invite holds", func(t *testing.T) {
		store := newStore(t)
		seedInvites(t, store)
		requireConflict(t, store.CreateInvite(ctx, NewInvite("i-1", "committee-9", "first.last9@example.com")))
		// A rejected create reserves nothing: the same invitee can still be invited to that committee.
		require.NoError(t, store.CreateInvite(ctx, NewInvite("i-new", "committee-9", "first.last9@example.com")))

		// The same committee and email as the invite that holds the uid, but other content: it must not be
		// mistaken for a create that already landed.
		impostor := NewInvite("i-1", "committee-1", " First.Last@example.com ")
		impostor.Role = "Impostor"
		requireConflict(t, store.CreateInvite(ctx, impostor))
		got, _, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		assert.Equal(t, "Member", got.Role)
	})

	t.Run("ListInvites lists the committee's invites, and ListAllInvites every invite", func(t *testing.T) {
		store := newStore(t)
		invites, err := store.ListAllInvites(ctx)
		require.NoError(t, err)
		assert.Empty(t, invites)

		seedInvites(t, store)
		invites, err = store.ListInvites(ctx, "committee-1")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"i-1", "i-2"}, inviteUIDs(invites))
		invites, err = store.ListInvites(ctx, "no-such-committee")
		require.NoError(t, err)
		assert.Empty(t, invites)
		invites, err = store.ListAllInvites(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"i-1", "i-2", "i-3"}, inviteUIDs(invites))
	})

	t.Run("UpdateInvite replaces the invite and the revision", func(t *testing.T) {
		store := newStore(t)
		seedInvites(t, store)
		invite, revision, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		invite.Status = "accepted"
		invite.Role = "Chair"
		invite.CommitteeName = "Renamed Committee"
		invite.OrganizationRequired = false
		invite.Organization = &model.CommitteeMemberOrganization{ID: "001A000001zzzzzAAA", Name: "Other Org", Website: "https://example.org"}
		invite.Inviter = &model.CommitteeUser{Name: "First Last", Username: "first-last-2", Email: "first.last2@example.com"}
		invite.ExpiresAt = invite.ExpiresAt.Add(24 * time.Hour)
		require.NoError(t, store.UpdateInvite(ctx, invite, revision))

		got, newRevision, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		assert.Equal(t, normalizeInvite(invite), normalizeInvite(got), "every edited field is stored")
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("UpdateInvite with a stale revision fails and changes nothing", func(t *testing.T) {
		store := newStore(t)
		seedInvites(t, store)
		invite, revision, err := store.GetInvite(ctx, "i-2")
		require.NoError(t, err)
		invite.Status = "first writer"
		require.NoError(t, store.UpdateInvite(ctx, invite, revision))

		invite.Status = "second writer"
		requireConflict(t, store.UpdateInvite(ctx, invite, revision))
		got, _, err := store.GetInvite(ctx, "i-2")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.Status)
	})

	t.Run("UpdateInvite of a missing invite is not found, and a nil invite is invalid", func(t *testing.T) {
		store := newStore(t)
		requireNotFound(t, store.UpdateInvite(ctx, NewInvite("ghost", "committee-1", "first.last@example.com"), 1))
		requireValidation(t, store.UpdateInvite(ctx, nil, 1))
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := newStore(t)
		seedInvites(t, store)
		invite, revision, err := store.GetInvite(ctx, "i-3")
		require.NoError(t, err)

		wins := race(t, func(writer int) error {
			update := *invite
			update.Status = fmt.Sprintf("writer %d", writer)
			return store.UpdateInvite(ctx, &update, revision)
		}, requireConflict)
		assert.Equal(t, 1, wins)
	})
}
