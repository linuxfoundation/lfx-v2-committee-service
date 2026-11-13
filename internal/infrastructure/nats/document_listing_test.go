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
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
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

// renameWriteKV makes the update of one record, the document's own, come back with an error, either after the update
// ran (a write whose reply was lost) or without running it, the two outcomes a caller cannot tell apart. The lookup
// keys go straight to the store. Once the update has been attempted, reading the record back can be made to fail too.
type renameWriteKV struct {
	jetstream.KeyValue
	recordKey string
	err       error
	applied   bool
	getErr    error
	attempted bool
	// beforeReadBack runs once, when the record is read back after the update was attempted: what another request does
	// between the failed write and the recovery.
	beforeReadBack func()
}

func (k *renameWriteKV) Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error) {
	if key != k.recordKey {
		return k.KeyValue.Update(ctx, key, value, revision)
	}
	k.attempted = true
	if k.applied {
		if _, err := k.KeyValue.Update(ctx, key, value, revision); err != nil {
			return 0, err
		}
	}
	return 0, k.err
}

func (k *renameWriteKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if key == k.recordKey && k.attempted && k.beforeReadBack != nil {
		hook := k.beforeReadBack
		k.beforeReadBack = nil
		hook()
	}
	if key == k.recordKey && k.attempted && k.getErr != nil {
		return nil, k.getErr
	}
	return k.KeyValue.Get(ctx, key)
}

