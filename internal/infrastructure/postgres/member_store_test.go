// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// sfid15 and sfid18 are the same Salesforce account in its two spellings.
const sfid15 = "001A000001abcDE"

var sfid18 = utils.NormalizeAccountSFID(sfid15)

// testMember returns a member with every field set.
func testMember(uid, committeeUID, email string) *model.CommitteeMember {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:               uid,
		CommitteeUID:      committeeUID,
		Username:          "first-last-" + uid,
		Avatar:            "https://example.com/avatar.png",
		Email:             email,
		FirstName:         "First",
		LastName:          "Last",
		JobTitle:          "Engineer",
		LinkedInProfile:   "https://example.com/in/first-last",
		Role:              model.CommitteeMemberRole{Name: "Chair", StartDate: "2026-01-01", EndDate: "2027-01-01"},
		AppointedBy:       "Community",
		Status:            "Active",
		Voting:            model.CommitteeMemberVotingInfo{Status: "Voting Rep", StartDate: "2026-02-01", EndDate: "2027-02-01"},
		Organization:      model.CommitteeMemberOrganization{ID: sfid18, Name: "Example Org", Website: "https://example.com"},
		CommitteeName:     "Committee One",
		CommitteeCategory: "Technical Steering Committee",
		ProjectUID:        "project-1",
		ProjectSlug:       "project-slug",
		CreatedAt:         created,
		UpdatedAt:         created.Add(time.Hour),
	}}
}

func memberUIDs(members []*model.CommitteeMember) []string {
	uids := make([]string, 0, len(members))
	for _, m := range members {
		uids = append(uids, m.UID)
	}
	return uids
}

// seedMembers returns a store with three members: two in committee-1 and one in committee-2.
func seedMembers(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	ctx := context.Background()
	for _, m := range []*model.CommitteeMember{
		testMember("m-1", "committee-1", "first.last@example.com"),
		testMember("m-2", "committee-1", "first.last2@example.com"),
		testMember("m-3", "committee-2", "first.last@example.com"),
	} {
		require.NoError(t, store.CreateMember(ctx, m))
	}
	return store
}

func TestStore_CreateMember(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	t.Run("round-trips every field", func(t *testing.T) {
		want := testMember("m-new", "committee-1", "first.last3@example.com")
		require.NoError(t, store.CreateMember(ctx, want))
		got, revision, err := store.GetMember(ctx, "m-new")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, want.CommitteeMemberBase, got.CommitteeMemberBase)
	})

	t.Run("an email already in the committee is a conflict, whatever its case or spacing", func(t *testing.T) {
		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com\t"} {
			requireConflict(t, store.CreateMember(ctx, testMember("m-dup", "committee-1", email)))
		}
		_, _, err := store.GetMember(ctx, "m-dup")
		requireNotFound(t, err)
	})

	t.Run("the same email in another committee is allowed", func(t *testing.T) {
		require.NoError(t, store.CreateMember(ctx, testMember("m-other", "committee-3", "first.last@example.com")))
	})

	t.Run("the same uid is a conflict", func(t *testing.T) {
		requireConflict(t, store.CreateMember(ctx, testMember("m-1", "committee-9", "first.last9@example.com")))
	})

	t.Run("a nil member or missing uids are validation errors", func(t *testing.T) {
		var validation errs.Validation
		require.ErrorAs(t, store.CreateMember(ctx, nil), &validation)
		require.ErrorAs(t, store.CreateMember(ctx, testMember("", "committee-1", "a@example.com")), &validation)
		require.ErrorAs(t, store.CreateMember(ctx, testMember("m-x", "", "a@example.com")), &validation)
	})
}

func TestStore_GetMember(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	member, _, err := store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "first.last@example.com", member.Email)

	_, _, err = store.GetMember(ctx, "missing")
	requireNotFound(t, err)

	var validation errs.Validation
	_, _, err = store.GetMember(ctx, "")
	require.ErrorAs(t, err, &validation)
	assert.Contains(t, err.Error(), "member UID")
}

func TestStore_GetMemberRevision(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	_, revision, err := store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	got, err := store.GetMemberRevision(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, revision, got)

	_, err = store.GetMemberRevision(ctx, "missing")
	requireNotFound(t, err)

	var validation errs.Validation
	_, err = store.GetMemberRevision(ctx, "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListMembersByCommittee(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	members, err := store.ListMembersByCommittee(ctx, "committee-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"m-1", "m-2"}, memberUIDs(members))

	members, err = store.ListMembersByCommittee(ctx, "no-such-committee")
	require.NoError(t, err)
	assert.Empty(t, members)

	var validation errs.Validation
	_, err = store.ListMembersByCommittee(ctx, "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListMembersByEmail(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	for _, email := range []string{"first.last@example.com", "FIRST.LAST@EXAMPLE.COM", "  first.last@example.com "} {
		members, err := store.ListMembersByEmail(ctx, email)
		require.NoError(t, err)
		assert.Equal(t, []string{"m-1", "m-3"}, memberUIDs(members), email)
	}

	var validation errs.Validation
	_, err := store.ListMembersByEmail(ctx, "  ")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListMembersByUsername(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	members, err := store.ListMembersByUsername(ctx, " First-Last-M-2 ")
	require.NoError(t, err)
	assert.Equal(t, []string{"m-2"}, memberUIDs(members))

	var validation errs.Validation
	_, err = store.ListMembersByUsername(ctx, " ")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListMembersByOrganization(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)
	short := testMember("m-short", "committee-4", "first.last-short@example.com")
	short.Organization.ID = sfid15
	require.NoError(t, store.CreateMember(ctx, short))

	for _, query := range []string{sfid15, sfid18, " " + sfid18 + " "} {
		members, err := store.ListMembersByOrganization(ctx, query)
		require.NoError(t, err)
		assert.Equal(t, []string{"m-1", "m-2", "m-3", "m-short"}, memberUIDs(members), query)
	}

	members, err := store.ListMembersByOrganization(ctx, utils.NormalizeAccountSFID("001A000001zzzzz"))
	require.NoError(t, err)
	assert.Empty(t, members)

	var validation errs.Validation
	_, err = store.ListMembersByOrganization(ctx, "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListAllMembers(t *testing.T) {
	members, err := seedMembers(t).ListAllMembers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"m-1", "m-2", "m-3"}, memberUIDs(members))
}

