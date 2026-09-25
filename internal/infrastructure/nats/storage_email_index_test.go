// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func emailMember(uid, email string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:   uid,
		Email: email,
	}}
}

// TestStorage_IndexMemberByEmail_Idempotent verifies that a duplicate index write
// (jetstream.ErrKeyExists) is treated as success — the entry already exists.
func TestStorage_IndexMemberByEmail_Idempotent(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{createErr: jetstream.ErrKeyExists})

	key, err := s.IndexMemberByEmail(context.Background(), emailMember("m-1", "user@example.com"))
	require.NoError(t, err, "ErrKeyExists must be treated as idempotent success")
	assert.NotEmpty(t, key)
}

// TestStorage_IndexMemberByEmail_CreateErrorPropagates verifies that a non-ErrKeyExists error
// from Create is wrapped and returned (with the key still returned for rollback).
func TestStorage_IndexMemberByEmail_CreateErrorPropagates(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{createErr: errors.New("kv create boom")})

	key, err := s.IndexMemberByEmail(context.Background(), emailMember("m-1", "user@example.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kv create boom", "underlying Create error must be surfaced")
	assert.NotEmpty(t, key, "the index key is returned even on failure so callers can roll it back")
}

// TestStorage_IndexMemberByEmail_NoEmailIsNoOp verifies that a member without an email writes
// no index entry and returns an empty key (not an error).
func TestStorage_IndexMemberByEmail_NoEmailIsNoOp(t *testing.T) {
	// createErr is set but must never be hit because no Create happens for an email-less member.
	s := newTestStorageWithKV(&mockKV{createErr: errors.New("should not be called")})

	key, err := s.IndexMemberByEmail(context.Background(), emailMember("m-1", ""))
	require.NoError(t, err)
	assert.Empty(t, key)
}

// TestStorage_ListMembersByEmail_EmptyEmailIsValidationError verifies the empty-email guard.
func TestStorage_ListMembersByEmail_EmptyEmailIsValidationError(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{})
	_, err := s.ListMembersByEmail(context.Background(), "   ")
	require.Error(t, err)
}

// TestStorage_ListMembersByEmail_ListKeysErrorPropagates verifies that an error from
// ListKeysFiltered is wrapped and returned.
func TestStorage_ListMembersByEmail_ListKeysErrorPropagates(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{listErr: errors.New("list keys boom")})

	members, err := s.ListMembersByEmail(context.Background(), "user@example.com")
	require.Error(t, err)
	assert.Nil(t, members)
	assert.Contains(t, err.Error(), "list keys boom")
}

// TestStorage_ListMembersByEmail_PerKeyGetErrorPropagates verifies that a genuine read error
// (not a not-found / stale-key) is propagated rather than silently swallowed.
func TestStorage_ListMembersByEmail_PerKeyGetErrorPropagates(t *testing.T) {
	const email = "user@example.com"
	ctx := context.Background()
	emailHash := emailMember("", email).BuildEmailIndexKey(ctx)

	badKey := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, emailHash, "m-bad")

	s := newTestStorageWithKV(&mockKV{
		listKeys: []string{badKey},
		getErrs:  map[string]error{"m-bad": errors.New("nats: connection closed")},
	})

	_, err := s.ListMembersByEmail(ctx, email)
	require.Error(t, err, "a genuine read error must be propagated")
	assert.Contains(t, err.Error(), "nats: connection closed")
}

// TestStorage_ListMembersByEmail_DeletedMemberSkipped verifies that a not-found Get (stale index
// key pointing at a member that was deleted) is silently skipped, not returned as an error.
func TestStorage_ListMembersByEmail_DeletedMemberSkipped(t *testing.T) {
	const email = "user@example.com"
	ctx := context.Background()
	emailHash := emailMember("", email).BuildEmailIndexKey(ctx)

	goodKey := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, emailHash, "m-good")
	deletedKey := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, emailHash, "m-deleted")

	goodBytes, err := json.Marshal(emailMember("m-good", email))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys:   []string{goodKey, deletedKey},
		getEntries: map[string]jetstream.KeyValueEntry{"m-good": &mockEntry{value: goodBytes, rev: 1}},
		getErrs:    map[string]error{"m-deleted": jetstream.ErrKeyNotFound},
	})

	members, err := s.ListMembersByEmail(ctx, email)
	require.NoError(t, err, "a not-found (deleted member) must be silently skipped")
	require.Len(t, members, 1)
	assert.Equal(t, "m-good", members[0].UID)
}

// TestStorage_ListMembersByEmail_StaleIndexEntrySkipped verifies the defensive post-read check:
// when a stale index key (email-change cleanup lagged/failed) points at a member whose email no
// longer matches the query, that member is skipped. A member whose email still matches is returned.
func TestStorage_ListMembersByEmail_StaleIndexEntrySkipped(t *testing.T) {
	const queryEmail = "current@example.com"
	const staleEmail = "old@example.com"
	ctx := context.Background()
	emailHash := emailMember("", queryEmail).BuildEmailIndexKey(ctx)

	matchKey := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, emailHash, "m-match")
	staleKey := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, emailHash, "m-stale")

	matchBytes, err := json.Marshal(emailMember("m-match", queryEmail))
	require.NoError(t, err)
	// The stale member's record now has a DIFFERENT email than the index key it is listed under.
	staleBytes, err := json.Marshal(emailMember("m-stale", staleEmail))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys: []string{matchKey, staleKey},
		getEntries: map[string]jetstream.KeyValueEntry{
			"m-match": &mockEntry{value: matchBytes, rev: 1},
			"m-stale": &mockEntry{value: staleBytes, rev: 1},
		},
	})

	members, err := s.ListMembersByEmail(ctx, queryEmail)
	require.NoError(t, err)
	require.Len(t, members, 1, "the stale cross-email entry must be skipped")
	assert.Equal(t, "m-match", members[0].UID)
}