// A rename whose write comes back with an error that is not a refusal (a client that disconnects mid-write is the usual
// cause) must not leave a name locked: the record is read back, and the keys follow what it holds.
func TestUpdateDocumentMetadata_RenameWriteError(t *testing.T) {
	ctx := context.Background()

	// setup creates the document "Minutes" and wraps the documents bucket so that its next update fails as the case says.
	setup := func(t *testing.T, wrap func(*renameWriteKV)) (*NATSClient, *model.CommitteeDocument, uint64, *renameWriteKV) {
		t.Helper()
		client := newKVClient(t)
		docs := NewDocumentStorage(client)
		require.NoError(t, docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "committee-1", "Minutes")))
		doc, revision, err := docs.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)

		real := client.kvStore[constants.KVBucketNameCommitteeDocuments]
		failing := &renameWriteKV{KeyValue: real, recordKey: "d-1"}
		wrap(failing)
		client.kvStore[constants.KVBucketNameCommitteeDocuments] = failing
		t.Cleanup(func() { client.kvStore[constants.KVBucketNameCommitteeDocuments] = real })
		return client, doc, revision, failing
	}
	nameHeld := func(t *testing.T, client *NATSClient, name string) bool {
		t.Helper()
		exists, err := NewDocumentStorage(client).DocumentNameExists(ctx, "committee-1", name)
		require.NoError(t, err)
		return exists
	}

	t.Run("a write that landed but was reported as failed is a success that releases the old name and keeps the new one", func(t *testing.T) {
		client, doc, revision, _ := setup(t, func(k *renameWriteKV) { k.applied, k.err = true, context.DeadlineExceeded })

		doc.Name = "Minutes 2026"
		err := NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision)

		require.NoError(t, err, "a write found to have landed is reported as done, so the update completes and is indexed")
		got, _, getErr := NewDocumentStorage(client).GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, getErr)
		assert.Equal(t, "Minutes 2026", got.Name, "the write did land")
		assert.True(t, nameHeld(t, client, "Minutes 2026"), "the record holds the new name")
		assert.False(t, nameHeld(t, client, "Minutes"), "the old name must not stay locked by a record that left it")
		require.NoError(t, NewDocumentStorage(client).CreateDocumentMetadata(ctx, storetest.NewDocument("d-2", "committee-1", "Minutes")), "the old name is free again")
	})

	t.Run("a write that may still land keeps the name reserved for it and says so", func(t *testing.T) {
		client, doc, revision, _ := setup(t, func(k *renameWriteKV) { k.err = context.DeadlineExceeded })

		doc.Name = "Minutes 2026"
		require.Error(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))

		got, _, getErr := NewDocumentStorage(client).GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, getErr)
		assert.Equal(t, "Minutes", got.Name, "the write did not land")
		assert.True(t, nameHeld(t, client, "Minutes"), "the record keeps its name")
		// The record is still at the revision the write was sent at, so the write could land later; releasing the
		// name now would let another document take it first.
		assert.True(t, nameHeld(t, client, "Minutes 2026"), "the name stays reserved while the write may still land")
	})

	t.Run("a write that can no longer land because the record moved on releases the reserved name", func(t *testing.T) {
		client, doc, revision, failing := setup(t, func(k *renameWriteKV) { k.err = context.Canceled })
		other := otherStorage(t, client)
		failing.beforeReadBack = func() {
			// Another request changes only the description: the record is at a later revision, so the earlier write,
			// which needs the revision it was sent at, can never apply.
			latest, rev, err := other.GetDocumentMetadata(ctx, "committee-1", "d-1")
			require.NoError(t, err)
			latest.Description = "edited meanwhile"
			require.NoError(t, other.UpdateDocumentMetadata(ctx, latest, rev))
		}

		doc.Name = "Minutes 2026"
		require.Error(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))

		assert.True(t, nameHeld(t, client, "Minutes"))
		assert.False(t, nameHeld(t, client, "Minutes 2026"), "the write cannot land any more, so its name is free")
	})

	t.Run("overlapping renames leave no name locked: A to B lands, B to C lands, then A to B is settled", func(t *testing.T) {
		client, doc, revision, failing := setup(t, func(k *renameWriteKV) { k.applied, k.err = true, context.DeadlineExceeded })
		other := otherStorage(t, client)
		failing.beforeReadBack = func() {
			latest, rev, err := other.GetDocumentMetadata(ctx, "committee-1", "d-1")
			require.NoError(t, err)
			latest.Name = "Minutes Final"
			require.NoError(t, other.UpdateDocumentMetadata(ctx, latest, rev))
		}

		doc.Name = "Minutes 2026"
		require.Error(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision), "the record was changed again, so this write is not reported as the outcome")

		got, _, getErr := NewDocumentStorage(client).GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, getErr)
		assert.Equal(t, "Minutes Final", got.Name)
		assert.True(t, nameHeld(t, client, "Minutes Final"))
		assert.False(t, nameHeld(t, client, "Minutes 2026"), "the intermediate name is free")
		assert.False(t, nameHeld(t, client, "Minutes"), "the original name must not stay locked by a record that left it")
	})

	t.Run("recovery does not free a name the record took back in the meantime", func(t *testing.T) {
		client, doc, revision, failing := setup(t, func(k *renameWriteKV) { k.applied, k.err = true, context.DeadlineExceeded })
		other := otherStorage(t, client)
		failing.beforeReadBack = func() {
			// B to C, then C back to B: the key for B is released and reserved again by another request.
			for _, name := range []string{"Minutes Final", "Minutes 2026"} {
				latest, rev, err := other.GetDocumentMetadata(ctx, "committee-1", "d-1")
				require.NoError(t, err)
				latest.Name = name
				require.NoError(t, other.UpdateDocumentMetadata(ctx, latest, rev))
			}
		}

		doc.Name = "Minutes 2026"
		_ = NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision)

		assert.True(t, nameHeld(t, client, "Minutes 2026"), "the record holds this name, so its key must survive the recovery")
		assert.False(t, nameHeld(t, client, "Minutes Final"))
		assert.False(t, nameHeld(t, client, "Minutes"))
		err := NewDocumentStorage(client).CreateDocumentMetadata(ctx, storetest.NewDocument("d-2", "committee-1", "Minutes 2026"))
		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict, "another document still cannot take the name")
	})

	t.Run("a write the server refuses outright releases the reserved name and changes nothing", func(t *testing.T) {
		client, doc, revision, _ := setup(t, func(k *renameWriteKV) { k.err = fmt.Errorf("update: %w", jetstream.ErrKeyExists) })

		doc.Name = "Minutes 2026"
		err := NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision)

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.False(t, nameHeld(t, client, "Minutes 2026"), "a refused rename must not leave its name locked")
		assert.True(t, nameHeld(t, client, "Minutes"))
	})

	t.Run("when the record cannot be read back both names are kept rather than guessed at", func(t *testing.T) {
		client, doc, revision, _ := setup(t, func(k *renameWriteKV) {
			k.err, k.getErr = context.DeadlineExceeded, errors.New("kv unavailable")
		})

		doc.Name = "Minutes 2026"
		require.Error(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))

		// Either name may be the record's, so neither key can be freed without risking a name that is in use.
		assert.True(t, nameHeld(t, client, "Minutes"))
		assert.True(t, nameHeld(t, client, "Minutes 2026"))
	})
}

