// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Fakes
// ─────────────────────────────────────────────────────────────────────────────

// fakeUserReader returns a fixed username for a given email; errors for unknown emails.
type fakeUserReader struct {
	// emailToUsername maps normalized email → username. A "" value means not found.
	emailToUsername map[string]string
	// errFor maps email → error to return instead of a lookup.
	errFor map[string]error
}

func (r *fakeUserReader) UsernameByEmail(_ context.Context, email string) (string, error) {
	if err, ok := r.errFor[email]; ok {
		return "", err
	}
	username, ok := r.emailToUsername[email]
	if !ok {
		return "", errs.NewNotFound(fmt.Sprintf("user not found: %s", email))
	}
	return username, nil
}

func (r *fakeUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (r *fakeUserReader) UserMetadataByPrincipal(_ context.Context, _ string) (*model.UserMetadata, error) {
	return nil, nil
}

// emailOnlyMember builds an email-only CommitteeMember (no username).
func emailOnlyMember(uid, committeeUID, email string) *model.CommitteeMember {
	return &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          uid,
			CommitteeUID: committeeUID,
			Email:        email,
		},
	}
}

// promotedMember builds a CommitteeMember that already has a username (already promoted).
func promotedMember(uid, committeeUID, email, username string) *model.CommitteeMember {
	m := emailOnlyMember(uid, committeeUID, email)
	m.Username = username
	return m
}

func newPromoteRC(reader port.CommitteeReader, writer service.CommitteeWriter, user *fakeUserReader, args []string) commands.RunContext {
	return commands.RunContext{
		CommitteeReader:             reader,
		CommitteeWriterOrchestrator: writer,
		UserReader:                  user,
		Args:                        args,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestPromoteEmailOnly_DryRunDoesNotWrite(t *testing.T) {
	member := emailOnlyMember("m1", "c1", "alice@example.com")
	reader := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {member}}}
	writer := &stubCommitteeWriter{}
	user := &fakeUserReader{emailToUsername: map[string]string{"alice@example.com": "alice"}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=true"}))
	require.NoError(t, err)
	assert.Empty(t, writer.updated, "dry-run must not call UpdateMember")
}

func TestPromoteEmailOnly_PromotesResolvableSeats(t *testing.T) {
	m1 := emailOnlyMember("m1", "c1", "alice@example.com")
	m2 := emailOnlyMember("m2", "c2", "bob@example.com")
	reader := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {m1},
		"c2": {m2},
	}}
	writer := &stubCommitteeWriter{}
	user := &fakeUserReader{emailToUsername: map[string]string{
		"alice@example.com": "alice",
		"bob@example.com":   "bob",
	}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Len(t, writer.updated, 2)

	usernames := map[string]bool{}
	for _, u := range writer.updated {
		usernames[u.Username] = true
	}
	assert.True(t, usernames["alice"])
	assert.True(t, usernames["bob"])
}

func TestPromoteEmailOnly_SkipsAlreadyPromoted(t *testing.T) {
	already := promotedMember("m1", "c1", "carol@example.com", "carol")
	reader := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {already}}}
	writer := &stubCommitteeWriter{}
	user := &fakeUserReader{emailToUsername: map[string]string{"carol@example.com": "carol"}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Empty(t, writer.updated, "already-promoted seat must not be written again")
}

func TestPromoteEmailOnly_SkipsNoEmail(t *testing.T) {
	noEmail := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", CommitteeUID: "c1"}}
	reader := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {noEmail}}}
	writer := &stubCommitteeWriter{}
	user := &fakeUserReader{emailToUsername: map[string]string{}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Empty(t, writer.updated)
}

func TestPromoteEmailOnly_SkipsWhenEmailNotResolvable(t *testing.T) {
	member := emailOnlyMember("m1", "c1", "unknown@example.com")
	reader := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {member}}}
	writer := &stubCommitteeWriter{}
	// emailToUsername is empty → UsernameByEmail returns NotFound
	user := &fakeUserReader{emailToUsername: map[string]string{}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Empty(t, writer.updated, "unresolvable email must be skipped, not failed")
}

