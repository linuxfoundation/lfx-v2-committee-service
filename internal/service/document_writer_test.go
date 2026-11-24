// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDocStorage is an in-memory implementation of CommitteeDocumentReaderWriter for tests.
type mockDocStorage struct {
	documents map[string]*model.CommitteeDocument
	revisions map[string]uint64
	files     map[string][]byte
	nameKeys  map[string]string // uniqueKey -> documentUID

	// createErr, when set, is returned by CreateDocumentMetadata before any state changes.
	createErr error
	// cancelOnCreate, when set, is called by CreateDocumentMetadata, which then fails with
	// context.Canceled without storing, simulating a client disconnect after the file was uploaded.
	cancelOnCreate context.CancelFunc
	// createErrAfterStore stores the metadata and then returns createErr, simulating a write that
	// was committed but reported as failed.
	createErrAfterStore bool
	// getErr, when set, is returned by GetDocumentMetadata, simulating a failed read.
	getErr error
	// skipNameCheck makes DocumentNameExists report false so a test can reach the authoritative
	// uniqueness check in CreateDocumentMetadata (simulating a concurrent upload winning the name).
	skipNameCheck bool
}

func newMockDocStorage() *mockDocStorage {
	return &mockDocStorage{
		documents: make(map[string]*model.CommitteeDocument),
		revisions: make(map[string]uint64),
		files:     make(map[string][]byte),
		nameKeys:  make(map[string]string),
	}
}

func (m *mockDocStorage) CreateDocumentMetadata(_ context.Context, doc *model.CommitteeDocument) error {
	if m.cancelOnCreate != nil {
		m.cancelOnCreate()
		return context.Canceled
	}
	if m.createErr != nil && !m.createErrAfterStore {
		return m.createErr
	}
	nameKey := doc.CommitteeUID + "|" + doc.Name
	if _, exists := m.nameKeys[nameKey]; exists {
		return errs.NewConflict("document with the same name already exists for this committee")
	}
	m.documents[doc.UID] = doc
	m.revisions[doc.UID] = 1
	m.nameKeys[nameKey] = doc.UID
	return m.createErr
}

func (m *mockDocStorage) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if m.getErr != nil {
		return nil, 0, m.getErr
	}
	doc, ok := m.documents[documentUID]
	if !ok || doc.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("document not found")
	}
	return doc, m.revisions[documentUID], nil
}

func (m *mockDocStorage) UpdateDocumentMetadata(_ context.Context, doc *model.CommitteeDocument, _ uint64) error {
	m.documents[doc.UID] = doc
	return nil
}

func (m *mockDocStorage) PutDocumentFile(_ context.Context, documentUID string, fileData []byte) error {
	m.files[documentUID] = fileData
	return nil
}

func (m *mockDocStorage) GetDocumentFile(_ context.Context, documentUID string) ([]byte, error) {
	data, ok := m.files[documentUID]
	if !ok {
		return nil, errors.New("file not found")
	}
	return data, nil
}

func (m *mockDocStorage) DeleteDocumentMetadata(_ context.Context, committeeUID, documentUID string, revision uint64) error {
	doc, ok := m.documents[documentUID]
	if !ok || doc.CommitteeUID != committeeUID {
		return errors.New("document not found")
	}
	if m.revisions[documentUID] != revision {
		return errs.NewConflict("document has been modified since it was last read")
	}
	// Clean up name uniqueness key (mirrors NATS implementation)
	nameKey := doc.CommitteeUID + "|" + doc.Name
	delete(m.nameKeys, nameKey)
	delete(m.documents, documentUID)
	delete(m.revisions, documentUID)
	return nil
}

func (m *mockDocStorage) DocumentNameExists(_ context.Context, committeeUID, name string) (bool, error) {
	if m.skipNameCheck {
		return false, nil
	}
	_, exists := m.nameKeys[committeeUID+"|"+name]
	return exists, nil
}

