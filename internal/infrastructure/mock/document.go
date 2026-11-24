// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

var _ port.CommitteeDocumentLister = (*MockDocumentRepository)(nil)

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
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	if doc.UID == "" || doc.CommitteeUID == "" {
		return errs.NewValidation("document UID and CommitteeUID must be non-empty")
	}
	slog.DebugContext(ctx, "mock document repository: creating document metadata", "uid", doc.UID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.documents[doc.UID]; taken {
		return errs.NewConflict("document already exists")
	}
	nameKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	if _, exists := m.nameKeys[nameKey]; exists {
		return errs.NewConflict("document with the same name already exists for this committee")
	}
	copied := *doc
	m.documents[doc.UID] = &copied
	m.documentRevisions[doc.UID] = 1
	m.nameKeys[nameKey] = doc.UID
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
	copied := *doc
	return &copied, m.documentRevisions[documentUID], nil
}

func (m *MockDocumentRepository) PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error {
	slog.DebugContext(ctx, "mock document repository: storing file", "document_uid", documentUID, "size", len(fileData))
	m.mu.Lock()
	defer m.mu.Unlock()
	buf := make([]byte, len(fileData))
	copy(buf, fileData)
	m.files[documentUID] = buf
	return nil
}

func (m *MockDocumentRepository) GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.files[documentUID]
	if !ok {
		return nil, errs.NewNotFound("document file not found", fmt.Errorf("document UID: %s", documentUID))
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	return buf, nil
}

// UpdateDocumentMetadata replaces the metadata found by its uid, like the real stores: a missing document is not found,
// a stale revision is a conflict, and a changed name moves the name key, so a name another document holds is a
// conflict that changes nothing. The revision moves on, so the next update needs the new one.
func (m *MockDocumentRepository) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.documents[doc.UID]
	if !ok {
		return errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", doc.UID))
	}
	if m.documentRevisions[doc.UID] != revision {
		return errs.NewConflict("document has been modified since it was last read")
	}
	oldKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, current.BuildIndexKey(ctx))
	newKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	if newKey != oldKey {
		if _, taken := m.nameKeys[newKey]; taken {
			return errs.NewConflict("document with the same name already exists for this committee")
		}
		delete(m.nameKeys, oldKey)
		m.nameKeys[newKey] = doc.UID
	}
	copied := *doc
	m.documents[doc.UID] = &copied
	m.documentRevisions[doc.UID] = revision + 1
	return nil
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
	// Clean up file data (mirrors NATS adapter's fire-and-forget file deletion)
	delete(m.files, documentUID)
	return nil
}

func (m *MockDocumentRepository) DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error) {
	probe := &model.CommitteeDocument{CommitteeUID: committeeUID, Name: name}
	nameKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, probe.BuildIndexKey(ctx))
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.nameKeys[nameKey]
	return exists, nil
}

// ListAllDocuments returns the committee's documents, or every document when committeeUID is empty, in uid order.
func (m *MockDocumentRepository) ListAllDocuments(_ context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	docs := make([]*model.CommitteeDocument, 0, len(m.documents))
	for _, doc := range m.documents {
		if committeeUID == "" || doc.CommitteeUID == committeeUID {
			docs = append(docs, doc)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].UID < docs[j].UID })
	return docs, nil
}

func (m *MockDocumentRepository) DeleteDocumentFile(_ context.Context, documentUID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, documentUID)
	return nil
}
