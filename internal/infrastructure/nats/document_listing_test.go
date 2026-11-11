// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

// keyEntry is a snapshot entry that carries only its key, as the MetaOnly watcher the listing uses delivers.
type keyEntry struct {
	jetstream.KeyValueEntry
	key string
}

func (e keyEntry) Key() string { return e.key }

// scriptedWatcher plays a fixed sequence of updates and then stops, the way the real watcher does: a nil entry is the
// sentinel that ends the initial snapshot, and a closed channel is what the library leaves behind when the subscription
// dies, so a listing that ends on a closed channel never saw the sentinel.
type scriptedWatcher struct {
	updates chan jetstream.KeyValueEntry
	stopped bool
}

func newScriptedWatcher(keys []string, complete bool) *scriptedWatcher {
	w := &scriptedWatcher{updates: make(chan jetstream.KeyValueEntry, len(keys)+1)}
	for _, key := range keys {
		w.updates <- keyEntry{key: key}
	}
	if complete {
		w.updates <- nil
	} else {
		close(w.updates)
	}
	return w
}

func (w *scriptedWatcher) Updates() <-chan jetstream.KeyValueEntry { return w.updates }
func (w *scriptedWatcher) Stop() error                             { w.stopped = true; return nil }

// watchedKV is a bucket whose watch plays the script, over the documents the embedded memKV holds.
type watchedKV struct {
	*memKV
	watcher *scriptedWatcher
}

func (k *watchedKV) WatchAll(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	return k.watcher, nil
}

func newDocumentBucket(t *testing.T, docs []*model.CommitteeDocument, extraKeys []string, complete bool) (*documentStorage, *scriptedWatcher) {
	t.Helper()
	kv := newMemKV()
	var keys []string
	for _, doc := range docs {
		data, err := json.Marshal(doc)
		require.NoError(t, err)
		kv.keys[doc.UID] = data
		keys = append(keys, doc.UID)
	}
	keys = append(keys, extraKeys...)
	watcher := newScriptedWatcher(keys, complete)
	client := newTestStorageWithBucket(constants.KVBucketNameCommitteeDocuments, &watchedKV{memKV: kv, watcher: watcher}).client
	return &documentStorage{client: client}, watcher
}

func TestDocumentStorage_ListAllDocuments(t *testing.T) {
	ctx := context.Background()
	folder := "folder-1"
	inFolder := &model.CommitteeDocument{UID: "doc-1", CommitteeUID: "committee-1", Name: "Charter", FolderUID: &folder}
	topLevel := &model.CommitteeDocument{UID: "doc-2", CommitteeUID: "committee-1", Name: "Minutes"}
	elsewhere := &model.CommitteeDocument{UID: "doc-3", CommitteeUID: "committee-2", Name: "Roadmap"}

	t.Run("lists the committee's documents when the snapshot is complete, skipping name lookup keys", func(t *testing.T) {
		ds, watcher := newDocumentBucket(t, []*model.CommitteeDocument{inFolder, topLevel, elsewhere}, []string{"lookup/committee-1/charter"}, true)

		docs, err := ds.ListAllDocuments(ctx, "committee-1")

		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"doc-1", "doc-2"}, []string{docs[0].UID, docs[1].UID})
		assert.True(t, watcher.stopped, "the watcher is stopped once the snapshot is read")
	})

	t.Run("lists every document when no committee is given", func(t *testing.T) {
		ds, _ := newDocumentBucket(t, []*model.CommitteeDocument{inFolder, topLevel, elsewhere}, nil, true)

		docs, err := ds.ListAllDocuments(ctx, "")

		require.NoError(t, err)
		assert.Len(t, docs, 3)
	})

	t.Run("an empty bucket is an empty list, not an error", func(t *testing.T) {
		ds, _ := newDocumentBucket(t, nil, nil, true)

		docs, err := ds.ListAllDocuments(ctx, "committee-1")

		require.NoError(t, err)
		assert.Empty(t, docs)
	})

	// The library closes the updates channel when the subscription dies, and a key lister reads that as the end of the
	// listing. A folder delete that trusted such a list would treat a folder as empty because its documents were never listed.
	t.Run("a snapshot that ends before its sentinel is an error, and keeps the documents it did receive", func(t *testing.T) {
		ds, watcher := newDocumentBucket(t, []*model.CommitteeDocument{inFolder}, nil, false)

		docs, err := ds.ListAllDocuments(ctx, "committee-1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "before it was complete")
		require.Len(t, docs, 1, "a document that was listed before the stream ended can still refuse a delete")
		assert.Equal(t, "doc-1", docs[0].UID)
		assert.True(t, watcher.stopped)
	})

	t.Run("a snapshot cut short with nothing received is an error, not an empty list", func(t *testing.T) {
		ds, _ := newDocumentBucket(t, nil, nil, false)

		docs, err := ds.ListAllDocuments(ctx, "committee-1")

		require.Error(t, err)
		assert.Empty(t, docs)
	})

	t.Run("a cancelled context is an error before the snapshot completes", func(t *testing.T) {
		kv := newMemKV()
		// A watcher that never delivers: no entries, no sentinel, and the channel stays open.
		watcher := &scriptedWatcher{updates: make(chan jetstream.KeyValueEntry)}
		client := newTestStorageWithBucket(constants.KVBucketNameCommitteeDocuments, &watchedKV{memKV: kv, watcher: watcher}).client
		ds := &documentStorage{client: client}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		_, err := ds.ListAllDocuments(cancelled, "committee-1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "cancelled")
	})
}
