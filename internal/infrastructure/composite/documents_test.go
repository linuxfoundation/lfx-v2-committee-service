// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// documentRig is a composite of the documents over a real KV and Object Store and a real PostgreSQL store.
type documentRig struct {
	docs   *Documents
	kv     natstest.DocumentBackend
	pg     *postgres.Store
	reader *sdkmetric.ManualReader
}

func newDocumentRig(t *testing.T, mode Mode, wrapPG func(DocumentPGBackend) DocumentPGBackend, opts ...Option) *documentRig {
	t.Helper()
	kv, pg := natstest.NewDocumentBackend(t), postgrestest.NewStore(t)
	return newDocumentRigOver(t, mode, kv, pg, wrapPG, opts...)
}

func newDocumentRigOver(t *testing.T, mode Mode, kv natstest.DocumentBackend, pg *postgres.Store,
	wrapPG func(DocumentPGBackend) DocumentPGBackend, opts ...Option) *documentRig {
	t.Helper()
	metrics, reader := newMetricsReader()
	var used DocumentPGBackend = pg
	if wrapPG != nil {
		used = wrapPG(pg)
	}
	docs, err := NewDocuments(mode, kv, used, append([]Option{WithMetrics(metrics)}, opts...)...)
	require.NoError(t, err)
	return &documentRig{docs: docs, kv: kv, pg: pg, reader: reader}
}

func (r *documentRig) counts(t *testing.T, name string) map[string]int64 {
	t.Helper()
	return countsOf(t, r.reader, name)
}

// Whatever the mode, the composite behaves as one store does for the document metadata: the same suite the KV store and
// the PostgreSQL store pass. In dual mode every read of the suite was compared and every write copied, and the stores
// never disagreed and nothing failed.
func TestContract_Documents(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModeDual, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			storetest.RunDocumentContract(t, storetest.Backend{Name: "composite-" + string(mode)}, func(t *testing.T) storetest.DocumentStore {
				r := newDocumentRig(t, mode, nil, WithSampleRate(1))
				t.Cleanup(func() {
					assert.Empty(t, r.counts(t, mismatches), "no false mismatch")
					assert.Empty(t, r.counts(t, compareErr))
					assert.Empty(t, r.counts(t, failures))
				})
				return r.docs
			})
		})
	}
}

func fileOf(t *testing.T, r *documentRig, uid string) ([]byte, error) {
	t.Helper()
	return r.kv.GetDocumentFile(context.Background(), uid)
}

func TestDocuments_EachModeWritesTheMetadataToItsStoresAndTheFileToKV(t *testing.T) {
	ctx := context.Background()
	for mode, want := range map[Mode]struct{ kv, pg bool }{ModeKV: {true, false}, ModeDual: {true, true}, ModePostgres: {false, true}} {
		t.Run(string(mode), func(t *testing.T) {
			r := newDocumentRig(t, mode, nil)

			require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
			require.NoError(t, r.docs.PutDocumentFile(ctx, "d-1", []byte("file")))

			_, _, kvErr := r.kv.GetDocumentMetadata(ctx, "uid-1", "d-1")
			_, _, pgErr := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
			assert.Equal(t, want.kv, kvErr == nil, "KV holds the metadata")
			assert.Equal(t, want.pg, pgErr == nil, "PostgreSQL holds the metadata")
			got, err := r.docs.GetDocumentFile(ctx, "d-1")
			require.NoError(t, err)
			assert.Equal(t, []byte("file"), got, "the file is read through the composite")
			stored, err := fileOf(t, r, "d-1")
			require.NoError(t, err, "the file is in the Object Store in every mode")
			assert.Equal(t, []byte("file"), stored)
		})
	}
}

func sameDocumentInBoth(t *testing.T, r *documentRig, uid string) {
	t.Helper()
	ctx := context.Background()
	kv, kvRevision, err := r.kv.GetDocumentMetadata(ctx, "uid-1", uid)
	require.NoError(t, err)
	pg, pgRevision, err := r.pg.GetDocumentMetadata(ctx, "uid-1", uid)
	require.NoError(t, err, "PostgreSQL holds the document")
	assert.Equal(t, kvRevision, pgRevision, "the revision is the one KV gave it")
	a, err := Digest(kv)
	require.NoError(t, err)
	b, err := Digest(pg)
	require.NoError(t, err)
	assert.Equal(t, a, b, "the same content")
}

