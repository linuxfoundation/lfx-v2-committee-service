// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The HTTP layer maps the outer error type (cmd/committee-api/service/error.go), so these check it
// directly: errors.As would also accept an internal error that wraps the right one.
func requireOuterConflict(t *testing.T, err error) {
	t.Helper()
	_, ok := err.(errs.Conflict)
	require.True(t, ok, "want a conflict as the outer error, got %T: %v", err, err)
}

func requireOuterValidation(t *testing.T, err error) {
	t.Helper()
	_, ok := err.(errs.Validation)
	require.True(t, ok, "want a validation error as the outer error, got %T: %v", err, err)
}

func newConflictTestMember(uid, email string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: uid, CommitteeUID: "c-1", Username: "first-last-" + uid, Email: email, FirstName: "First", LastName: "Last",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}
}

// A write guarded by a revision that is no longer current is a conflict (a 409), as the PostgreSQL store
// reports it, whichever record it targets; a read or delete with an empty uid is a validation error (a 400).
func TestStorage_LostSwapIsConflict(t *testing.T) {
	ctx := context.Background()
	s := NewStorage(newKVClient(t))

	require.NoError(t, s.CreateMember(ctx, newConflictTestMember("m-1", "first.last@example.com")))
	_, memberRevision, err := s.GetMember(ctx, "m-1")
	require.NoError(t, err)

	invite := &model.CommitteeInvite{UID: "i-1", CommitteeUID: "c-1", InviteeEmail: "first.last@example.com", Status: "pending", CreatedAt: time.Now()}
	require.NoError(t, s.CreateInvite(ctx, invite))
	_, inviteRevision, err := s.GetInvite(ctx, "i-1")
	require.NoError(t, err)

	application := &model.CommitteeApplication{UID: "a-1", CommitteeUID: "c-1", ApplicantEmail: "first.last@example.com", Status: "pending", CreatedAt: time.Now()}
	require.NoError(t, s.CreateApplication(ctx, application))
	_, applicationRevision, err := s.GetApplication(ctx, "a-1")
	require.NoError(t, err)

	t.Run("delete member", func(t *testing.T) {
		requireOuterConflict(t, s.DeleteMember(ctx, "m-1", memberRevision+1000))
		_, _, err := s.GetMember(ctx, "m-1")
		require.NoError(t, err, "a rejected delete changes nothing")
	})

	t.Run("update invite", func(t *testing.T) {
		invite.Status = "accepted"
		require.NoError(t, s.UpdateInvite(ctx, invite, inviteRevision))
		invite.Status = "stale writer"
		requireOuterConflict(t, s.UpdateInvite(ctx, invite, inviteRevision))
		got, _, err := s.GetInvite(ctx, "i-1")
		require.NoError(t, err)
		assert.Equal(t, "accepted", got.Status)
	})

	t.Run("update application", func(t *testing.T) {
		application.Status = "approved"
		require.NoError(t, s.UpdateApplication(ctx, application, applicationRevision))
		application.Status = "stale writer"
		requireOuterConflict(t, s.UpdateApplication(ctx, application, applicationRevision))
		got, _, err := s.GetApplication(ctx, "a-1")
		require.NoError(t, err)
		assert.Equal(t, "approved", got.Status)
	})

	t.Run("update member", func(t *testing.T) {
		member, revision, err := s.GetMember(ctx, "m-1")
		require.NoError(t, err)
		member.JobTitle = "first writer"
		_, err = s.UpdateMember(ctx, member, revision)
		require.NoError(t, err)
		member.JobTitle = "stale writer"
		_, err = s.UpdateMember(ctx, member, revision)
		requireOuterConflict(t, err)
	})

	t.Run("an empty uid is a validation error", func(t *testing.T) {
		_, _, err := s.GetMember(ctx, "")
		requireOuterValidation(t, err)
		_, err = s.GetMemberRevision(ctx, "")
		requireOuterValidation(t, err)
		_, _, err = s.GetInvite(ctx, "")
		requireOuterValidation(t, err)
		_, _, err = s.GetApplication(ctx, "")
		requireOuterValidation(t, err)
		_, _, err = s.GetSettings(ctx, "")
		requireOuterValidation(t, err)
		requireOuterValidation(t, s.DeleteMember(ctx, "", 1))
	})
}

// failingUpdateKV wraps a real bucket so every Update fails with err, which is how a lost compare-and-swap
// looks to the store: jetstream.ErrKeyExists is JetStream's "wrong last sequence".
type failingUpdateKV struct {
	jetstream.KeyValue
	err error
}

func (k *failingUpdateKV) Update(context.Context, string, []byte, uint64) (uint64, error) {
	return 0, k.err
}

// The counter updates read the committee and write it back guarded by the revision they read, so another
// writer in between makes the write lose the swap: a conflict, not an internal error. A different failure
// stays an internal error.
func TestStorage_CounterLostSwapIsConflict(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	s := NewStorage(client)
	require.NoError(t, s.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	real := client.kvStore[constants.KVBucketNameCommittees]

	client.kvStore[constants.KVBucketNameCommittees] = &failingUpdateKV{KeyValue: real, err: jetstream.ErrKeyExists}
	_, _, err := s.UpdateTotalMembers(ctx, "uid-1", 9)
	requireOuterConflict(t, err)
	_, _, err = s.UpdateHasMailingList(ctx, "uid-1", false)
	requireOuterConflict(t, err)

	client.kvStore[constants.KVBucketNameCommittees] = &failingUpdateKV{KeyValue: real, err: errors.New("simulated failure")}
	_, _, err = s.UpdateTotalMembers(ctx, "uid-1", 9)
	_, ok := err.(errs.Unexpected)
	require.True(t, ok, "want an internal error as the outer error, got %T: %v", err, err)
}
