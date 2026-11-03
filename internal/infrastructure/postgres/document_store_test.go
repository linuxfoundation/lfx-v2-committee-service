// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// testDocument returns document metadata with every field set.
func testDocument(uid, committeeUID, name string) *model.CommitteeDocument {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	folder := "f-1"
	return &model.CommitteeDocument{
		UID: uid, CommitteeUID: committeeUID, FolderUID: &folder, Name: name, Description: "Meeting minutes",
		FileName: "minutes.pdf", FileSize: 2048, ContentType: "application/pdf",
		CreatedBy: testAuditUser(), UpdatedBy: testAuditUser(), CreatedAt: created, UpdatedAt: created.Add(time.Hour),
	}
}

func documentUIDs(docs []*model.CommitteeDocument) []string {
	uids := []string{}
	for _, d := range docs {
		uids = append(uids, d.UID)
	}
	return uids
}

func seedDocuments(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	for _, d := range []*model.CommitteeDocument{
		testDocument("d-1", "committee-1", "Minutes"), testDocument("d-2", "committee-1", "Agenda"), testDocument("d-3", "committee-2", "Minutes"),
	} {
		require.NoError(t, store.CreateDocumentMetadata(context.Background(), d))
	}
	return store
}

func TestStore_CreateDocumentMetadata(t *testing.T) {
	ctx := context.Background()
	store := seedDocuments(t)

	t.Run("round-trips every field", func(t *testing.T) {
		want := testDocument("d-new", "committee-1", "Budget")
		require.NoError(t, store.CreateDocumentMetadata(ctx, want))
		got, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-new")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, want, got)
	})

	t.Run("a document with no folder or audit users round-trips as empty", func(t *testing.T) {
		want := testDocument("d-bare", "committee-1", "Bare")
		want.FolderUID, want.CreatedBy, want.UpdatedBy, want.Description = nil, nil, nil, ""
		require.NoError(t, store.CreateDocumentMetadata(ctx, want))
		got, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-bare")
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("a name already in the committee is a conflict, but only on an exact match", func(t *testing.T) {
		requireConflict(t, store.CreateDocumentMetadata(ctx, testDocument("d-dup", "committee-1", "Minutes")))
		_, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateDocumentMetadata(ctx, testDocument("d-case", "committee-1", "minutes")))
		require.NoError(t, store.CreateDocumentMetadata(ctx, testDocument("d-other", "committee-3", "Minutes")))
	})

	t.Run("the same uid is a conflict; a nil document is invalid", func(t *testing.T) {
		requireConflict(t, store.CreateDocumentMetadata(ctx, testDocument("d-1", "committee-9", "Elsewhere")))
		var validation errs.Validation
		require.ErrorAs(t, store.CreateDocumentMetadata(ctx, nil), &validation)
		require.ErrorAs(t, store.CreateDocumentMetadata(ctx, testDocument("", "committee-1", "No uid")), &validation)
		require.ErrorAs(t, store.CreateDocumentMetadata(ctx, testDocument("d-x", "", "No committee")), &validation)
	})
}

func TestStore_GetDocumentMetadata(t *testing.T) {
	ctx := context.Background()
	store := seedDocuments(t)

	doc, _, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
	require.NoError(t, err)
	assert.Equal(t, "Minutes", doc.Name)

	_, _, err = store.GetDocumentMetadata(ctx, "committee-1", "missing")
	requireNotFound(t, err)
	_, _, err = store.GetDocumentMetadata(ctx, "committee-2", "d-1")
	requireNotFound(t, err)

	var validation errs.Validation
	_, _, err = store.GetDocumentMetadata(ctx, "committee-1", "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_DocumentNameExists(t *testing.T) {
	ctx := context.Background()
	store := seedDocuments(t)

	for name, want := range map[string]bool{"Minutes": true, "minutes": false, "Budget": false} {
		exists, err := store.DocumentNameExists(ctx, "committee-1", name)
		require.NoError(t, err)
		assert.Equal(t, want, exists, name)
	}
	exists, err := store.DocumentNameExists(ctx, "committee-9", "Minutes")
	require.NoError(t, err)
	assert.False(t, exists, "names are per committee")
}

func TestStore_ListAllDocuments(t *testing.T) {
	ctx := context.Background()
	store := seedDocuments(t)

	docs, err := store.ListAllDocuments(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"d-1", "d-2", "d-3"}, documentUIDs(docs))

	docs, err = store.ListAllDocuments(ctx, "committee-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"d-1", "d-2"}, documentUIDs(docs))

	docs, err = store.ListAllDocuments(ctx, "no-such-committee")
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestStore_UpdateDocumentMetadata(t *testing.T) {
	ctx := context.Background()
	store := seedDocuments(t)
	current := func(t *testing.T, uid string) (*model.CommitteeDocument, uint64) {
		t.Helper()
		doc, revision, err := store.GetDocumentMetadata(ctx, "committee-1", uid)
		require.NoError(t, err)
		return doc, revision
	}

	t.Run("updates the metadata and replaces the revision", func(t *testing.T) {
		doc, revision := current(t, "d-1")
		doc.Description, doc.FileSize = "Updated", 4096
		require.NoError(t, store.UpdateDocumentMetadata(ctx, doc, revision))
		got, newRevision := current(t, "d-1")
		assert.Equal(t, doc, got)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		doc, revision := current(t, "d-2")
		doc.Description = "first writer"
		require.NoError(t, store.UpdateDocumentMetadata(ctx, doc, revision))
		doc.Description = "second writer"
		requireConflict(t, store.UpdateDocumentMetadata(ctx, doc, revision))
		got, _ := current(t, "d-2")
		assert.Equal(t, "first writer", got.Description)
	})

	t.Run("taking another document's name in the committee is a conflict", func(t *testing.T) {
		doc, revision := current(t, "d-2")
		doc.Name = "Minutes"
		requireConflict(t, store.UpdateDocumentMetadata(ctx, doc, revision))
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, revision := current(t, "d-1")
		wins := raceWriters(t, func() error {
			doc := *original
			return store.UpdateDocumentMetadata(ctx, &doc, revision)
		})
		assert.Equal(t, 1, wins)
	})

	t.Run("a missing document is not found and a nil one is invalid", func(t *testing.T) {
		doc, revision := current(t, "d-2")
		doc.UID = "missing"
		requireNotFound(t, store.UpdateDocumentMetadata(ctx, doc, revision))
		var validation errs.Validation
		require.ErrorAs(t, store.UpdateDocumentMetadata(ctx, nil, 1), &validation)
	})
}

func TestStore_DeleteDocumentMetadata(t *testing.T) {
	ctx := context.Background()
	store := seedDocuments(t)
	_, revision, err := store.GetDocumentMetadata(ctx, "committee-1", "d-1")
	require.NoError(t, err)

	var validation errs.Validation
	require.ErrorAs(t, store.DeleteDocumentMetadata(ctx, "committee-1", "", revision), &validation)
	requireConflict(t, store.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision+1000))
	requireNotFound(t, store.DeleteDocumentMetadata(ctx, "committee-2", "d-1", revision))
	_, _, err = store.GetDocumentMetadata(ctx, "committee-1", "d-1")
	require.NoError(t, err, "a rejected delete changes nothing")

	require.NoError(t, store.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision))
	requireNotFound(t, store.DeleteDocumentMetadata(ctx, "committee-1", "d-1", revision))

	// The name is free again.
	require.NoError(t, store.CreateDocumentMetadata(ctx, testDocument("d-again", "committee-1", "Minutes")))
}