func TestDocuments_DualWritesCarryTheKVRevisionToPostgreSQL(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDual, nil)

	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	sameDocumentInBoth(t, r, "d-1")
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)

	doc.Description = "changed"
	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))
	sameDocumentInBoth(t, r, "d-1")
	_, updated, err := r.kv.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)
	assert.Greater(t, updated, revision, "a token from before the update is stale in both stores")

	require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", updated))
	_, _, kvErr := r.kv.GetDocumentMetadata(ctx, "uid-1", "d-1")
	_, _, pgErr := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
	assert.Error(t, kvErr)
	assert.Error(t, pgErr, "the delete reached PostgreSQL")
	assert.Empty(t, r.counts(t, failures))
}

// failingDocumentPG is a PostgreSQL store whose writes fail.
type failingDocumentPG struct{ DocumentPGBackend }

func (failingDocumentPG) CreateDocumentMetadata(context.Context, *model.CommitteeDocument) error {
	return errPG
}

func (failingDocumentPG) UpdateDocumentMetadata(context.Context, *model.CommitteeDocument, uint64) error {
	return errPG
}

func (failingDocumentPG) DeleteDocumentMetadata(context.Context, string, string, uint64) error {
	return errPG
}

func TestDocuments_DualASecondaryFailureIsCountedAndNeverFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDual, func(pg DocumentPGBackend) DocumentPGBackend { return failingDocumentPG{pg} })

	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err, "KV decided")
	doc.Description = "changed"
	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))
	_, revision, err = r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)
	require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

	assert.Equal(t, map[string]int64{"create_document": 1, "update_document": 1, "delete_document": 1}, r.counts(t, failures))
}

// An update replaces the metadata by uid and may move the document to another committee: the copy still reaches the row,
// which PostgreSQL holds under the committee it had.
func TestDocuments_DualAnUpdateThatChangesTheCommitteeIsCopied(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDual, nil, WithSampleRate(1))
	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)

	doc.CommitteeUID = "uid-2"
	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))

	assert.Empty(t, r.counts(t, failures), "the copy found the row under its old committee")
	kv, kvRevision, err := r.kv.GetDocumentMetadata(ctx, "uid-2", "d-1")
	require.NoError(t, err)
	pg, pgRevision, err := r.pg.GetDocumentMetadata(ctx, "uid-2", "d-1")
	require.NoError(t, err, "PostgreSQL holds the document under the new committee")
	assert.Equal(t, kvRevision, pgRevision)
	assert.Equal(t, kv.CommitteeUID, pg.CommitteeUID)
}

// A copy that was missed is made up for by the next update of the same document, which copies what KV holds then.
func TestDocuments_DualTheNextUpdateBringsPostgreSQLUpToWhatKVHolds(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDual, nil)
	require.NoError(t, r.kv.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes"))) // never copied
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)
	require.NoError(t, r.pg.CreateDocumentMetadata(mustRevision(t, ctx, revision), doc)) // an older copy
	doc.Description = "newer"
	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))

	sameDocumentInBoth(t, r, "d-1")
	assert.Empty(t, r.counts(t, failures))
}

func mustRevision(t *testing.T, ctx context.Context, revision uint64) context.Context {
	t.Helper()
	wctx, err := postgres.WithRevision(ctx, revision)
	require.NoError(t, err)
	return wctx
}

// The file goes with the metadata, whichever store decided: the NATS adapter removes it itself, and in postgres mode the
// composite does, since the PostgreSQL store holds metadata only.
func TestDocuments_DeletingTheMetadataRemovesTheFileInEveryMode(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []Mode{ModeKV, ModeDual, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			r := newDocumentRig(t, mode, nil)
			require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
			require.NoError(t, r.docs.PutDocumentFile(ctx, "d-1", []byte("file")))
			_, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
			require.NoError(t, err)

			require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

			_, err = fileOf(t, r, "d-1")
			var notFound errs.NotFound
			assert.ErrorAs(t, err, &notFound, "the file is gone")
		})
	}
}

// failingFileKV is a KV store that cannot delete a file.
type failingFileKV struct{ natstest.DocumentBackend }

func (failingFileKV) DeleteDocumentFile(context.Context, string) error { return errPG }

