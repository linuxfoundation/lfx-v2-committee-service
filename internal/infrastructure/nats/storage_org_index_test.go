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

// The mocks below embed the jetstream interfaces and override only the methods the by-organization
// index code paths exercise (Create / ListKeysFiltered / Get). Any other method call panics (nil
// embedded interface), which surfaces unexpected KV access in tests.

type mockKV struct {
	jetstream.KeyValue
	createErr  error
	listKeys   []string
	listErr    error
	getEntries map[string]jetstream.KeyValueEntry
	getErrs    map[string]error
}

func (m *mockKV) Create(_ context.Context, _ string, _ []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	return 1, m.createErr
}

func (m *mockKV) ListKeysFiltered(_ context.Context, _ ...string) (jetstream.KeyLister, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &mockKeyLister{keys: m.listKeys}, nil
}

func (m *mockKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	if err, ok := m.getErrs[key]; ok {
		return nil, err
	}
	if e, ok := m.getEntries[key]; ok {
		return e, nil
	}
	return nil, jetstream.ErrKeyNotFound
}

type mockKeyLister struct {
	keys []string
}

func (l *mockKeyLister) Keys() <-chan string {
	ch := make(chan string, len(l.keys))
	for _, k := range l.keys {
		ch <- k
	}
	close(ch)
	return ch
}

func (l *mockKeyLister) Stop() error { return nil }

type mockEntry struct {
	jetstream.KeyValueEntry
	value []byte
	rev   uint64
}

func (e *mockEntry) Value() []byte    { return e.value }
func (e *mockEntry) Revision() uint64 { return e.rev }

// newTestStorageWithKV builds a storage backed by the given mock KV for the committee-members bucket.
func newTestStorageWithKV(kv jetstream.KeyValue) *storage {
	return &storage{client: &NATSClient{
		kvStore: map[string]jetstream.KeyValue{constants.KVBucketNameCommitteeMembers: kv},
	}}
}

func orgMember(uid, orgSFID string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          uid,
		Organization: model.CommitteeMemberOrganization{ID: orgSFID},
	}}
}

// TestStorage_IndexMemberByOrganization_Idempotent verifies that a duplicate index write
// (jetstream.ErrKeyExists) is treated as success — the index entry already exists.
func TestStorage_IndexMemberByOrganization_Idempotent(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{createErr: jetstream.ErrKeyExists})

	key, err := s.IndexMemberByOrganization(context.Background(), orgMember("m-1", "001B000000IqhSLIAZ"))
	require.NoError(t, err, "ErrKeyExists must be treated as idempotent success")
	assert.NotEmpty(t, key)
}

// TestStorage_IndexMemberByOrganization_CreateErrorPropagates verifies that a non-ErrKeyExists error
// from Create is wrapped and returned (with the key still returned per the contract).
func TestStorage_IndexMemberByOrganization_CreateErrorPropagates(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{createErr: errors.New("kv create boom")})

	key, err := s.IndexMemberByOrganization(context.Background(), orgMember("m-1", "001B000000IqhSLIAZ"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kv create boom", "underlying Create error must be surfaced")
	assert.NotEmpty(t, key, "the index key is returned even on failure so callers can roll it back")
}

// TestStorage_IndexMemberByOrganization_NoOrgIsNoOp verifies a member without an organization id writes
// no index entry and returns an empty key (not an error).
func TestStorage_IndexMemberByOrganization_NoOrgIsNoOp(t *testing.T) {
	// createErr is set but must never be hit because no Create happens for an org-less member.
	s := newTestStorageWithKV(&mockKV{createErr: errors.New("should not be called")})

	key, err := s.IndexMemberByOrganization(context.Background(), orgMember("m-1", ""))
	require.NoError(t, err)
	assert.Empty(t, key)
}

// TestStorage_ListMembersByOrganization_ListKeysErrorPropagates verifies that an error from
// ListKeysFiltered is wrapped and returned.
func TestStorage_ListMembersByOrganization_ListKeysErrorPropagates(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{listErr: errors.New("list keys boom")})

	members, err := s.ListMembersByOrganization(context.Background(), "001B000000IqhSLIAZ")
	require.Error(t, err)
	assert.Nil(t, members)
	assert.Contains(t, err.Error(), "list keys boom")
}

// TestStorage_ListMembersByOrganization_PerKeyGetFailureSkipped verifies that a per-key Get failure is
// logged and skipped (best-effort) while the remaining members are still returned.
func TestStorage_ListMembersByOrganization_PerKeyGetFailureSkipped(t *testing.T) {
	const org = "001B000000IqhSLIAZ"
	goodKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, org, "m-good")
	badKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, org, "m-bad")

	goodBytes, err := json.Marshal(orgMember("m-good", org))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys: []string{goodKey, badKey},
		// s.get() reads by the member UID suffix, not the full index key.
		getEntries: map[string]jetstream.KeyValueEntry{"m-good": &mockEntry{value: goodBytes, rev: 1}},
		getErrs:    map[string]error{"m-bad": errors.New("get boom")},
	})

	members, err := s.ListMembersByOrganization(context.Background(), org)
	require.NoError(t, err, "a per-key Get failure must not fail the whole list")
	require.Len(t, members, 1, "the failed key is skipped; the good member is still returned")
	assert.Equal(t, "m-good", members[0].UID)
}

// TestStorage_ListMembersByOrganization_EmptyOrgIsValidationError verifies the empty-SFID guard.
func TestStorage_ListMembersByOrganization_EmptyOrgIsValidationError(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{})
	_, err := s.ListMembersByOrganization(context.Background(), "   ")
	require.Error(t, err)
}

// TestStorage_ListMembersByOrganization_StaleIndexEntrySkipped verifies the defensive post-read check:
// when a stale index key (org-change cleanup lagged/failed) points at a member whose organization.id no
// longer matches the requested org, that member is skipped so a seat never leaks across orgs. A member
// whose record still matches the requested org is still returned.
func TestStorage_ListMembersByOrganization_StaleIndexEntrySkipped(t *testing.T) {
	const reqOrg = "001B000000IqhSLIAZ"
	const otherOrg = "001C000000AbCdEFGH"
	matchKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, reqOrg, "m-match")
	staleKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, reqOrg, "m-stale")

	matchBytes, err := json.Marshal(orgMember("m-match", reqOrg))
	require.NoError(t, err)
	// The stale member's record now points at a DIFFERENT org than the index key it is listed under.
	staleBytes, err := json.Marshal(orgMember("m-stale", otherOrg))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys: []string{matchKey, staleKey},
		getEntries: map[string]jetstream.KeyValueEntry{
			"m-match": &mockEntry{value: matchBytes, rev: 1},
			"m-stale": &mockEntry{value: staleBytes, rev: 1},
		},
	})

	members, err := s.ListMembersByOrganization(context.Background(), reqOrg)
	require.NoError(t, err)
	require.Len(t, members, 1, "the stale cross-org entry must be skipped")
	assert.Equal(t, "m-match", members[0].UID)
}