// testEach checks the streaming contract shared by EachMember and EachMemberStrict.
func testEach(t *testing.T, each func(*Store) func(context.Context, func(*model.CommitteeMember) error) error) {
	ctx := context.Background()
	store := seedMembers(t)

	t.Run("visits every member", func(t *testing.T) {
		var seen []string
		require.NoError(t, each(store)(ctx, func(m *model.CommitteeMember) error {
			seen = append(seen, m.UID)
			return nil
		}))
		assert.Equal(t, []string{"m-1", "m-2", "m-3"}, seen)
	})

	t.Run("stops at the first error fn returns", func(t *testing.T) {
		stop := errors.New("stop")
		calls := 0
		err := each(store)(ctx, func(*model.CommitteeMember) error {
			calls++
			return stop
		})
		assert.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})

	t.Run("fn may call back into the store", func(t *testing.T) {
		require.NoError(t, each(store)(ctx, func(m *model.CommitteeMember) error {
			_, _, err := store.GetMember(ctx, m.UID)
			return err
		}))
	})
}

func TestStore_EachMember(t *testing.T) {
	testEach(t, func(s *Store) func(context.Context, func(*model.CommitteeMember) error) error { return s.EachMember })
}

func TestStore_EachMemberStrict(t *testing.T) {
	testEach(t, func(s *Store) func(context.Context, func(*model.CommitteeMember) error) error {
		return s.EachMemberStrict
	})
}

func TestStore_UpdateMember(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	current := func(t *testing.T, uid string) (*model.CommitteeMember, uint64) {
		t.Helper()
		member, revision, err := store.GetMember(ctx, uid)
		require.NoError(t, err)
		return member, revision
	}

	t.Run("updates the member and replaces the revision", func(t *testing.T) {
		member, revision := current(t, "m-1")
		member.JobTitle = "Changed"
		updated, err := store.UpdateMember(ctx, member, revision)
		require.NoError(t, err)
		assert.Equal(t, "Changed", updated.JobTitle)

		got, newRevision := current(t, "m-1")
		assert.Equal(t, "Changed", got.JobTitle)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		member, revision := current(t, "m-1")
		member.JobTitle = "first writer"
		_, err := store.UpdateMember(ctx, member, revision)
		require.NoError(t, err)

		member.JobTitle = "second writer"
		_, err = store.UpdateMember(ctx, member, revision)
		requireConflict(t, err)
		got, _ := current(t, "m-1")
		assert.Equal(t, "first writer", got.JobTitle)
	})

	t.Run("a missing member is not found and a nil one is invalid", func(t *testing.T) {
		member, revision := current(t, "m-1")
		member.UID = "missing"
		_, err := store.UpdateMember(ctx, member, revision)
		requireNotFound(t, err)

		var validation errs.Validation
		_, err = store.UpdateMember(ctx, nil, 1)
		require.ErrorAs(t, err, &validation)
		member.UID = ""
		_, err = store.UpdateMember(ctx, member, revision)
		require.ErrorAs(t, err, &validation)
	})

	t.Run("taking another member's email in the committee is a conflict", func(t *testing.T) {
		member, revision := current(t, "m-2")
		member.Email = "First.Last@example.com"
		member.CommitteeUID = "committee-1"
		_, err := store.UpdateMember(ctx, member, revision)
		requireConflict(t, err)
		got, _ := current(t, "m-2")
		assert.Equal(t, "first.last2@example.com", got.Email)
	})

	t.Run("the old email is free after a change", func(t *testing.T) {
		member, revision := current(t, "m-2")
		member.Email = "first.last-changed@example.com"
		_, err := store.UpdateMember(ctx, member, revision)
		require.NoError(t, err)
		require.NoError(t, store.CreateMember(ctx, testMember("m-reuse", "committee-1", "first.last2@example.com")))
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, revision := current(t, "m-3")
		var wg sync.WaitGroup
		results := make([]error, 8)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				member := &model.CommitteeMember{CommitteeMemberBase: original.CommitteeMemberBase}
				member.JobTitle = fmt.Sprintf("writer %d", i)
				_, results[i] = store.UpdateMember(ctx, member, revision)
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

func TestStore_DeleteMember(t *testing.T) {
	ctx := context.Background()
	store := seedMembers(t)

	t.Run("a stale revision is a conflict and deletes nothing", func(t *testing.T) {
		_, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		requireConflict(t, store.DeleteMember(ctx, "m-1", revision+1))
		_, _, err = store.GetMember(ctx, "m-1")
		require.NoError(t, err)
	})

	t.Run("deletes the member and frees its email", func(t *testing.T) {
		_, revision, err := store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		require.NoError(t, store.DeleteMember(ctx, "m-1", revision))
		_, _, err = store.GetMember(ctx, "m-1")
		requireNotFound(t, err)
		require.NoError(t, store.CreateMember(ctx, testMember("m-again", "committee-1", "first.last@example.com")))
	})

	t.Run("a missing member is not found", func(t *testing.T) {
		requireNotFound(t, store.DeleteMember(ctx, "missing", 1))
		var validation errs.Validation
		require.ErrorAs(t, store.DeleteMember(ctx, "", 1), &validation)
	})
}