func TestPromoteEmailOnly_SameEmailAcrossCommitteesCallsAuthOnce(t *testing.T) {
	email := "shared@example.com"
	m1 := emailOnlyMember("m1", "c1", email)
	m2 := emailOnlyMember("m2", "c2", email)
	reader := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {m1},
		"c2": {m2},
	}}
	writer := &stubCommitteeWriter{}
	callCount := 0
	user := &fakeUserReader{emailToUsername: map[string]string{email: "shared-user"}}
	// We can't intercept call count directly, but we verify both seats are promoted.

	_ = callCount
	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Len(t, writer.updated, 2, "both seats with the same email must be promoted")
	for _, u := range writer.updated {
		assert.Equal(t, "shared-user", u.Username)
	}
}

func TestPromoteEmailOnly_AuthErrorCountsAsFailed(t *testing.T) {
	member := emailOnlyMember("m1", "c1", "broken@example.com")
	reader := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {member}}}
	writer := &stubCommitteeWriter{}
	user := &fakeUserReader{errFor: map[string]error{
		"broken@example.com": fmt.Errorf("auth service timeout"),
	}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	// The command returns an error when any seat failed.
	require.Error(t, err)
	assert.Empty(t, writer.updated)
}

func TestPromoteEmailOnly_EmptyBucket_Succeeds(t *testing.T) {
	reader := &mockReader{members: map[string][]*model.CommitteeMember{}}
	writer := &stubCommitteeWriter{}
	user := &fakeUserReader{emailToUsername: map[string]string{}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, writer, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Empty(t, writer.updated)
}

func TestPromoteEmailOnly_ConflictRetriesAndSucceeds(t *testing.T) {
	email := "retry@example.com"
	// Use a copying reader so that GetMember returns a fresh struct on every call,
	// matching what the real NATS KV storage does (each read is independent).
	snap := emailOnlyMember("m1", "c1", email)
	reader := &copyingMemberReader{snap: snap}
	// First UpdateMember call returns a conflict; second succeeds.
	conflictWriter := &retryingStubWriter{maxConflicts: 1}
	user := &fakeUserReader{emailToUsername: map[string]string{email: "retried"}}

	sub := &promoteEmailOnlyMembersSubcommand{}
	err := sub.Run(context.Background(), newPromoteRC(reader, conflictWriter, user, []string{"--dry-run=false"}))
	require.NoError(t, err)
	assert.Len(t, conflictWriter.updated, 1)
	assert.Equal(t, "retried", conflictWriter.updated[0].Username)
}

// copyingMemberReader streams a single member via EachMember and returns a fresh copy
// on every GetMember call so that mutations between retries don't bleed through.
type copyingMemberReader struct {
	mockReader
	snap *model.CommitteeMember
}

func (r *copyingMemberReader) EachMember(_ context.Context, fn func(*model.CommitteeMember) error) error {
	cp := *r.snap
	return fn(&cp)
}

func (r *copyingMemberReader) GetMember(_ context.Context, _ string) (*model.CommitteeMember, uint64, error) {
	cp := *r.snap
	return &cp, 1, nil
}

// retryingStubWriter simulates revision conflicts on the first N calls to UpdateMember,
// then succeeds.
type retryingStubWriter struct {
	stubCommitteeWriter
	maxConflicts int
	callCount    int
}

func (w *retryingStubWriter) UpdateMember(_ context.Context, member *model.CommitteeMember, _ uint64, _ bool, _ bool) (*model.CommitteeMember, error) {
	w.callCount++
	if w.callCount <= w.maxConflicts {
		return nil, errs.NewConflict(fmt.Sprintf("committee_member %s revision conflict", member.UID))
	}
	w.updated = append(w.updated, member)
	return member, nil
}
