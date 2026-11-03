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

// testInvite returns an invite with every field set.
func testInvite(uid, committeeUID, email string) *model.CommitteeInvite {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &model.CommitteeInvite{
		UID:                  uid,
		CommitteeUID:         committeeUID,
		CommitteeName:        "Committee One",
		OrganizationRequired: true,
		InviteeEmail:         email,
		Role:                 "Member",
		Organization:         &model.CommitteeMemberOrganization{ID: "org-1", Name: "Example Org", Website: "https://example.com"},
		Status:               "pending",
		CreatedAt:            created,
		Inviter:              &model.CommitteeUser{Name: "First Last", Username: "first-last", Email: "first.last@example.com"},
		ExpiresAt:            created.Add(30 * 24 * time.Hour),
	}
}

func inviteUIDs(invites []*model.CommitteeInvite) []string {
	uids := make([]string, 0, len(invites))
	for _, i := range invites {
		uids = append(uids, i.UID)
	}
	return uids
}

// seedInvites returns a store with two invites in committee-1 and one in committee-2.
func seedInvites(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	for _, i := range []*model.CommitteeInvite{
		testInvite("i-1", "committee-1", "first.last@example.com"),
		testInvite("i-2", "committee-1", "first.last2@example.com"),
		testInvite("i-3", "committee-2", "first.last@example.com"),
	} {
		require.NoError(t, store.CreateInvite(context.Background(), i))
	}
	return store
}

func TestStore_CreateInvite(t *testing.T) {
	ctx := context.Background()
	store := seedInvites(t)

	t.Run("round-trips every field", func(t *testing.T) {
		want := testInvite("i-new", "committee-1", "first.last3@example.com")
		require.NoError(t, store.CreateInvite(ctx, want))
		got, revision, err := store.GetInvite(ctx, "i-new")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, want, got)
	})

	t.Run("an invite with no organization, inviter or expiry round-trips as empty", func(t *testing.T) {
		want := testInvite("i-bare", "committee-1", "first.last4@example.com")
		want.Organization, want.Inviter, want.ExpiresAt = nil, nil, time.Time{}
		require.NoError(t, store.CreateInvite(ctx, want))
		got, _, err := store.GetInvite(ctx, "i-bare")
		require.NoError(t, err)
		assert.Equal(t, want, got)

		var noExpiry bool
		require.NoError(t, store.pool.QueryRow(ctx, "SELECT expires_at IS NULL FROM committee_invites WHERE uid = 'i-bare'").Scan(&noExpiry))
		assert.True(t, noExpiry, "no expiry is stored as NULL, not a zero time")
	})

	t.Run("an email already invited to the committee is a conflict, whatever its case, spacing or status", func(t *testing.T) {
		_, revision, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		declined, _, err := store.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		declined.Status = "declined"
		require.NoError(t, store.UpdateInvite(ctx, declined, revision))

		for _, email := range []string{"first.last@example.com", "First.Last@Example.com", " first.last@example.com\t"} {
			requireConflict(t, store.CreateInvite(ctx, testInvite("i-dup", "committee-1", email)))
		}
		_, _, err = store.GetInvite(ctx, "i-dup")
		requireNotFound(t, err)
	})

	t.Run("the same email in another committee is allowed", func(t *testing.T) {
		require.NoError(t, store.CreateInvite(ctx, testInvite("i-other", "committee-3", "first.last@example.com")))
	})

	t.Run("the same uid is a conflict and a nil invite is invalid", func(t *testing.T) {
		requireConflict(t, store.CreateInvite(ctx, testInvite("i-2", "committee-9", "first.last9@example.com")))
		var validation errs.Validation
		require.ErrorAs(t, store.CreateInvite(ctx, nil), &validation)
	})
}

func TestStore_GetInvite(t *testing.T) {
	ctx := context.Background()
	store := seedInvites(t)

	invite, _, err := store.GetInvite(ctx, "i-1")
	require.NoError(t, err)
	assert.Equal(t, "first.last@example.com", invite.InviteeEmail)

	_, _, err = store.GetInvite(ctx, "missing")
	requireNotFound(t, err)

	var validation errs.Validation
	_, _, err = store.GetInvite(ctx, "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListInvites(t *testing.T) {
	ctx := context.Background()
	store := seedInvites(t)

	invites, err := store.ListInvites(ctx, "committee-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"i-1", "i-2"}, inviteUIDs(invites))

	invites, err = store.ListInvites(ctx, "no-such-committee")
	require.NoError(t, err)
	assert.Empty(t, invites)
}

func TestStore_ListAllInvites(t *testing.T) {
	invites, err := seedInvites(t).ListAllInvites(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"i-1", "i-2", "i-3"}, inviteUIDs(invites))
}

func TestStore_UpdateInvite(t *testing.T) {
	ctx := context.Background()
	store := seedInvites(t)

	current := func(t *testing.T, uid string) (*model.CommitteeInvite, uint64) {
		t.Helper()
		invite, revision, err := store.GetInvite(ctx, uid)
		require.NoError(t, err)
		return invite, revision
	}
	updatedAt := func(t *testing.T, uid string) time.Time {
		t.Helper()
		var at time.Time
		require.NoError(t, store.pool.QueryRow(ctx, "SELECT updated_at FROM committee_invites WHERE uid = $1", uid).Scan(&at))
		return at
	}

	t.Run("updates the invite, replaces the revision and sets updated_at", func(t *testing.T) {
		invite, revision := current(t, "i-1")
		assert.True(t, updatedAt(t, "i-1").Equal(invite.CreatedAt), "updated_at starts at created_at")

		invite.Status = "accepted"
		require.NoError(t, store.UpdateInvite(ctx, invite, revision))
		got, newRevision := current(t, "i-1")
		assert.Equal(t, "accepted", got.Status)
		assert.NotEqual(t, revision, newRevision)
		assert.True(t, updatedAt(t, "i-1").After(invite.CreatedAt))
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		invite, revision := current(t, "i-2")
		invite.Status = "first writer"
		require.NoError(t, store.UpdateInvite(ctx, invite, revision))

		invite.Status = "second writer"
		requireConflict(t, store.UpdateInvite(ctx, invite, revision))
		got, _ := current(t, "i-2")
		assert.Equal(t, "first writer", got.Status)
	})

	t.Run("a missing invite is not found and a nil one is invalid", func(t *testing.T) {
		invite, revision := current(t, "i-2")
		invite.UID = "missing"
		requireNotFound(t, store.UpdateInvite(ctx, invite, revision))

		var validation errs.Validation
		require.ErrorAs(t, store.UpdateInvite(ctx, nil, 1), &validation)
	})

	t.Run("taking another invite's email in the committee is a conflict", func(t *testing.T) {
		invite, revision := current(t, "i-2")
		invite.InviteeEmail = "First.Last@example.com"
		requireConflict(t, store.UpdateInvite(ctx, invite, revision))
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, revision := current(t, "i-3")
		var wg sync.WaitGroup
		results := make([]error, 8)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				invite := *original
				invite.Status = fmt.Sprintf("writer %d", i)
				results[i] = store.UpdateInvite(ctx, &invite, revision)
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