func (m *mockDocStorage) DeleteDocumentFile(ctx context.Context, documentUID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(m.files, documentUID)
	return nil
}

// errorPublisher always returns an error from all publish methods.
type errorPublisher struct{}

func (e *errorPublisher) Indexer(_ context.Context, _ string, _ any, _ bool) error {
	return errors.New("indexer unavailable")
}
func (e *errorPublisher) UpdateAccess(_ context.Context, _ any) error {
	return errors.New("access service unavailable")
}
func (e *errorPublisher) DeleteAccess(_ context.Context, _ any) error {
	return errors.New("access service unavailable")
}
func (e *errorPublisher) MemberPut(_ context.Context, _ any) error {
	return errors.New("access service unavailable")
}
func (e *errorPublisher) MemberRemove(_ context.Context, _ any) error {
	return errors.New("access service unavailable")
}
func (e *errorPublisher) Event(_ context.Context, _ string, _ any, _ bool) error {
	return errors.New("event service unavailable")
}

func validPDFFileData() []byte {
	return bytes.Repeat([]byte("x"), 1024) // 1KB
}

func newDocWriterOrch(storage *mockDocStorage) service.CommitteeDocumentDataWriter {
	return service.NewDocumentWriterOrchestrator(
		service.WithDocumentWriter(storage),
		service.WithDocumentReaderForWriter(storage),
	)
}

// --- UploadDocument validation tests ---

func TestUploadDocument_NilDocument_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), nil, validPDFFileData(), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "document is required")
}

func TestUploadDocument_MissingName_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		CommitteeUID: "committee-1",
		FileName:     "report.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestUploadDocument_MissingCommitteeUID_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:        "Annual Report",
		FileName:    "report.pdf",
		ContentType: "application/pdf",
	}, validPDFFileData(), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "committee UID")
}

func TestUploadDocument_MissingFileName_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Annual Report",
		CommitteeUID: "committee-1",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file name")
}

func TestUploadDocument_EmptyFileData_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Annual Report",
		CommitteeUID: "committee-1",
		FileName:     "report.pdf",
		ContentType:  "application/pdf",
	}, []byte{}, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file data")
}

func TestUploadDocument_MissingContentType_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Annual Report",
		CommitteeUID: "committee-1",
		FileName:     "report.pdf",
	}, validPDFFileData(), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "content type")
}

func TestUploadDocument_InvalidContentType_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Script",
		CommitteeUID: "committee-1",
		FileName:     "script.sh",
		ContentType:  "application/x-sh",
	}, validPDFFileData(), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestUploadDocument_OversizedFile_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	oversized := bytes.Repeat([]byte("x"), model.MaxDocumentFileSize+1)

	_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Huge File",
		CommitteeUID: "committee-1",
		FileName:     "big.pdf",
		ContentType:  "application/pdf",
	}, oversized, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

// --- UploadDocument success tests ---

func TestUploadDocument_Success(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)

	fileData := validPDFFileData()
	doc, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Architecture Overview",
		CommitteeUID: "committee-1",
		FileName:     "arch.pdf",
		ContentType:  "application/pdf",
	}, fileData, false)

	require.NoError(t, err)
	assert.NotEmpty(t, doc.UID)
	assert.Equal(t, "Architecture Overview", doc.Name)
	assert.Equal(t, "committee-1", doc.CommitteeUID)
	assert.Equal(t, int64(len(fileData)), doc.FileSize)
	assert.False(t, doc.CreatedAt.IsZero())
	assert.False(t, doc.UpdatedAt.IsZero())

	// Verify file stored in storage
	storedFile, ok := storage.files[doc.UID]
	require.True(t, ok, "expected file to be stored")
	assert.Equal(t, fileData, storedFile)

	// Verify metadata stored
	stored, ok := storage.documents[doc.UID]
	require.True(t, ok, "expected metadata to be stored")
	assert.Equal(t, doc.UID, stored.UID)
}