// A file that cannot be removed does not fail the delete of the metadata, which is already gone.
func TestDocuments_APostgresDeleteDoesNotFailWhenTheFileCannotBeRemoved(t *testing.T) {
	ctx := context.Background()
	kv, pg := natstest.NewDocumentBackend(t), postgrestest.NewStore(t)
	metrics, reader := newMetricsReader()
	docs, err := NewDocuments(ModePostgres, failingFileKV{kv}, pg, WithMetrics(metrics))
	require.NoError(t, err)

	require.NoError(t, docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	_, revision, err := docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)

	require.NoError(t, docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

	_, _, err = pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
	assert.Error(t, err, "the metadata is gone")
	assert.Equal(t, map[string]int64{"delete_document_file": 1}, countsOf(t, reader, failures), "the file left behind is counted")
}

// cancellingDocumentPG cancels the request once the metadata is deleted, as a client that goes away would.
type cancellingDocumentPG struct {
	DocumentPGBackend
	cancel context.CancelFunc
}

func (p cancellingDocumentPG) DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error {
	err := p.DocumentPGBackend.DeleteDocumentMetadata(ctx, committeeUID, documentUID, revision)
	p.cancel()
	return err
}

// A client that goes away right after the metadata was deleted does not leave the file behind.
func TestDocuments_APostgresDeleteRemovesTheFileWhenTheRequestIsCancelledMeanwhile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newDocumentRig(t, ModePostgres, func(pg DocumentPGBackend) DocumentPGBackend {
		return cancellingDocumentPG{DocumentPGBackend: pg, cancel: cancel}
	})
	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	require.NoError(t, r.docs.PutDocumentFile(ctx, "d-1", []byte("file")))
	_, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)

	require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

	_, err = fileOf(t, r, "d-1")
	var notFound errs.NotFound
	assert.ErrorAs(t, err, &notFound, "the file is gone")
	assert.Empty(t, r.counts(t, failures))
}

// A document that PostgreSQL holds at another revision, because an earlier copy failed, is still removed by a delete, and a
// document it does not have is where the delete leaves it.
func TestDocuments_DualADeleteConverges(t *testing.T) {
	ctx := context.Background()

	t.Run("a row at another revision", func(t *testing.T) {
		r := newDocumentRig(t, ModeDual, nil)
		require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
		_, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
		require.NoError(t, err)
		behind, pgRevision, err := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
		require.NoError(t, err)
		behind.Description = "an update PostgreSQL has and KV has not"
		require.NoError(t, r.pg.UpdateDocumentMetadata(mustRevision(t, ctx, pgRevision+1000), behind, pgRevision))

		require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

		_, _, err = r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
		assert.Error(t, err, "removed whatever its revision")
		assert.Empty(t, r.counts(t, failures))
	})

	t.Run("an absent row", func(t *testing.T) {
		r := newDocumentRig(t, ModeDual, nil)
		require.NoError(t, r.kv.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes"))) // never copied
		_, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
		require.NoError(t, err)

		require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

		assert.Empty(t, r.counts(t, failures), "an absent row is where a delete leaves it")
	})
}

func TestDocuments_DualCompareCountsWhatDiffers(t *testing.T) {
	ctx := context.Background()

	t.Run("two stores that agree count nothing", func(t *testing.T) {
		r := newDocumentRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))

		_, _, _ = r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
		_, _ = r.docs.ListAllDocuments(ctx, "uid-1")
		_, _ = r.docs.DocumentNameExists(ctx, "uid-1", "Minutes")
		_, _ = r.docs.DocumentNameExists(ctx, "uid-1", "Other")
		_, _, _ = r.docs.GetDocumentMetadata(ctx, "uid-1", "missing") // a miss in both

		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("a document only KV has is a mismatch in every read", func(t *testing.T) {
		r := newDocumentRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.kv.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))

		_, _, _ = r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
		_, _ = r.docs.ListAllDocuments(ctx, "uid-1")
		_, _ = r.docs.DocumentNameExists(ctx, "uid-1", "Minutes")

		assert.Equal(t, map[string]int64{"get_document": 1, "list_documents": 1, "document_name_exists": 1}, r.counts(t, mismatches))
	})

	t.Run("a revision that differs alone is a mismatch", func(t *testing.T) {
		r := newDocumentRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
		doc, revision, err := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
		require.NoError(t, err)
		require.NoError(t, r.pg.UpdateDocumentMetadata(mustRevision(t, ctx, revision+1000), doc, revision))

		_, _, _ = r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")

		assert.Equal(t, map[string]int64{"get_document": 1}, r.counts(t, mismatches))
	})

	t.Run("the list of every committee is not compared", func(t *testing.T) {
		r := newDocumentRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.kv.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))

		_, _ = r.docs.ListAllDocuments(ctx, "")

		assert.Empty(t, r.counts(t, mismatches))
	})
}

func TestDocuments_Defaults(t *testing.T) {
	kv, pg := natstest.NewDocumentBackend(t), postgrestest.NewStore(t)

	d, err := NewDocuments(ModeDual, kv, pg)

	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, d.writeTimeout)
	_, err = NewDocuments(ModePostgres, nil, pg)
	assert.Error(t, err, "the files are in the Object Store in every mode")
	_, err = NewDocuments(ModeDual, kv, nil)
	assert.Error(t, err)
	_, err = NewDocuments(ModePostgres, kv, nil)
	assert.Error(t, err)
	_, err = NewDocuments(ModeDualPG, kv, pg)
	assert.Error(t, err)
}
