// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// MockDocumentRepository provides an in-memory mock implementation of CommitteeDocumentReaderWriter.
type MockDocumentRepository struct {
	mu                sync.RWMutex
	documents         map[string]*model.CommitteeDocument
	documentRevisions map[string]uint64
	files             map[string][]byte // documentUID -> file data
	nameKeys          map[string]string // uniqueKey -> documentUID
}

// NewMockDocumentRepository creates a new empty mock document repository.
func NewMockDocumentRepository() *MockDocumentRepository {
	return &MockDocumentRepository{
		documents:         make(map[string]*model.CommitteeDocument),
		documentRevisions: make(map[string]uint64),
		files:             make(map[string][]byte),
		nameKeys:          make(map[string]string),
	}
}

func (m *MockDocumentRepository) CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error {
	slog.DebugContext(ctx, "mock document repository: creating document metadata", "uid", doc.UID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.documents[doc.UID] = doc
	m.documentRevisions[doc.UID] = 1
	return nil
}

func (m *MockDocumentRepository) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	if documentUID == "" {
		return nil, 0, errs.NewValidation("document UID cannot be empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	doc, ok := m.documents[documentUID]
	if !ok || doc.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
	}
	return doc, m.documentRevisions[documentUID], nil
}

func (m *MockDocumentRepository) ListDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*model.CommitteeDocument
	for _, d := range m.documents {
		if d.CommitteeUID == committeeUID {
			result = append(result, d)
		}
	}
	return result, nil
}

func (m *MockDocumentRepository) PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error {
	slog.DebugContext(ctx, "mock document repository: storing file", "document_uid", documentUID, "size", len(fileData))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[documentUID] = fileData
	return nil
}

func (m *MockDocumentRepository) GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.files[documentUID]
	if !ok {
		return nil, errs.NewNotFound("document file not found", fmt.Errorf("document UID: %s", documentUID))
	}
	return data, nil
}

func (m *MockDocumentRepository) DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[documentUID]
	if !ok || doc.CommitteeUID != committeeUID {
		return errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
	}
	if m.documentRevisions[documentUID] != revision {
		return errs.NewConflict("document has been modified or deleted")
	}
	// Clean up name uniqueness key (mirrors NATS implementation)
	nameKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	delete(m.nameKeys, nameKey)
	delete(m.documents, documentUID)
	delete(m.documentRevisions, documentUID)
	return nil
}

func (m *MockDocumentRepository) UniqueDocumentName(ctx context.Context, doc *model.CommitteeDocument) (string, error) {
	uniqueKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.nameKeys[uniqueKey]; exists {
		return uniqueKey, errs.NewConflict("document with the same name already exists for this committee")
	}
	m.nameKeys[uniqueKey] = doc.UID
	return uniqueKey, nil
}

func (m *MockDocumentRepository) DeleteUniqueDocumentName(ctx context.Context, uniqueKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nameKeys, uniqueKey)
	return nil
}