func TestUploadDocument_DuplicateName_ReturnsConflict(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)

	doc := &model.CommitteeDocument{
		Name:         "Architecture Overview",
		CommitteeUID: "committee-1",
		FileName:     "arch.pdf",
		ContentType:  "application/pdf",
	}

	_, err := orch.UploadDocument(context.Background(), doc, validPDFFileData(), false)
	require.NoError(t, err)

	// Second upload with same name (different file) must be rejected
	doc2 := &model.CommitteeDocument{
		Name:         "Architecture Overview",
		CommitteeUID: "committee-1",
		FileName:     "arch-v2.pdf",
		ContentType:  "application/pdf",
	}
	_, err = orch.UploadDocument(context.Background(), doc2, validPDFFileData(), false)

	require.Error(t, err)
	var conflictErr errs.Conflict
	assert.True(t, errors.As(err, &conflictErr), "expected a Conflict error, got: %T", err)
}

func TestUploadDocument_DuplicateName_DifferentCommittee_Succeeds(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)

	for _, committeeUID := range []string{"committee-1", "committee-2"} {
		_, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
			Name:         "Architecture Overview",
			CommitteeUID: committeeUID,
			FileName:     "arch.pdf",
			ContentType:  "application/pdf",
		}, validPDFFileData(), false)
		require.NoError(t, err, "upload for committee %s should succeed", committeeUID)
	}
}

func TestUploadDocument_PublisherErrors_DoNotFail(t *testing.T) {
	storage := newMockDocStorage()
	orch := service.NewDocumentWriterOrchestrator(
		service.WithDocumentWriter(storage),
		service.WithDocumentReaderForWriter(storage),
		service.WithDocumentPublisher(&errorPublisher{}),
	)

	doc, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Resilience Test",
		CommitteeUID: "committee-1",
		FileName:     "test.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)

	// Publisher errors are fire-and-forget; the upload must succeed.
	require.NoError(t, err)
	assert.NotEmpty(t, doc.UID)
}

// --- DeleteDocument tests ---

func TestDeleteDocument_Success(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)

	// Upload a document first so there is something to delete.
	uploaded, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "To Be Deleted",
		CommitteeUID: "committee-1",
		FileName:     "delete-me.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)
	require.NoError(t, err)

	// Retrieve the current revision from storage.
	revision := storage.revisions[uploaded.UID]

	err = orch.DeleteDocument(context.Background(), "committee-1", uploaded.UID, revision, false)

	require.NoError(t, err)
	_, exists := storage.documents[uploaded.UID]
	assert.False(t, exists, "expected document metadata to be removed after delete")
}

func TestDeleteDocument_NotFound_ReturnsError(t *testing.T) {
	orch := newDocWriterOrch(newMockDocStorage())

	err := orch.DeleteDocument(context.Background(), "committee-1", "nonexistent-uid", 1, false)

	assert.Error(t, err)
}

func TestDeleteDocument_WrongRevision_ReturnsConflict(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)

	uploaded, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Conflict Test",
		CommitteeUID: "committee-1",
		FileName:     "conflict.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)
	require.NoError(t, err)

	wrongRevision := storage.revisions[uploaded.UID] + 99
	err = orch.DeleteDocument(context.Background(), "committee-1", uploaded.UID, wrongRevision, false)

	require.Error(t, err)
	var conflictErr errs.Conflict
	assert.True(t, errors.As(err, &conflictErr), "expected a Conflict error, got: %T", err)
}

func TestDeleteDocument_NameReusableAfterDelete(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)

	uploaded, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Reusable Name",
		CommitteeUID: "committee-1",
		FileName:     "doc.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)
	require.NoError(t, err)

	revision := storage.revisions[uploaded.UID]
	err = orch.DeleteDocument(context.Background(), "committee-1", uploaded.UID, revision, false)
	require.NoError(t, err)

	// Re-upload with the same name must succeed after delete
	_, err = orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Reusable Name",
		CommitteeUID: "committee-1",
		FileName:     "doc-v2.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)
	require.NoError(t, err)
}

