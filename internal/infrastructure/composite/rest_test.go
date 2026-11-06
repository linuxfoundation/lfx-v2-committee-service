// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// newMetricsReader returns metrics that count into a reader the test can read.
func newMetricsReader() (*Metrics, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	return newMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")), reader
}

// linkRig is a composite of the links and folders over a real KV store and a real PostgreSQL store.
type linkRig struct {
	links  *Links
	kv     natstest.Backend
	pg     *postgres.Store
	reader *sdkmetric.ManualReader
}

func newLinkRig(t *testing.T, mode Mode, wrapPG func(LinkBackend) LinkBackend, opts ...Option) *linkRig {
	t.Helper()
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)
	metrics, reader := newMetricsReader()
	var used LinkBackend = pg
	if wrapPG != nil {
		used = wrapPG(pg)
	}
	links, err := NewLinks(mode, kv, used, append([]Option{WithMetrics(metrics)}, opts...)...)
	require.NoError(t, err)
	return &linkRig{links: links, kv: kv, pg: pg, reader: reader}
}

func (r *linkRig) counts(t *testing.T, name string) map[string]int64 {
	t.Helper()
	return countsOf(t, r.reader, name)
}

// linkStore is the composite as the link suites want it: the links and folders, and the audit-sync methods of the documents,
// which another composite owns and which go to the store the mode makes primary.
type linkStore struct {
	*Links
	docs interface {
		ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
		GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
		UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error
	}
}

func (s linkStore) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	return s.docs.ListAllDocuments(ctx, committeeUID)
}

func (s linkStore) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	return s.docs.GetDocumentMetadata(ctx, committeeUID, documentUID)
}

func (s linkStore) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	return s.docs.UpdateDocumentMetadata(ctx, doc, revision)
}

// Whatever the mode, the composite behaves as one store does for the links and the folders: the same suites the KV store and
// the PostgreSQL store pass. In dual mode every read of the suite was compared and every write copied, and the stores never
// disagreed and nothing failed.
func TestContract_Links(t *testing.T) {
	suites := map[string]func(*testing.T, storetest.Backend, storetest.LinkFactory){
		"links": storetest.RunLinkContract, "folders": storetest.RunFolderContract,
	}
	for name, run := range suites {
		for _, mode := range []Mode{ModeKV, ModeDual, ModePostgres} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				run(t, storetest.Backend{Name: "composite-" + string(mode)}, func(t *testing.T) storetest.LinkStore {
					r := newLinkRig(t, mode, nil, WithSampleRate(1))
					t.Cleanup(func() {
						assert.Empty(t, r.counts(t, mismatches), "no false mismatch")
						assert.Empty(t, r.counts(t, compareErr))
						assert.Empty(t, r.counts(t, failures))
					})
					if mode == ModePostgres {
						return linkStore{Links: r.links, docs: r.pg}
					}
					return linkStore{Links: r.links, docs: r.kv}
				})
			})
		}
	}
}

// linkKind is one kind of record the composite of the links copies.
type linkKind struct {
	name string
	// create makes a record through the composite and returns its uid.
	create func(t *testing.T, r *linkRig) string
	// update changes the record through the composite.
	update func(t *testing.T, r *linkRig, uid string)
	// remove deletes it through the composite.
	remove func(t *testing.T, r *linkRig, uid string)
	// read returns what a store holds for the uid, or the error.
	read func(store LinkBackend, uid string) (any, uint64, error)
	// createOp, updateOp and removeOp name what is counted when the copy fails.
	createOp, updateOp, removeOp string
}

