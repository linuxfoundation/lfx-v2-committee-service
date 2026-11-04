// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func memberUIDs(members []*model.CommitteeMember) []string {
	uids := make([]string, 0, len(members))
	for _, m := range members {
		uids = append(uids, m.UID)
	}
	return uids
}

// seedMembers creates three members: two in committee-1 and one in committee-2 that shares the email of
// the first.
func seedMembers(t *testing.T, store interface {
	CreateMember(context.Context, *model.CommitteeMember) error
}) {
	t.Helper()
	for _, m := range []*model.CommitteeMember{
		NewMember("m-1", "committee-1", "first.last@example.com"),
		NewMember("m-2", "committee-1", "first.last2@example.com"),
		NewMember("m-3", "committee-2", "first.last@example.com"),
	} {
		require.NoError(t, store.CreateMember(context.Background(), m))
	}
}

// RunMemberContract runs the committee member cases against the backend: create, read, update, delete,
// the email uniqueness per committee, and every lookup.
func RunMemberContract(t *testing.T, backend Backend, newStore CommitteeFactory) {
	t.Run(backend.Name, func(t *testing.T) { runMemberContract(t, newStore) })
}

func runMemberContract(t *testing.T, newStore CommitteeFactory) {
	ctx := context.Background()

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := NewMember("m-1", "committee-1", "first.last@example.com")
		require.NoError(t, store.CreateMember(ctx, want))

		got, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeMember(want), normalizeMember(got))

		onlyRevision, err := store.GetMemberRevision(ctx, "m-1")
		require.NoError(t, err)
		assert.Equal(t, revision, onlyRevision)
	})

	t.Run("a missing member is not found, and an empty uid is invalid", func(t *testing.T) {
		store := newStore(t)
		_, _, err := store.GetMember(ctx, "missing")
		requireNotFound(t, err)
		_, err = store.GetMemberRevision(ctx, "missing")
		requireNotFound(t, err)
		_, _, err = store.GetMember(ctx, "")
		requireValidation(t, err)
		_, err = store.GetMemberRevision(ctx, "")
		requireValidation(t, err)
	})

	t.Run("create rejects a nil member and missing uids", func(t *testing.T) {
		store := newStore(t)
		requireValidation(t, store.CreateMember(ctx, nil))
		requireValidation(t, store.CreateMember(ctx, NewMember("", "committee-1", "first.last@example.com")))
		requireValidation(t, store.CreateMember(ctx, NewMember("m-x", "", "first.last@example.com")))
	})

	t.Run("an email is unique per committee, whatever its case or spacing", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com\t"} {
			requireConflict(t, store.CreateMember(ctx, NewMember("m-dup", "committee-1", email)))
		}
		_, _, err := store.GetMember(ctx, "m-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateMember(ctx, NewMember("m-other", "committee-3", "first.last@example.com")), "the same email in another committee")
	})

	t.Run("the same uid is a conflict, and a username may repeat", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		requireConflict(t, store.CreateMember(ctx, NewMember("m-1", "committee-9", "first.last9@example.com")))

		repeat := NewMember("m-4", "committee-1", "first.last4@example.com")
		repeat.Username = "first-last-m-1"
		require.NoError(t, store.CreateMember(ctx, repeat))
	})

	t.Run("ListMembersByCommittee lists the committee's members", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		members, err := store.ListMembersByCommittee(ctx, "committee-1")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"m-1", "m-2"}, memberUIDs(members))

		members, err = store.ListMembersByCommittee(ctx, "no-such-committee")
		require.NoError(t, err)
		assert.Empty(t, members)
	})

	t.Run("ListMembersByOrganization matches the 18-character and 15-character spellings", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		short := NewMember("m-short", "committee-1", "first.last5@example.com")
		short.Organization.ID = SFID15
		require.NoError(t, store.CreateMember(ctx, short))

		for _, query := range []string{SFID15, SFID18, " " + SFID18 + " "} {
			members, err := store.ListMembersByOrganization(ctx, query)
			require.NoError(t, err, query)
			assert.ElementsMatch(t, []string{"m-1", "m-2", "m-3", "m-short"}, memberUIDs(members), query)
		}
		members, err := store.ListMembersByOrganization(ctx, "001A000001zzzzzAAA")
		require.NoError(t, err)
		assert.Empty(t, members)
	})

	t.Run("ListMembersByEmail and ListMembersByUsername compare the normalized value", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com "} {
			members, err := store.ListMembersByEmail(ctx, email)
			require.NoError(t, err, email)
			assert.ElementsMatch(t, []string{"m-1", "m-3"}, memberUIDs(members), email)
		}
		members, err := store.ListMembersByEmail(ctx, "nobody@example.com")
		require.NoError(t, err)
		assert.Empty(t, members)

		for _, username := range []string{"first-last-m-2", "First-Last-M-2", " first-last-m-2 "} {
			members, err := store.ListMembersByUsername(ctx, username)
			require.NoError(t, err, username)
			assert.Equal(t, []string{"m-2"}, memberUIDs(members), username)
		}
		members, err = store.ListMembersByUsername(ctx, "nobody")
		require.NoError(t, err)
		assert.Empty(t, members)
	})

	t.Run("ListAllMembers, EachMember and EachMemberStrict visit every member", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		all, err := store.ListAllMembers(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"m-1", "m-2", "m-3"}, memberUIDs(all))

		each := map[string]func(context.Context, func(*model.CommitteeMember) error) error{
			"EachMember": store.EachMember, "EachMemberStrict": store.EachMemberStrict,
		}
		for name, walk := range each {
			var visited []*model.CommitteeMember
			require.NoError(t, walk(ctx, func(m *model.CommitteeMember) error {
				visited = append(visited, m)
				return nil
			}), name)
			assert.ElementsMatch(t, []string{"m-1", "m-2", "m-3"}, memberUIDs(visited), name)

			stop := errors.New("stop here")
			calls := 0
			err := walk(ctx, func(*model.CommitteeMember) error {
				calls++
				return stop
			})
			require.ErrorIs(t, err, stop, name)
			assert.Equal(t, 1, calls, "%s stops at the first error fn returns", name)
		}
	})

	t.Run("UpdateMember replaces the member and the revision, and the lookups follow it", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		member, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)

		member.JobTitle = "Updated"
		member.Email = "first.last-new@example.com"
		member.Username = "first-last-renamed"
		member.Organization.ID = "001A000001zzzzzAAA"
		updated, err := store.UpdateMember(ctx, member, revision)
		require.NoError(t, err)
		assert.Equal(t, "Updated", updated.JobTitle)

		got, newRevision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		assert.Equal(t, "Updated", got.JobTitle)
		assert.NotEqual(t, revision, newRevision)

		byEmail, err := store.ListMembersByEmail(ctx, "first.last@example.com")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-3"}, memberUIDs(byEmail), "the old email no longer lists the member")
		byEmail, err = store.ListMembersByEmail(ctx, "first.last-new@example.com")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-1"}, memberUIDs(byEmail))
		byUsername, err := store.ListMembersByUsername(ctx, "first-last-renamed")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-1"}, memberUIDs(byUsername))
		byUsername, err = store.ListMembersByUsername(ctx, "first-last-m-1")
		require.NoError(t, err)
		assert.Empty(t, byUsername)
		byOrg, err := store.ListMembersByOrganization(ctx, "001A000001zzzzzAAA")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-1"}, memberUIDs(byOrg))
		byOrg, err = store.ListMembersByOrganization(ctx, SFID18)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"m-2", "m-3"}, memberUIDs(byOrg))

		// The old email is free again in the committee.
		require.NoError(t, store.CreateMember(ctx, NewMember("m-5", "committee-1", "first.last@example.com")))
	})

	t.Run("UpdateMember with a stale revision fails and changes nothing", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		member, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		member.JobTitle = "first writer"
		_, err = store.UpdateMember(ctx, member, revision)
		require.NoError(t, err)

		member.JobTitle = "second writer"
		_, err = store.UpdateMember(ctx, member, revision)
		requireConflict(t, err)
		got, _, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.JobTitle)
	})

	t.Run("UpdateMember of a missing member is not found, and a nil member is invalid", func(t *testing.T) {
		store := newStore(t)
		_, err := store.UpdateMember(ctx, NewMember("ghost", "committee-1", "first.last@example.com"), 1)
		requireNotFound(t, err)
		_, err = store.UpdateMember(ctx, nil, 1)
		requireValidation(t, err)
	})

	t.Run("UpdateMember enforces the email of the committee", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		member, revision, err := store.GetMember(ctx, "m-2")
		require.NoError(t, err)
		member.Email = "First.Last@example.com"
		_, err = store.UpdateMember(ctx, member, revision)
		requireConflict(t, err)

		got, _, err := store.GetMember(ctx, "m-2")
		require.NoError(t, err)
		assert.Equal(t, "first.last2@example.com", got.Email)
		byEmail, err := store.ListMembersByEmail(ctx, "first.last2@example.com")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-2"}, memberUIDs(byEmail), "a rejected update leaves the lookups alone")
	})

	t.Run("DeleteMember removes the member and every lookup", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		_, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)

		requireConflict(t, store.DeleteMember(ctx, "m-1", revision+1000))
		_, _, err = store.GetMember(ctx, "m-1")
		require.NoError(t, err, "a rejected delete changes nothing")

		require.NoError(t, store.DeleteMember(ctx, "m-1", revision))
		_, _, err = store.GetMember(ctx, "m-1")
		requireNotFound(t, err)
		requireNotFound(t, store.DeleteMember(ctx, "m-1", revision))
		requireValidation(t, store.DeleteMember(ctx, "", 1))

		byCommittee, err := store.ListMembersByCommittee(ctx, "committee-1")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-2"}, memberUIDs(byCommittee))
		byEmail, err := store.ListMembersByEmail(ctx, "first.last@example.com")
		require.NoError(t, err)
		assert.Equal(t, []string{"m-3"}, memberUIDs(byEmail))
		byUsername, err := store.ListMembersByUsername(ctx, "first-last-m-1")
		require.NoError(t, err)
		assert.Empty(t, byUsername)
		byOrg, err := store.ListMembersByOrganization(ctx, SFID18)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"m-2", "m-3"}, memberUIDs(byOrg))
		all, err := store.ListAllMembers(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"m-2", "m-3"}, memberUIDs(all))

		require.NoError(t, store.CreateMember(ctx, NewMember("m-6", "committee-1", "first.last@example.com")), "the email is free again")
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := newStore(t)
		seedMembers(t, store)
		member, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)

		wins := race(t, func(writer int) error {
			update := *member
			update.JobTitle = fmt.Sprintf("writer %d", writer)
			_, err := store.UpdateMember(ctx, &update, revision)
			return err
		}, requireConflict)
		assert.Equal(t, 1, wins)
	})
}