// otherDocs is a second connection to the same server, for the requests that run while a faulted one is mid-flight.
type otherDocs struct {
	port.CommitteeDocumentReaderWriter
	sync *storage
}

func (o otherDocs) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	return o.sync.UpdateDocumentMetadata(ctx, doc, revision)
}

func otherStorage(t *testing.T, client *NATSClient) otherDocs {
	t.Helper()
	other, err := NewClient(context.Background(), client.config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	return otherDocs{NewDocumentStorage(other), NewStorageBackend(other)}
}

// lostReservationKV stores the reservation for one lookup key and then reports an error, as a Create whose reply was
// lost does. value, when set, is what is stored instead of the caller's, to stand for a reservation another attempt made.
type lostReservationKV struct {
	jetstream.KeyValue
	lostKey string
	value   string
	err     error
}

func (k *lostReservationKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	if key != k.lostKey || k.err == nil {
		return k.KeyValue.Create(ctx, key, value, opts...)
	}
	stored := value
	if k.value != "" {
		stored = []byte(k.value)
	}
	if _, err := k.KeyValue.Create(ctx, key, stored, opts...); err != nil {
		return 0, err
	}
	return 0, k.err
}

// A reservation that was stored but whose reply was lost must not lock the name against the document's own retry.
func TestUpdateDocumentMetadata_LostReservationReply(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*NATSClient, *model.CommitteeDocument, uint64, *lostReservationKV) {
		t.Helper()
		client := newKVClient(t)
		docs := NewDocumentStorage(client)
		require.NoError(t, docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "committee-1", "Minutes")))
		doc, revision, err := docs.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)
		probe := &model.CommitteeDocument{CommitteeUID: "committee-1", Name: "Minutes 2026"}
		real := client.kvStore[constants.KVBucketNameCommitteeDocuments]
		lost := &lostReservationKV{KeyValue: real, lostKey: fmt.Sprintf(constants.KVLookupDocumentPrefix, probe.BuildIndexKey(ctx)), err: context.DeadlineExceeded}
		client.kvStore[constants.KVBucketNameCommitteeDocuments] = lost
		t.Cleanup(func() { client.kvStore[constants.KVBucketNameCommitteeDocuments] = real })
		return client, doc, revision, lost
	}
	nameHeld := func(t *testing.T, client *NATSClient, name string) bool {
		t.Helper()
		exists, err := NewDocumentStorage(client).DocumentNameExists(ctx, "committee-1", name)
		require.NoError(t, err)
		return exists
	}

	t.Run("the key this attempt stored is released, so the same rename can be retried", func(t *testing.T) {
		client, doc, revision, lost := setup(t)

		doc.Name = "Minutes 2026"
		require.Error(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))
		assert.False(t, nameHeld(t, client, "Minutes 2026"), "a reservation this attempt made must not stay locked")
		assert.True(t, nameHeld(t, client, "Minutes"))

		lost.err = nil
		require.NoError(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))
		assert.True(t, nameHeld(t, client, "Minutes 2026"))
		assert.False(t, nameHeld(t, client, "Minutes"))
	})

	t.Run("a key that holds the document's uid but not this attempt's token is left alone", func(t *testing.T) {
		client, doc, revision, lost := setup(t)
		lost.value = "d-1#another-attempt"

		doc.Name = "Minutes 2026"
		require.Error(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))
		assert.True(t, nameHeld(t, client, "Minutes 2026"), "another attempt's reservation is not this one's to release")
	})
}

// The reservation a rename makes carries a token, which must not stop the document's delete from releasing the name.
func TestDeleteDocumentMetadata_ReleasesRenamedName(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	docs := NewDocumentStorage(client)
	require.NoError(t, docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "committee-1", "Minutes")))
	doc, revision, err := docs.GetDocumentMetadata(ctx, "committee-1", "d-1")
	require.NoError(t, err)
	doc.Name = "Minutes 2026"
	require.NoError(t, NewStorageBackend(client).UpdateDocumentMetadata(ctx, doc, revision))
	_, revision, err = docs.GetDocumentMetadata(ctx, "committee-1", "d-1")
	require.NoError(t, err)

	require.NoError(t, docs.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision))

	exists, err := docs.DocumentNameExists(ctx, "committee-1", "Minutes 2026")
	require.NoError(t, err)
	assert.False(t, exists, "a deleted document must not leave its renamed name locked")
}
