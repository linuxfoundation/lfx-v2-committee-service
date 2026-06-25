// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	pkgerrors "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// avatarMockUserReader satisfies port.UserReader for the avatar backfill tests.
type avatarMockUserReader struct {
	pictures  map[string]string // username → picture (UserMetadataByPrincipal)
	usernames map[string]string // email → username (UsernameByEmail fallback)
	metaErr   error             // when set, returned by UserMetadataByPrincipal for every principal
	notFound  map[string]bool   // username → return NotFound from UserMetadataByPrincipal
}

func (m *avatarMockUserReader) UsernameByEmail(_ context.Context, email string) (string, error) {
	if u, ok := m.usernames[email]; ok {
		return u, nil
	}
	return "", pkgerrors.NewNotFound("mock: username not found for email: " + email)
}

func (m *avatarMockUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, pkgerrors.NewNotFound("mock: not implemented")
}

func (m *avatarMockUserReader) UserMetadataByPrincipal(_ context.Context, principal string) (*model.UserMetadata, error) {
	if m.metaErr != nil {
		return nil, m.metaErr
	}
	if m.notFound[principal] {
		return nil, pkgerrors.NewNotFound("mock: metadata not found for " + principal)
	}
	return &model.UserMetadata{Picture: m.pictures[principal]}, nil
}

func avatarMember(uid, username, avatar string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:      uid,
		Username: username,
		Avatar:   avatar,
	}}
}

func newAvatarRC(r *mockReader, w *mockMemberWriter, ur *avatarMockUserReader, args ...string) commands.RunContext {
	return commands.RunContext{
		CommitteeReader:       r,
		CommitteeMemberWriter: w,
		UserReader:            ur,
		Args:                  args,
	}
}

// freshGetMemberReader overrides GetMember to return a DISTINCT struct from the EachMember snapshot,
// simulating a concurrent change to another field between scan and write — so a test can prove the
// backfill applies only Avatar to the re-read record (not the stale snapshot).
type freshGetMemberReader struct {
	*mockReader
	fresh *model.CommitteeMember
}

func (r *freshGetMemberReader) GetMember(_ context.Context, _ string) (*model.CommitteeMember, uint64, error) {
	return r.fresh, 7, nil
}

func TestMemberAvatarAttribute_WritesFreshRecord_OnlyAvatar(t *testing.T) {
	// EachMember snapshot: stale FirstName, empty avatar.
	snapshot := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", Username: "alice", FirstName: "Stale", Avatar: ""}}
	// The fresh re-read carries a concurrent change to FirstName that must be preserved.
	fresh := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", Username: "alice", FirstName: "ConcurrentlyChanged", Avatar: ""}}
	r := &freshGetMemberReader{
		mockReader: &mockReader{members: map[string][]*model.CommitteeMember{"c1": {snapshot}}},
		fresh:      fresh,
	}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{"alice": "https://example.com/alice.png"}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), commands.RunContext{
		CommitteeReader: r, CommitteeMemberWriter: w, UserReader: ur, Args: []string{"--dry-run=false"},
	})
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Equal(t, "https://example.com/alice.png", w.updated[0].Avatar, "avatar must be applied to the fresh record")
	assert.Equal(t, "ConcurrentlyChanged", w.updated[0].FirstName, "the concurrent change on the fresh record must be preserved (stale snapshot not written back)")
}

func TestMemberAvatarAttribute_SkipsWhenFreshAlreadyCorrect(t *testing.T) {
	// Snapshot shows drift, but a concurrent run already set the avatar on the fresh record → no write.
	snapshot := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", Username: "alice", Avatar: ""}}
	fresh := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", Username: "alice", Avatar: "https://example.com/alice.png"}}
	r := &freshGetMemberReader{
		mockReader: &mockReader{members: map[string][]*model.CommitteeMember{"c1": {snapshot}}},
		fresh:      fresh,
	}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{"alice": "https://example.com/alice.png"}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), commands.RunContext{
		CommitteeReader: r, CommitteeMemberWriter: w, UserReader: ur, Args: []string{"--dry-run=false"},
	})
	require.NoError(t, err)
	assert.Empty(t, w.updated, "no write when the fresh re-read already has the resolved avatar")
}

func TestMemberAvatarAttribute_BackfillsAvatar(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "alice", ""), avatarMember("m2", "bob", "")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{
		"alice": "https://example.com/alice.png",
		"bob":   "https://example.com/bob.png",
	}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false"))
	require.NoError(t, err)
	require.Len(t, w.updated, 2)
	got := updatedByUID(w)
	assert.Equal(t, "https://example.com/alice.png", got["m1"].Avatar)
	assert.Equal(t, "https://example.com/bob.png", got["m2"].Avatar)
}

func TestMemberAvatarAttribute_ClearsStaleAvatar(t *testing.T) {
	// A resolved-but-empty picture clears a stale stored avatar (photo removed upstream).
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "ghost", "https://example.com/old.png")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false"))
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Empty(t, w.updated[0].Avatar)
}

func TestMemberAvatarAttribute_Idempotent_SkipsCorrect(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "alice", "https://example.com/alice.png")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{"alice": "https://example.com/alice.png"}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false"))
	require.NoError(t, err)
	assert.Empty(t, w.updated, "a member whose avatar already matches must not be rewritten")
}

func TestMemberAvatarAttribute_DryRun_NoWrites(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "alice", "")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{"alice": "https://example.com/alice.png"}}

	// default dry-run is true
	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur))
	require.NoError(t, err)
	assert.Empty(t, w.updated, "dry-run must not write")
}

func TestMemberAvatarAttribute_LookupError_LeavesAvatarUntouched(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "alice", "https://example.com/old.png")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{metaErr: errors.New("nats: metadata timeout")}

	// A transport error is isolated (no write, no abort) so a transient outage never wipes a good avatar.
	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false"))
	require.NoError(t, err)
	assert.Empty(t, w.updated)
}

func TestMemberAvatarAttribute_NotFound_LeavesAvatarUntouched(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "ghost", "https://example.com/old.png")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{notFound: map[string]bool{"ghost": true}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false"))
	require.NoError(t, err)
	assert.Empty(t, w.updated, "a NotFound principal must not wipe an existing avatar")
}

func TestMemberAvatarAttribute_MissingOnly_SkipsPopulated(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {avatarMember("m1", "alice", "https://example.com/alice.png"), avatarMember("m2", "bob", "")},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{pictures: map[string]string{
		"alice": "https://example.com/alice-new.png",
		"bob":   "https://example.com/bob.png",
	}}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false", "--missing-only"))
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Equal(t, "m2", w.updated[0].UID)
}

func TestMemberAvatarAttribute_UsernameFallbackByEmail(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{
		"c1": {{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", Email: "carol@example.com"}}},
	}}
	w := &mockMemberWriter{}
	ur := &avatarMockUserReader{
		usernames: map[string]string{"carol@example.com": "carol"},
		pictures:  map[string]string{"carol": "https://example.com/carol.png"},
	}

	err := (&memberAvatarAttributeSubcommand{}).Run(context.Background(), newAvatarRC(r, w, ur, "--dry-run=false"))
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Equal(t, "https://example.com/carol.png", w.updated[0].Avatar)
}
