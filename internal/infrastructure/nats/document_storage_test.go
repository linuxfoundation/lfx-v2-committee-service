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

	t.Run("ambiguous write error keeps the lookup key so a duplicate cannot be created", func(t *testing.T) {
		kv := newMemKV()
		kv.ambiguousCreateFor = "doc-1"
		ds := newDocStorage(kv)
		d := doc("doc-1", "Charter")

		require.Error(t, ds.CreateDocumentMetadata(ctx, d))

		assert.Contains(t, kv.keys, fmt.Sprintf(constants.KVLookupDocumentPrefix, d.BuildIndexKey(ctx)))

		var conflict errs.Conflict
		assert.ErrorAs(t, ds.CreateDocumentMetadata(ctx, doc("doc-2", "Charter")), &conflict)
	})

	t.Run("a cancelled request still releases the lookup key", func(t *testing.T) {
		kv := newMemKV()
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		kv.cancel, kv.cancelOnCreate = cancel, "doc-1"
		ds := newDocStorage(kv)

		require.Error(t, ds.CreateDocumentMetadata(reqCtx, doc("doc-1", "Charter")))

		assert.Empty(t, kv.keys, "the lookup key must not stay locked because the request was cancelled")
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

func TestDocumentStorage_DocumentNameExists(t *testing.T) {
	ctx := context.Background()
	kv := newMemKV()
	ds := &documentStorage{client: newTestStorageWithBucket(constants.KVBucketNameCommitteeDocuments, kv).client}

	exists, err := ds.DocumentNameExists(ctx, "committee-1", "Charter")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, ds.CreateDocumentMetadata(ctx, &model.CommitteeDocument{UID: "doc-1", CommitteeUID: "committee-1", Name: "Charter"}))

	exists, err = ds.DocumentNameExists(ctx, "committee-1", "Charter")
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = ds.DocumentNameExists(ctx, "committee-2", "Charter")
	require.NoError(t, err)
	assert.False(t, exists, "the same name in another committee is a different key")
}
