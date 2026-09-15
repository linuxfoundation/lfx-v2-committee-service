// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// newTestStorageWithCommitteesKV builds a storage backed by the given mock KV for the
// committees bucket, which is where both the project+name index key and the primary
// committee record live.
func newTestStorageWithCommitteesKV(kv jetstream.KeyValue) *storage {
	return &storage{client: &NATSClient{
		kvStore: map[string]jetstream.KeyValue{constants.KVBucketNameCommittees: kv},
	}}
}

// TestStorage_FindUIDByProjectAndName_Match verifies the happy path: the index key
// resolves to a UID whose primary record still has the requested project/name.
func TestStorage_FindUIDByProjectAndName_Match(t *testing.T) {
	ctx := context.Background()
	const projectUID = "project-1"
	const name = "Technical Steering Committee"

	indexKey := fmt.Sprintf(constants.KVLookupPrefix,
		(&model.Committee{CommitteeBase: model.CommitteeBase{ProjectUID: projectUID, Name: name}}).BuildIndexKey(ctx))

	baseBytes, err := json.Marshal(model.CommitteeBase{UID: "c-1", ProjectUID: projectUID, Name: name})
	require.NoError(t, err)

	s := newTestStorageWithCommitteesKV(&mockKV{
		getEntries: map[string]jetstream.KeyValueEntry{
			indexKey: &mockEntry{value: []byte("c-1"), rev: 1},
			"c-1":    &mockEntry{value: baseBytes, rev: 1},
		},
	})

	uid, err := s.FindUIDByProjectAndName(ctx, projectUID, name)
	require.NoError(t, err)
	assert.Equal(t, "c-1", uid)
}

// TestStorage_FindUIDByProjectAndName_NoIndexEntry verifies the plain miss case: no
// index key exists at all.
func TestStorage_FindUIDByProjectAndName_NoIndexEntry(t *testing.T) {
	s := newTestStorageWithCommitteesKV(&mockKV{})

	_, err := s.FindUIDByProjectAndName(context.Background(), "project-1", "Nonexistent Committee")
	require.Error(t, err)
	var nf errs.NotFound
	assert.ErrorAs(t, err, &nf)
}

// TestStorage_FindUIDByProjectAndName_StaleIndexDeletedCommittee verifies the defensive
// post-read check this PR review comment requested: an index key that still points at a
// UID whose primary record no longer exists (delete's secondary-index cleanup is
// best-effort and can lag or fail) must report "not found", not report the deleted
// committee as existing.
func TestStorage_FindUIDByProjectAndName_StaleIndexDeletedCommittee(t *testing.T) {
	ctx := context.Background()
	const projectUID = "project-1"
	const name = "Technical Steering Committee"

	indexKey := fmt.Sprintf(constants.KVLookupPrefix,
		(&model.Committee{CommitteeBase: model.CommitteeBase{ProjectUID: projectUID, Name: name}}).BuildIndexKey(ctx))

	s := newTestStorageWithCommitteesKV(&mockKV{
		getEntries: map[string]jetstream.KeyValueEntry{
			indexKey: &mockEntry{value: []byte("c-deleted"), rev: 1},
		},
		getErrs: map[string]error{
			"c-deleted": jetstream.ErrKeyNotFound,
		},
	})

	_, err := s.FindUIDByProjectAndName(ctx, projectUID, name)
	require.Error(t, err, "a stale index key pointing at a deleted committee must not report Exists=true")
	var nf errs.NotFound
	assert.ErrorAs(t, err, &nf)
}

// TestStorage_FindUIDByProjectAndName_StaleIndexRenamedCommittee verifies the second
// stale-index case: the index key still exists and its target UID still exists, but the
// primary record's project/name no longer matches what the index key represents (the
// committee was renamed/reparented and old-index cleanup lagged or failed).
func TestStorage_FindUIDByProjectAndName_StaleIndexRenamedCommittee(t *testing.T) {
	ctx := context.Background()
	const projectUID = "project-1"
	const oldName = "Old Committee Name"
	const newName = "New Committee Name"

	oldIndexKey := fmt.Sprintf(constants.KVLookupPrefix,
		(&model.Committee{CommitteeBase: model.CommitteeBase{ProjectUID: projectUID, Name: oldName}}).BuildIndexKey(ctx))

	// The primary record now has the NEW name, but the OLD index key was never cleaned up.
	baseBytes, err := json.Marshal(model.CommitteeBase{UID: "c-1", ProjectUID: projectUID, Name: newName})
	require.NoError(t, err)

	s := newTestStorageWithCommitteesKV(&mockKV{
		getEntries: map[string]jetstream.KeyValueEntry{
			oldIndexKey: &mockEntry{value: []byte("c-1"), rev: 1},
			"c-1":       &mockEntry{value: baseBytes, rev: 1},
		},
	})

	_, err = s.FindUIDByProjectAndName(ctx, projectUID, oldName)
	require.Error(t, err, "a stale index key pointing at a renamed committee must not report Exists=true")
	var nf errs.NotFound
	assert.ErrorAs(t, err, &nf)
}