func TestDeleteDocument_PublisherErrors_DoNotFail(t *testing.T) {
	storage := newMockDocStorage()
	orch := service.NewDocumentWriterOrchestrator(
		service.WithDocumentWriter(storage),
		service.WithDocumentReaderForWriter(storage),
		service.WithDocumentPublisher(&errorPublisher{}),
	)

	uploaded, err := orch.UploadDocument(context.Background(), &model.CommitteeDocument{
		Name:         "Delete Publisher Test",
		CommitteeUID: "committee-1",
		FileName:     "pub-test.pdf",
		ContentType:  "application/pdf",
	}, validPDFFileData(), false)
	require.NoError(t, err)

	revision := storage.revisions[uploaded.UID]

	err = orch.DeleteDocument(context.Background(), "committee-1", uploaded.UID, revision, false)

	// Publisher errors are fire-and-forget; the delete must succeed.
	require.NoError(t, err)
}

func newDuplicateTestDocs() (first, second *model.CommitteeDocument) {
	first = &model.CommitteeDocument{
		Name:         "Architecture Overview",
		CommitteeUID: "committee-1",
		FileName:     "arch.pdf",
		ContentType:  "application/pdf",
	}
	second = &model.CommitteeDocument{
		Name:         "Architecture Overview",
		CommitteeUID: "committee-1",
		FileName:     "arch-v2.pdf",
		ContentType:  "application/pdf",
	}
	return first, second
}

func TestUploadDocument_DuplicateName_PreCheckDoesNotUploadFile(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)
	first, second := newDuplicateTestDocs()

	_, err := orch.UploadDocument(context.Background(), first, validPDFFileData(), false)
	require.NoError(t, err)

	_, err = orch.UploadDocument(context.Background(), second, validPDFFileData(), false)

	var conflictErr errs.Conflict
	require.ErrorAs(t, err, &conflictErr)
	assert.Len(t, storage.files, 1, "a rejected duplicate must not leave a second file behind")
	assert.Len(t, storage.documents, 1)
}

func TestUploadDocument_DuplicateName_LostRaceRemovesUploadedFile(t *testing.T) {
	storage := newMockDocStorage()
	orch := newDocWriterOrch(storage)
	first, second := newDuplicateTestDocs()

	_, err := orch.UploadDocument(context.Background(), first, validPDFFileData(), false)
	require.NoError(t, err)

	// Simulate a concurrent upload that won the name after the pre-check passed: the
	// authoritative check in CreateDocumentMetadata rejects this one.
	storage.skipNameCheck = true
	_, err = orch.UploadDocument(context.Background(), second, validPDFFileData(), false)

	var conflictErr errs.Conflict
	require.ErrorAs(t, err, &conflictErr)
	assert.Len(t, storage.files, 1, "the file uploaded for the losing request must be removed")
	assert.Len(t, storage.documents, 1)
}

func TestUploadDocument_MetadataCreateFails_RemovesUploadedFile(t *testing.T) {
	storage := newMockDocStorage()
	storage.createErr = errors.New("metadata store unavailable")
	orch := newDocWriterOrch(storage)
	first, _ := newDuplicateTestDocs()

	_, err := orch.UploadDocument(context.Background(), first, validPDFFileData(), false)

	require.Error(t, err)
	assert.Empty(t, storage.files, "the uploaded file must not be orphaned when metadata creation fails")
	assert.Empty(t, storage.documents)
}

func TestUploadDocument_AmbiguousMetadataWrite_KeepsUploadedFile(t *testing.T) {
	storage := newMockDocStorage()
	storage.createErr = errors.New("timeout after the write was committed")
	storage.createErrAfterStore = true
	orch := newDocWriterOrch(storage)
	first, _ := newDuplicateTestDocs()

	_, err := orch.UploadDocument(context.Background(), first, validPDFFileData(), false)

	require.Error(t, err)
	assert.Len(t, storage.documents, 1, "the metadata was committed")
	assert.Len(t, storage.files, 1, "the file must not be deleted while its metadata exists")
}