func linkKinds() []linkKind {
	ctx := context.Background()
	return []linkKind{
		{
			name: "link",
			create: func(t *testing.T, r *linkRig) string {
				require.NoError(t, r.links.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
				return "l-1"
			},
			update: func(t *testing.T, r *linkRig, uid string) {
				link, revision, err := r.links.GetLink(ctx, "uid-1", uid)
				require.NoError(t, err)
				link.Name += " changed"
				require.NoError(t, r.links.UpdateLink(ctx, link, revision))
			},
			remove: func(t *testing.T, r *linkRig, uid string) {
				_, revision, err := r.links.GetLink(ctx, "uid-1", uid)
				require.NoError(t, err)
				require.NoError(t, r.links.DeleteLink(ctx, "uid-1", uid, revision))
			},
			read:     func(s LinkBackend, uid string) (any, uint64, error) { return s.GetLink(ctx, "uid-1", uid) },
			createOp: "create_link", updateOp: "update_link", removeOp: "delete_link",
		},
		{
			name: "folder",
			create: func(t *testing.T, r *linkRig) string {
				require.NoError(t, r.links.CreateLinkFolder(ctx, storetest.NewFolder("f-1", "uid-1", "Charters")))
				return "f-1"
			},
			update: func(t *testing.T, r *linkRig, uid string) {
				folder, revision, err := r.links.GetLinkFolder(ctx, "uid-1", uid)
				require.NoError(t, err)
				folder.Name += " changed"
				require.NoError(t, r.links.UpdateLinkFolder(ctx, folder, revision))
			},
			remove: func(t *testing.T, r *linkRig, uid string) {
				_, revision, err := r.links.GetLinkFolder(ctx, "uid-1", uid)
				require.NoError(t, err)
				require.NoError(t, r.links.DeleteLinkFolder(ctx, "uid-1", uid, revision))
			},
			read:     func(s LinkBackend, uid string) (any, uint64, error) { return s.GetLinkFolder(ctx, "uid-1", uid) },
			createOp: "create_link_folder", updateOp: "update_link_folder", removeOp: "delete_link_folder",
		},
	}
}

func sameLinkRecord(t *testing.T, k linkKind, r *linkRig, uid string) {
	t.Helper()
	kv, kvRevision, err := k.read(r.kv, uid)
	require.NoError(t, err)
	pg, pgRevision, err := k.read(r.pg, uid)
	require.NoError(t, err)
	assert.Equal(t, kvRevision, pgRevision, "the revision is the one KV gave it")
	kvDigest, err := Digest(kv)
	require.NoError(t, err)
	pgDigest, err := Digest(pg)
	require.NoError(t, err)
	assert.Equal(t, kvDigest, pgDigest, "the record is the same")
}

func goneFromLinks(k linkKind, store LinkBackend, uid string) bool {
	_, _, err := k.read(store, uid)
	return isNotFound(err)
}

func TestLinks_EachModeWritesToItsStores(t *testing.T) {
	for _, k := range linkKinds() {
		for mode, want := range map[Mode]struct{ kv, pg bool }{ModeKV: {true, false}, ModeDual: {true, true}, ModePostgres: {false, true}} {
			t.Run(k.name+"/"+string(mode), func(t *testing.T) {
				r := newLinkRig(t, mode, nil)

				uid := k.create(t, r)

				assert.Equal(t, want.kv, !goneFromLinks(k, r.kv, uid), "KV holds the %s", k.name)
				assert.Equal(t, want.pg, !goneFromLinks(k, r.pg, uid), "PostgreSQL holds the %s", k.name)
			})
		}
	}
}

func TestLinks_DualWritesCarryTheKVRevisionToPostgreSQL(t *testing.T) {
	for _, k := range linkKinds() {
		t.Run(k.name, func(t *testing.T) {
			r := newLinkRig(t, ModeDual, nil)

			uid := k.create(t, r)
			sameLinkRecord(t, k, r, uid)
			_, created, err := k.read(r.pg, uid)
			require.NoError(t, err)

			k.update(t, r, uid)
			sameLinkRecord(t, k, r, uid)
			_, updated, err := k.read(r.pg, uid)
			require.NoError(t, err)
			assert.Greater(t, updated, created, "a token from before the update is stale in both stores")

			k.remove(t, r, uid)
			assert.True(t, goneFromLinks(k, r.kv, uid) && goneFromLinks(k, r.pg, uid), "the %s is gone from both", k.name)
			assert.Empty(t, r.counts(t, failures), "nothing failed on the way")
		})
	}
}

// failingLinkPG is a PostgreSQL store whose writes of the links and folders fail.
type failingLinkPG struct{ LinkBackend }

func (failingLinkPG) CreateLink(context.Context, *model.CommitteeLink) error { return errPG }
func (failingLinkPG) UpdateLink(context.Context, *model.CommitteeLink, uint64) error {
	return errPG
}
func (failingLinkPG) DeleteLink(context.Context, string, string, uint64) error { return errPG }
func (failingLinkPG) CreateLinkFolder(context.Context, *model.CommitteeLinkFolder) error {
	return errPG
}
func (failingLinkPG) UpdateLinkFolder(context.Context, *model.CommitteeLinkFolder, uint64) error {
	return errPG
}
func (failingLinkPG) DeleteLinkFolder(context.Context, string, string, uint64) error { return errPG }

func TestLinks_DualASecondaryFailureIsCountedAndNeverFailsTheRequest(t *testing.T) {
	for _, k := range linkKinds() {
		t.Run(k.name, func(t *testing.T) {
			r := newLinkRig(t, ModeDual, func(pg LinkBackend) LinkBackend { return failingLinkPG{pg} })

			uid := k.create(t, r)
			k.update(t, r, uid)
			k.remove(t, r, uid)

			assert.Equal(t, map[string]int64{k.createOp: 1, k.updateOp: 1, k.removeOp: 1}, r.counts(t, failures))
			assert.True(t, goneFromLinks(k, r.kv, uid), "KV decided every one of them, the delete included")
		})
	}
}

// flakyLinkPG is a PostgreSQL store whose updates of the links and folders fail while fail is set.
type flakyLinkPG struct {
	LinkBackend
	fail *atomic.Bool
}

func (f flakyLinkPG) UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error {
	if f.fail.Load() {
		return errPG
	}
	return f.LinkBackend.UpdateLink(ctx, link, revision)
}

func (f flakyLinkPG) UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error {
	if f.fail.Load() {
		return errPG
	}
	return f.LinkBackend.UpdateLinkFolder(ctx, folder, revision)
}

// A copy that was missed is made up for by the next update of the same record, which writes what KV holds then.
func TestLinks_DualTheNextUpdateBringsPostgreSQLUpToWhatKVHolds(t *testing.T) {
	for _, k := range linkKinds() {
		t.Run(k.name, func(t *testing.T) {
			var fail atomic.Bool
			r := newLinkRig(t, ModeDual, func(pg LinkBackend) LinkBackend { return flakyLinkPG{pg, &fail} })
			uid := k.create(t, r)

			fail.Store(true)
			k.update(t, r, uid)
			assert.Equal(t, map[string]int64{k.updateOp: 1}, r.counts(t, failures), "the copy was missed")

			fail.Store(false)
			k.update(t, r, uid)

			assert.Equal(t, map[string]int64{k.updateOp: 1}, r.counts(t, failures), "nothing more failed")
			sameLinkRecord(t, k, r, uid)
		})
	}
}

// deletingLinkKV is a KV store that deletes the link or the folder as the copy of its creation reads it back, which is where a
// delete that finishes while a create is being copied lands.
type deletingLinkKV struct {
	LinkBackend
	once sync.Once
}

func (d *deletingLinkKV) GetLink(ctx context.Context, committeeUID, uid string) (*model.CommitteeLink, uint64, error) {
	link, revision, err := d.LinkBackend.GetLink(ctx, committeeUID, uid)
	d.once.Do(func() { _ = d.DeleteLink(ctx, committeeUID, uid, revision) })
	return link, revision, err
}

func (d *deletingLinkKV) GetLinkFolder(ctx context.Context, committeeUID, uid string) (*model.CommitteeLinkFolder, uint64, error) {
	folder, revision, err := d.LinkBackend.GetLinkFolder(ctx, committeeUID, uid)
	d.once.Do(func() { _ = d.DeleteLinkFolder(ctx, committeeUID, uid, revision) })
	return folder, revision, err
}

// A delete that finishes in KV while a create is being copied finds nothing to remove in PostgreSQL, so the copy must not
// leave the record behind.
func TestLinks_DualACreateCopiedWhileADeleteFinishesDoesNotBringTheRecordBack(t *testing.T) {
	for _, k := range linkKinds() {
		t.Run(k.name, func(t *testing.T) {
			r := newLinkRig(t, ModeDual, nil)
			r.links.kv = &deletingLinkKV{LinkBackend: r.kv} // what the copy reads from; the primary store is still the real one

			uid := k.create(t, r)

			assert.True(t, goneFromLinks(k, r.kv, uid) && goneFromLinks(k, r.pg, uid), "the %s is in neither store", k.name)
			assert.Empty(t, r.counts(t, failures))
		})
	}
}

// A record that PostgreSQL holds at another revision, because an earlier copy failed, is still removed by a delete.
func TestLinks_DualADeleteRemovesWhatPostgreSQLHoldsWhateverItsRevision(t *testing.T) {
	ctx := context.Background()
	r := newLinkRig(t, ModeDual, nil)
	require.NoError(t, r.links.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
	_, revision, err := r.links.GetLink(ctx, "uid-1", "l-1")
	require.NoError(t, err)
	behind, pgRevision, err := r.pg.GetLink(ctx, "uid-1", "l-1")
	require.NoError(t, err)
	behind.Name = "an update PostgreSQL has and KV has not"
	wctx, err := postgres.WithRevision(ctx, pgRevision+1000)
	require.NoError(t, err)
	require.NoError(t, r.pg.UpdateLink(wctx, behind, pgRevision))

	require.NoError(t, r.links.DeleteLink(ctx, "uid-1", "l-1", revision))

	_, _, pgErr := r.pg.GetLink(ctx, "uid-1", "l-1")
	assert.True(t, isNotFound(pgErr))
	assert.Empty(t, r.counts(t, failures))
}

// A folder that PostgreSQL holds at another revision, because an earlier copy failed, is still removed by a delete.
func TestLinks_DualADeleteOfAFolderRemovesWhatPostgreSQLHoldsWhateverItsRevision(t *testing.T) {
	ctx := context.Background()
	r := newLinkRig(t, ModeDual, nil)
	require.NoError(t, r.links.CreateLinkFolder(ctx, storetest.NewFolder("f-1", "uid-1", "Charters")))
	_, revision, err := r.links.GetLinkFolder(ctx, "uid-1", "f-1")
	require.NoError(t, err)
	behind, pgRevision, err := r.pg.GetLinkFolder(ctx, "uid-1", "f-1")
	require.NoError(t, err)
	behind.Name = "An update PostgreSQL has and KV has not"
	wctx, err := postgres.WithRevision(ctx, pgRevision+1000)
	require.NoError(t, err)
	require.NoError(t, r.pg.UpdateLinkFolder(wctx, behind, pgRevision))

	require.NoError(t, r.links.DeleteLinkFolder(ctx, "uid-1", "f-1", revision))

	_, _, pgErr := r.pg.GetLinkFolder(ctx, "uid-1", "f-1")
	assert.True(t, isNotFound(pgErr))
	assert.Empty(t, r.counts(t, failures))
	require.NoError(t, r.links.CreateLinkFolder(ctx, storetest.NewFolder("f-2", "uid-1", "Charters")), "the name is free again")
	assert.Empty(t, r.counts(t, failures), "and is free in PostgreSQL too")
}

// updatingLinkKV is a KV store that updates the link, or the folder, as the copy of its creation reads it back for the second
// time, which is where an update that finishes while a create is being copied lands.
type updatingLinkKV struct {
	LinkBackend
	calls atomic.Int32
}

func (u *updatingLinkKV) GetLink(ctx context.Context, committeeUID, uid string) (*model.CommitteeLink, uint64, error) {
	if u.calls.Add(1) == 2 {
		if link, revision, err := u.LinkBackend.GetLink(ctx, committeeUID, uid); err == nil {
			link.Name = "updated meanwhile"
			_ = u.UpdateLink(ctx, link, revision)
		}
	}
	return u.LinkBackend.GetLink(ctx, committeeUID, uid)
}

func (u *updatingLinkKV) GetLinkFolder(ctx context.Context, committeeUID, uid string) (*model.CommitteeLinkFolder, uint64, error) {
	if u.calls.Add(1) == 2 {
		if folder, revision, err := u.LinkBackend.GetLinkFolder(ctx, committeeUID, uid); err == nil {
			folder.Name = "Updated meanwhile"
			_ = u.UpdateLinkFolder(ctx, folder, revision)
		}
	}
	return u.LinkBackend.GetLinkFolder(ctx, committeeUID, uid)
}

// An update that finishes in KV while a create is being copied failed to copy, for there was nothing in PostgreSQL to
// update yet, and the insert would leave the older version. The copy catches up with what KV holds.
func TestLinks_DualACreateCopiedWhileAnUpdateFinishesLeavesTheNewVersion(t *testing.T) {
	for _, k := range linkKinds() {
		t.Run(k.name, func(t *testing.T) {
			r := newLinkRig(t, ModeDual, nil)
			r.links.kv = &updatingLinkKV{LinkBackend: r.kv} // what the copy reads from; the primary store is still the real one

			uid := k.create(t, r)

			sameLinkRecord(t, k, r, uid)
			pg, _, err := k.read(r.pg, uid)
			require.NoError(t, err)
			switch record := pg.(type) {
			case *model.CommitteeLink:
				assert.Equal(t, "updated meanwhile", record.Name, "PostgreSQL holds the newer version")
			case *model.CommitteeLinkFolder:
				assert.Equal(t, "Updated meanwhile", record.Name, "PostgreSQL holds the newer version")
			}
			assert.Empty(t, r.counts(t, failures))
		})
	}
}

func TestLinks_DualCompareCountsWhatDiffers(t *testing.T) {
	ctx := context.Background()

	t.Run("two stores that agree count nothing", func(t *testing.T) {
		r := newLinkRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.links.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
		require.NoError(t, r.links.CreateLinkFolder(ctx, storetest.NewFolder("f-1", "uid-1", "Charters")))

		_, _, _ = r.links.GetLink(ctx, "uid-1", "l-1")
		_, _ = r.links.ListLinks(ctx, "uid-1")
		_, _, _ = r.links.GetLinkFolder(ctx, "uid-1", "f-1")
		_, _ = r.links.ListLinkFolders(ctx, "uid-1")
		_, _, _ = r.links.GetLink(ctx, "uid-1", "l-missing")

		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("a record only KV has is a mismatch in every read of it", func(t *testing.T) {
		r := newLinkRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.kv.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
		require.NoError(t, r.kv.CreateLinkFolder(ctx, storetest.NewFolder("f-1", "uid-1", "Charters")))

		_, _, _ = r.links.GetLink(ctx, "uid-1", "l-1")
		_, _ = r.links.ListLinks(ctx, "uid-1")
		_, _, _ = r.links.GetLinkFolder(ctx, "uid-1", "f-1")
		_, _ = r.links.ListLinkFolders(ctx, "uid-1")

		assert.Equal(t, map[string]int64{"get_link": 1, "list_links": 1, "get_link_folder": 1, "list_link_folders": 1}, r.counts(t, mismatches))
	})

	t.Run("a read that failed is not compared", func(t *testing.T) {
		r := newLinkRig(t, ModeDual, nil, WithSampleRate(1))

		_, _, err := r.links.GetLink(ctx, "uid-1", "") // an empty link uid is a validation error

		require.Error(t, err)
		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})
}

func TestLinks_NewLinksNeedsTheStoresTheModeUses(t *testing.T) {
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)

	_, err := NewLinks(ModePostgres, kv, nil)
	assert.Error(t, err)
	_, err = NewLinks(ModeKV, nil, pg)
	assert.Error(t, err)
	_, err = NewLinks(ModeDual, kv, pg)
	assert.NoError(t, err)
}
