// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func TestStorage_CreateLinkFolder(t *testing.T) {
	ctx := context.Background()
	folder := func(uid, name string) *model.CommitteeLinkFolder {
		return &model.CommitteeLinkFolder{UID: uid, CommitteeUID: "committee-1", Name: name}
	}

	t.Run("writes the record and the name lookup key", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeFolders, kv)
		f := folder("folder-1", "Meeting Notes")

		require.NoError(t, s.CreateLinkFolder(ctx, f))

		assert.Contains(t, kv.keys, "folder-1")
		assert.Equal(t, []byte("folder-1"), kv.keys[fmt.Sprintf(constants.KVLookupFolderPrefix, f.BuildIndexKey(ctx))])
	})

	t.Run("duplicate name in the same committee is a conflict and changes nothing", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeFolders, kv)
		require.NoError(t, s.CreateLinkFolder(ctx, folder("folder-1", "Meeting Notes")))
		keysBefore := len(kv.keys)

		err := s.CreateLinkFolder(ctx, folder("folder-2", "Meeting Notes"))

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.Len(t, kv.keys, keysBefore)
	})

	t.Run("a cancelled request still releases the lookup key", func(t *testing.T) {
		kv := newMemKV()
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		kv.cancel, kv.cancelOnCreate = cancel, "folder-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeFolders, kv)

		require.Error(t, s.CreateLinkFolder(reqCtx, folder("folder-1", "Meeting Notes")))

		assert.Empty(t, kv.keys, "the lookup key must not stay locked because the request was cancelled")
	})

	t.Run("failed record write releases the lookup key so the name is not locked", func(t *testing.T) {
		kv := newMemKV()
		kv.failCreateFor = "folder-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeFolders, kv)

		require.Error(t, s.CreateLinkFolder(ctx, folder("folder-1", "Meeting Notes")))
		assert.Empty(t, kv.keys)

		kv.failCreateFor = ""
		require.NoError(t, s.CreateLinkFolder(ctx, folder("folder-1", "Meeting Notes")))
	})
}
