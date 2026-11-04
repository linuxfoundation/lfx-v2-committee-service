// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// DocumentStore is what the document metadata suite needs of a backend. The file bytes are not part of
// it: they live in the NATS Object Store, and the PostgreSQL store holds metadata only.
type DocumentStore interface {
	CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error
	GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
	DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error)
	UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error
	DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error
	ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
}

// DocumentFactory builds a fresh, empty document metadata store.
type DocumentFactory func(t *testing.T) DocumentStore

func documentUIDs(docs []*model.CommitteeDocument) []string {
	uids := make([]string, 0, len(docs))
	for _, d := range docs {
		uids = append(uids, d.UID)
	}
	return uids
}

// RunDocumentContract runs the committee document metadata cases against the backend: create, get, the
// name check, list, update with the revision, delete with the revision within the committee, and the name
// unique per committee.
func RunDocumentContract(t *testing.T, backend Backend, newStore DocumentFactory) {
	t.Run(backend.Name, func(t *testing.T) { runDocumentContract(t, newStore) })
}

func runDocumentContract(t *testing.T, newStore DocumentFactory) {
	ctx := context.Background()
	seed := func(t *testing.T) DocumentStore {
		t.Helper()
		store := newStore(t)
		for _, d := range []*model.CommitteeDocument{
			NewDocument("d-1", "committee-1", "Minutes"), NewDocument("d-2", "committee-1", "Agenda"), NewDocument("d-3", "committee-2", "Minutes"),
		} {
			require.NoError(t, store.CreateDocumentMetadata(ctx, d))
		}
		return store
	}

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := NewDocument("d-1", "committee-1", "Minutes")
		require.NoError(t, store.CreateDocumentMetadata(ctx, want))
		got, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeDocument(want), normalizeDocument(got))
	})

	t.Run("a document with no folder, description or audit users reads back empty", func(t *testing.T) {
		store := newStore(t)
		want := NewDocument("d-bare", "committee-1", "Bare")
		want.FolderUID, want.CreatedBy, want.UpdatedBy, want.Description = nil, nil, nil, ""
		require.NoError(t, store.CreateDocumentMetadata(ctx, want))
		got, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-bare")
		require.NoError(t, err)
		assert.Equal(t, normalizeDocument(want), normalizeDocument(got))
	})

	t.Run("a document is found within its committee only, and an empty uid is invalid", func(t *testing.T) {
		store := seed(t)
		_, _, err := store.GetDocumentMetadata(ctx, "committee-1", "missing")
		requireNotFound(t, err)
		_, _, err = store.GetDocumentMetadata(ctx, "committee-2", "d-1")
		requireNotFound(t, err)
		_, _, err = store.GetDocumentMetadata(ctx, "committee-1", "")
		requireValidation(t, err)
	})

	t.Run("create rejects a nil document and missing uids", func(t *testing.T) {
		store := newStore(t)
		requireValidation(t, store.CreateDocumentMetadata(ctx, nil))
		requireValidation(t, store.CreateDocumentMetadata(ctx, NewDocument("", "committee-1", "Minutes")))
		requireValidation(t, store.CreateDocumentMetadata(ctx, NewDocument("d-x", "", "Minutes")))
	})

	t.Run("a document name is unique per committee, and case-sensitive", func(t *testing.T) {
		store := seed(t)
		requireConflict(t, store.CreateDocumentMetadata(ctx, NewDocument("d-dup", "committee-1", "Minutes")))
		_, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateDocumentMetadata(ctx, NewDocument("d-case", "committee-1", "minutes")))
		require.NoError(t, store.CreateDocumentMetadata(ctx, NewDocument("d-other", "committee-3", "Minutes")))
	})

	t.Run("the same uid is a conflict, whatever else the second document holds", func(t *testing.T) {
		store := seed(t)
		requireConflict(t, store.CreateDocumentMetadata(ctx, NewDocument("d-1", "committee-9", "Elsewhere")))
		// A refused create reserves nothing: that name is still free in that committee.
		require.NoError(t, store.CreateDocumentMetadata(ctx, NewDocument("d-new", "committee-9", "Elsewhere")))

		// The same committee and name as the document that holds the uid, but other content: not a create
		// that already landed.
		impostor := NewDocument("d-1", "committee-1", "Minutes")
		impostor.Description = "Impostor"
		requireConflict(t, store.CreateDocumentMetadata(ctx, impostor))
		got, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)
		assert.Equal(t, "Meeting minutes", got.Description)
	})

	t.Run("DocumentNameExists compares the exact name within the committee", func(t *testing.T) {
		store := seed(t)
		for _, c := range []struct {
			committee, name string
			want            bool
		}{
			{"committee-1", "Minutes", true},
			{"committee-1", "minutes", false},
			{"committee-1", "Budget", false},
			{"committee-9", "Minutes", false},
		} {
			exists, err := store.DocumentNameExists(ctx, c.committee, c.name)
			require.NoError(t, err)
			assert.Equal(t, c.want, exists, "%s in %s", c.name, c.committee)
		}
	})

	t.Run("ListAllDocuments lists every document, or the committee's", func(t *testing.T) {
		store := newStore(t)
		docs, err := store.ListAllDocuments(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, docs)

		store = seed(t)
		docs, err = store.ListAllDocuments(ctx, "")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"d-1", "d-2", "d-3"}, documentUIDs(docs))
		docs, err = store.ListAllDocuments(ctx, "committee-1")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"d-1", "d-2"}, documentUIDs(docs))
		docs, err = store.ListAllDocuments(ctx, "no-such-committee")
		require.NoError(t, err)
		assert.Empty(t, docs)
	})

	t.Run("UpdateDocumentMetadata replaces the metadata and the revision", func(t *testing.T) {
		store := seed(t)
		doc, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)
		doc.Description = "Updated"
		doc.FileName = "minutes-v2.pdf"
		doc.FileSize = 4096
		doc.ContentType = "text/plain"
		doc.FolderUID = nil
		doc.UpdatedBy = &model.CommitteeUser{Name: "First Last", Username: "first-last-2", Email: "first.last2@example.com"}
		require.NoError(t, store.UpdateDocumentMetadata(ctx, doc, revision))

		got, newRevision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)
		assert.Equal(t, normalizeDocument(doc), normalizeDocument(got), "every edited field is stored")
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("UpdateDocumentMetadata with a stale revision fails and changes nothing", func(t *testing.T) {
		store := seed(t)
		doc, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-2")
		require.NoError(t, err)
		doc.Description = "first writer"
		require.NoError(t, store.UpdateDocumentMetadata(ctx, doc, revision))

		doc.Description = "second writer"
		requireConflict(t, store.UpdateDocumentMetadata(ctx, doc, revision))
		got, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-2")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.Description)
	})

	t.Run("UpdateDocumentMetadata of a missing document is not found, and a nil one is invalid", func(t *testing.T) {
		store := newStore(t)
		requireNotFound(t, store.UpdateDocumentMetadata(ctx, NewDocument("ghost", "committee-1", "Ghost"), 1))
		requireValidation(t, store.UpdateDocumentMetadata(ctx, nil, 1))
	})

	t.Run("DeleteDocumentMetadata removes the document within its committee, with the revision, and frees its name", func(t *testing.T) {
		store := seed(t)
		_, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)

		requireValidation(t, store.DeleteDocumentMetadata(ctx, "committee-1", "", revision))
		requireConflict(t, store.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision+1000))
		requireNotFound(t, store.DeleteDocumentMetadata(ctx, "committee-2", "d-1", revision))
		_, _, err = store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err, "a rejected delete changes nothing")
		// A rejected delete leaves the name taken, in this committee and in the other.
		requireConflict(t, store.CreateDocumentMetadata(ctx, NewDocument("d-dup", "committee-1", "Minutes")))
		requireConflict(t, store.CreateDocumentMetadata(ctx, NewDocument("d-dup", "committee-2", "Minutes")))

		require.NoError(t, store.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision))
		_, _, err = store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		requireNotFound(t, err)
		requireNotFound(t, store.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision))
		exists, err := store.DocumentNameExists(ctx, "committee-1", "Minutes")
		require.NoError(t, err)
		assert.False(t, exists)
		require.NoError(t, store.CreateDocumentMetadata(ctx, NewDocument("d-again", "committee-1", "Minutes")), "the name is free again")
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := seed(t)
		doc, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
		require.NoError(t, err)
		wins := race(t, func(writer int) error {
			update := *doc
			update.Description = fmt.Sprintf("writer %d", writer)
			return store.UpdateDocumentMetadata(ctx, &update, revision)
		}, requireConflict)
		assert.Equal(t, 1, wins)
	})
}