func TestUploadDocument_AmbiguousMetadataWrite_FailedReadKeepsUploadedFile(t *testing.T) {
	storage := newMockDocStorage()
	storage.createErr = errors.New("timeout after the write was committed")
	storage.createErrAfterStore = true
	storage.getErr = errors.New("metadata store unavailable")
	orch := newDocWriterOrch(storage)
	first, _ := newDuplicateTestDocs()

	_, err := orch.UploadDocument(context.Background(), first, validPDFFileData(), false)

	require.Error(t, err)
	assert.Len(t, storage.files, 1, "the file must be kept when the metadata cannot be confirmed absent")
}

func TestUploadDocument_CancelledRequest_RemovesUploadedFile(t *testing.T) {
	storage := newMockDocStorage()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storage.cancelOnCreate = cancel
	orch := newDocWriterOrch(storage)
	first, _ := newDuplicateTestDocs()

	_, err := orch.UploadDocument(ctx, first, validPDFFileData(), false)

	require.Error(t, err)
	assert.Empty(t, storage.documents)
	assert.Empty(t, storage.files, "a cancelled upload must not leave its file behind")
}

func TestUpdateDocument(t *testing.T) {
	const committeeUID = "committee-1"
	const documentUID = "doc-1"

	ctx := context.Background()
	creator := &model.CommitteeUser{Username: "first-last", Name: "First Last"}
	editor := &model.CommitteeUser{Username: "other-editor", Name: "Other Editor"}
	uploadedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	folderRef := func(uid string) *string { return &uid }
	fileData := validPDFFileData()

	// setup builds a repository holding two documents of committee-1 (doc-1 in existingFolder, or at the root when nil,
	// with its file, and doc-2) and one of committee-2 that shares doc-1's name, and the orchestrator over it. The
	// revision is doc-1's current one.
	setup := func(t *testing.T, existingFolder *string) (*mock.MockDocumentRepository, *mock.MockCommitteePublisher, service.CommitteeDocumentDataWriter, uint64) {
		t.Helper()
		repo := mock.NewMockDocumentRepository()
		for _, d := range []*model.CommitteeDocument{
			{
				UID: documentUID, CommitteeUID: committeeUID, FolderUID: existingFolder, Name: "Minutes", Description: "Old description",
				FileName: "minutes.pdf", FileSize: int64(len(fileData)), ContentType: "application/pdf",
				CreatedBy: creator, UpdatedBy: creator, CreatedAt: uploadedAt, UpdatedAt: uploadedAt,
			},
			{UID: "doc-2", CommitteeUID: committeeUID, Name: "Agenda", FileName: "agenda.pdf", ContentType: "application/pdf", CreatedAt: uploadedAt, UpdatedAt: uploadedAt},
			{UID: "doc-3", CommitteeUID: "committee-2", Name: "Minutes", FileName: "minutes.pdf", ContentType: "application/pdf", CreatedAt: uploadedAt, UpdatedAt: uploadedAt},
		} {
			require.NoError(t, repo.CreateDocumentMetadata(ctx, d))
		}
		require.NoError(t, repo.PutDocumentFile(ctx, documentUID, fileData))
		publisher := &mock.MockCommitteePublisher{}
		orch := service.NewDocumentWriterOrchestrator(
			service.WithDocumentWriter(repo),
			service.WithDocumentReaderForWriter(repo),
			service.WithDocumentPublisher(publisher),
		)
		_, revision, err := repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		return repo, publisher, orch, revision
	}

	// request is a complete update of doc-1 in committee-1; a test overrides what it is about.
	request := func(mutate func(*model.CommitteeDocument)) *model.CommitteeDocument {
		doc := &model.CommitteeDocument{
			UID:          documentUID,
			CommitteeUID: committeeUID,
			Name:         "Minutes 2026",
			Description:  "New description",
			UpdatedBy:    editor,
		}
		if mutate != nil {
			mutate(doc)
		}
		return doc
	}

	// requireUnchanged asserts doc-1 is exactly as setup left it, its name still held, and nothing was published.
	requireUnchanged := func(t *testing.T, repo *mock.MockDocumentRepository, publisher *mock.MockCommitteePublisher, revision uint64) {
		t.Helper()
		stored, storedRevision, err := repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		assert.Equal(t, "Minutes", stored.Name)
		assert.Equal(t, "Old description", stored.Description)
		assert.Equal(t, revision, storedRevision, "a refused update must not move the revision")
		assert.Empty(t, publisher.IndexerMessages, "a refused update must not tell the indexer anything")
		held, err := repo.DocumentNameExists(ctx, committeeUID, "Minutes")
		require.NoError(t, err)
		assert.True(t, held, "a refused update must keep the name it holds")
	}

	t.Run("replaces the editable fields and keeps the file, the identity and who uploaded it", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)
		before := time.Now().UTC()

		updated, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.FolderUID = folderRef("folder-a") }), revision, false)

		require.NoError(t, err)
		stored, storedRevision, err := repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		assert.Equal(t, updated, stored)
		assert.Equal(t, "Minutes 2026", stored.Name)
		assert.Equal(t, "New description", stored.Description)
		require.NotNil(t, stored.FolderUID)
		assert.Equal(t, "folder-a", *stored.FolderUID)
		assert.Equal(t, documentUID, stored.UID)
		assert.Equal(t, committeeUID, stored.CommitteeUID)
		assert.Equal(t, "minutes.pdf", stored.FileName, "the file is not editable")
		assert.Equal(t, int64(len(fileData)), stored.FileSize)
		assert.Equal(t, "application/pdf", stored.ContentType)
		assert.Equal(t, creator, stored.CreatedBy)
		assert.Equal(t, uploadedAt, stored.CreatedAt)
		assert.Equal(t, editor, stored.UpdatedBy)
		assert.False(t, stored.UpdatedAt.Before(before), "updated_at must be the time of the update")
		assert.Equal(t, revision+1, storedRevision)

		// The stored file is not touched.
		gotFile, err := repo.GetDocumentFile(ctx, documentUID)
		require.NoError(t, err)
		assert.Equal(t, fileData, gotFile)

		// The new name is held and the old one is free again, in this committee only.
		for _, c := range []struct {
			committee, name string
			want            bool
		}{
			{committeeUID, "Minutes 2026", true},
			{committeeUID, "Minutes", false},
			{"committee-2", "Minutes", true},
		} {
			exists, err := repo.DocumentNameExists(ctx, c.committee, c.name)
			require.NoError(t, err)
			assert.Equal(t, c.want, exists, "%s in %s", c.name, c.committee)
		}

		require.Len(t, publisher.IndexerMessages, 1)
		assert.Equal(t, constants.IndexCommitteeDocumentSubject, publisher.IndexerSubjects[0])
		message, ok := publisher.IndexerMessages[0].(*model.CommitteeIndexerMessage)
		require.True(t, ok, "the indexer is sent a committee indexer message")
		assert.Equal(t, model.ActionUpdated, message.Action)
		assert.Contains(t, message.Tags, "folder_uid:folder-a", "the folder tag is what a folder's contents are found by")
		require.NotNil(t, message.IndexingConfig)
		assert.Equal(t, "Minutes 2026", message.IndexingConfig.SortName)
	})

	t.Run("moves a document between folders and back to the root, and an empty folder uid is the root", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, folderRef("folder-a"))

		_, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Minutes"; d.FolderUID = folderRef("folder-b") }), revision, false)
		require.NoError(t, err)
		stored, revision, err := repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		require.NotNil(t, stored.FolderUID)
		assert.Equal(t, "folder-b", *stored.FolderUID)

		// An omitted folder is the root, so the next request names none.
		_, err = orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Minutes" }), revision, false)
		require.NoError(t, err)
		stored, revision, err = repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		assert.Nil(t, stored.FolderUID)

		_, err = orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Minutes"; d.FolderUID = folderRef("") }), revision, false)
		require.NoError(t, err)
		stored, _, err = repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		assert.Nil(t, stored.FolderUID, "an empty uid must not be stored as a folder that does not exist")

		require.Len(t, publisher.IndexerMessages, 3)
		rootMessage, ok := publisher.IndexerMessages[1].(*model.CommitteeIndexerMessage)
		require.True(t, ok)
		for _, tag := range rootMessage.Tags {
			assert.NotContains(t, tag, "folder_uid:", "a document at the root carries no folder tag")
		}
	})

	t.Run("clears the description when the request has none, and keeps the name when only that changes", func(t *testing.T) {
		repo, _, orch, revision := setup(t, nil)

		_, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Minutes"; d.Description = "" }), revision, false)

		require.NoError(t, err)
		stored, _, err := repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, err)
		assert.Equal(t, "Minutes", stored.Name)
		assert.Empty(t, stored.Description)
		held, err := repo.DocumentNameExists(ctx, committeeUID, "Minutes")
		require.NoError(t, err)
		assert.True(t, held, "keeping the name must keep holding it")
	})

	t.Run("a name another document in the committee holds is a conflict and changes nothing", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)

		_, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Agenda" }), revision, false)

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		requireUnchanged(t, repo, publisher, revision)
	})

	t.Run("a name only another committee holds is free", func(t *testing.T) {
		repo, _, orch, revision := setup(t, nil)
		require.NoError(t, repo.CreateDocumentMetadata(ctx, &model.CommitteeDocument{UID: "doc-4", CommitteeUID: "committee-2", Name: "Budget", FileName: "b.pdf", ContentType: "application/pdf"}))

		_, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Budget" }), revision, false)

		require.NoError(t, err)
	})

	t.Run("a revision that has moved on is a conflict and changes nothing", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)
		_, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Minutes" }), revision, false)
		require.NoError(t, err)
		publisher.IndexerMessages, publisher.IndexerSubjects = nil, nil

		// The same, now stale, revision again.
		_, err = orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.Name = "Lost update" }), revision, false)

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		stored, _, getErr := repo.GetDocumentMetadata(ctx, committeeUID, documentUID)
		require.NoError(t, getErr)
		assert.Equal(t, "Minutes", stored.Name, "the update that lost must not overwrite the one that won")
		assert.Empty(t, publisher.IndexerMessages)
	})

	t.Run("does not find a document of another committee", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)

		_, err := orch.UpdateDocument(ctx, request(func(d *model.CommitteeDocument) { d.CommitteeUID = "committee-2" }), revision, false)

		var notFound errs.NotFound
		require.ErrorAs(t, err, &notFound)
		requireUnchanged(t, repo, publisher, revision)
	})

	t.Run("refuses an incomplete request", func(t *testing.T) {
		tests := []struct {
			name   string
			doc    *model.CommitteeDocument
			wantIn string
		}{
			{name: "no document", doc: nil, wantIn: "document is required"},
			{name: "no document uid", doc: request(func(d *model.CommitteeDocument) { d.UID = "" }), wantIn: "document UID"},
			{name: "no committee uid", doc: request(func(d *model.CommitteeDocument) { d.CommitteeUID = "" }), wantIn: "committee UID"},
			{name: "no name", doc: request(func(d *model.CommitteeDocument) { d.Name = "" }), wantIn: "name"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				repo, publisher, orch, revision := setup(t, nil)

				_, err := orch.UpdateDocument(ctx, tt.doc, revision, false)

				var validation errs.Validation
				require.ErrorAs(t, err, &validation)
				assert.Contains(t, err.Error(), tt.wantIn)
				requireUnchanged(t, repo, publisher, revision)
			})
		}
	})
}
