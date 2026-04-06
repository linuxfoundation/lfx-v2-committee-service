// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
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
	m.documents[doc.UID] = doc
	m.revisions[doc.UID] = 1
	return nil
}

func (m *mockDocStorage) GetDocumentMetadata(_ context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	doc, ok := m.documents[documentUID]
	if !ok || doc.CommitteeUID != committeeUID {
		return nil, 0, errors.New("document not found")
	}
	return doc, m.revisions[documentUID], nil
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

func (m *mockDocStorage) UniqueDocumentName(_ context.Context, doc *model.CommitteeDocument) (string, error) {
	uniqueKey := doc.CommitteeUID + "|" + doc.Name
	if _, exists := m.nameKeys[uniqueKey]; exists {
		return uniqueKey, errs.NewConflict("document with the same name already exists for this committee")
	}
	m.nameKeys[uniqueKey] = doc.UID
	return uniqueKey, nil
}

func (m *mockDocStorage) DeleteUniqueDocumentName(_ context.Context, uniqueKey string) error {
	delete(m.nameKeys, uniqueKey)
	return nil
}

// errorPublisher always returns an error from all publish methods.
type errorPublisher struct{}

func (e *errorPublisher) Indexer(_ context.Context, _ string, _ any, _ bool) error {
	return errors.New("indexer unavailable")
}
func (e *errorPublisher) Access(_ context.Context, _ string, _ any, _ bool) error {
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
