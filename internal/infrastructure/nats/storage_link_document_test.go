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

func TestStorage_CreateDocumentMetadata(t *testing.T) {
	ctx := context.Background()
	newDocStorage := func(kv *memKV) *documentStorage {
		return &documentStorage{client: newTestStorageWithBucket(constants.KVBucketNameCommitteeDocuments, kv).client}
	}
	doc := func(uid, name string) *model.CommitteeDocument {
		return &model.CommitteeDocument{UID: uid, CommitteeUID: "committee-1", Name: name}
	}

	t.Run("writes the record and the name lookup key", func(t *testing.T) {
		kv := newMemKV()
		ds := newDocStorage(kv)
		d := doc("doc-1", "Charter")

		require.NoError(t, ds.CreateDocumentMetadata(ctx, d))

		assert.Contains(t, kv.keys, "doc-1")
		assert.Equal(t, []byte("doc-1"), kv.keys[fmt.Sprintf(constants.KVLookupDocumentPrefix, d.BuildIndexKey(ctx))])
	})

	t.Run("duplicate name in the same committee is a conflict and changes nothing", func(t *testing.T) {
		kv := newMemKV()
		ds := newDocStorage(kv)
		require.NoError(t, ds.CreateDocumentMetadata(ctx, doc("doc-1", "Charter")))
		keysBefore := len(kv.keys)

		err := ds.CreateDocumentMetadata(ctx, doc("doc-2", "Charter"))

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.Len(t, kv.keys, keysBefore)
	})

	t.Run("failed record write releases the lookup key so the name is not locked", func(t *testing.T) {
		kv := newMemKV()
		kv.failCreateFor = "doc-1"
		ds := newDocStorage(kv)

		require.Error(t, ds.CreateDocumentMetadata(ctx, doc("doc-1", "Charter")))
		assert.Empty(t, kv.keys)

		kv.failCreateFor = ""
		require.NoError(t, ds.CreateDocumentMetadata(ctx, doc("doc-1", "Charter")))
	})
}
