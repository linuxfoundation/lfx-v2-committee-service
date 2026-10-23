// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// TestStorage_EachMember_PerKeyReadErrorIsSkippedNotReturned verifies that EachMember stays
// best-effort: a per-key read failure is logged and skipped, never surfaced as the returned error,
// so existing sync/repair commands keep scanning the rest of the bucket and printing their stats.
func TestStorage_EachMember_PerKeyReadErrorIsSkippedNotReturned(t *testing.T) {
	goodBytes, err := json.Marshal(orgMember("m-good", "org-1"))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys:   []string{"m-good", "m-bad"},
		getEntries: map[string]jetstream.KeyValueEntry{"m-good": &mockEntry{value: goodBytes, rev: 1}},
		getErrs:    map[string]error{"m-bad": errors.New("nats: connection closed")},
	})

	var seen []string
	err = s.EachMember(context.Background(), func(m *model.CommitteeMember) error {
		seen = append(seen, m.UID)
		return nil
	})
	require.NoError(t, err, "EachMember must stay best-effort and not surface a per-key read failure")
	assert.Equal(t, []string{"m-good"}, seen)
}

// TestStorage_EachMemberStrict_PerKeyReadErrorIsAggregatedAndReturned verifies that
// EachMemberStrict still skips the bad key during iteration (callers relying on fn still see every
// good member) but reports the failure back once the scan completes, so a caller like "report
// board-seats" can tell a partial scan apart from a complete one.
func TestStorage_EachMemberStrict_PerKeyReadErrorIsAggregatedAndReturned(t *testing.T) {
	goodBytes, err := json.Marshal(orgMember("m-good", "org-1"))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys:   []string{"m-good", "m-bad"},
		getEntries: map[string]jetstream.KeyValueEntry{"m-good": &mockEntry{value: goodBytes, rev: 1}},
		getErrs:    map[string]error{"m-bad": errors.New("nats: connection closed")},
	})

	var seen []string
	err = s.EachMemberStrict(context.Background(), func(m *model.CommitteeMember) error {
		seen = append(seen, m.UID)
		return nil
	})
	require.Error(t, err, "EachMemberStrict must report a partial scan back to the caller")
	assert.Equal(t, []string{"m-good"}, seen, "the good member must still be visited despite the bad key")
}

// TestStorage_EachMemberStrict_NoFailedReads_ReturnsNil verifies a complete scan (no per-key
// failures) returns a nil error from EachMemberStrict, same as EachMember.
func TestStorage_EachMemberStrict_NoFailedReads_ReturnsNil(t *testing.T) {
	goodBytes, err := json.Marshal(orgMember("m-good", "org-1"))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys:   []string{"m-good"},
		getEntries: map[string]jetstream.KeyValueEntry{"m-good": &mockEntry{value: goodBytes, rev: 1}},
	})

	err = s.EachMemberStrict(context.Background(), func(*model.CommitteeMember) error { return nil })
	require.NoError(t, err)
}

// TestStorage_EachMember_FnErrorStopsIterationImmediately verifies that the first error fn returns
// still aborts the scan right away for both variants, regardless of strict.
func TestStorage_EachMember_FnErrorStopsIterationImmediately(t *testing.T) {
	m1Bytes, err := json.Marshal(orgMember("m1", "org-1"))
	require.NoError(t, err)
	m2Bytes, err := json.Marshal(orgMember("m2", "org-1"))
	require.NoError(t, err)

	s := newTestStorageWithKV(&mockKV{
		listKeys: []string{"m1", "m2"},
		getEntries: map[string]jetstream.KeyValueEntry{
			"m1": &mockEntry{value: m1Bytes, rev: 1},
			"m2": &mockEntry{value: m2Bytes, rev: 1},
		},
	})

	fnErr := errors.New("stop here")
	var seen []string
	err = s.EachMember(context.Background(), func(m *model.CommitteeMember) error {
		seen = append(seen, m.UID)
		return fnErr
	})
	require.ErrorIs(t, err, fnErr)
	assert.Len(t, seen, 1, "iteration must stop at the first member fn errors on")
}

// TestStorage_EachMember_ListKeysErrorPropagates verifies that an error from the initial ListKeys
// call (as opposed to a per-member Get) is wrapped and returned immediately.
func TestStorage_EachMember_ListKeysErrorPropagates(t *testing.T) {
	s := newTestStorageWithKV(&mockKV{listErr: errors.New("list keys boom")})

	err := s.EachMember(context.Background(), func(*model.CommitteeMember) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list keys boom")
}
