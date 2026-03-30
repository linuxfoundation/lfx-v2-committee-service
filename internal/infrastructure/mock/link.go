// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// MockLinkRepository provides an in-memory mock implementation of CommitteeLinkReaderWriter.
type MockLinkRepository struct {
	mu      sync.RWMutex
	links   map[string]*model.CommitteeLink
	folders map[string]*model.CommitteeLinkFolder
	// revision tracking
	linkRevisions   map[string]uint64
	folderRevisions map[string]uint64
	// unique folder name lookup keys
	folderNameKeys map[string]string // uniqueKey -> folderUID
}

// NewMockLinkRepository creates a new empty mock link repository.
func NewMockLinkRepository() *MockLinkRepository {
	return &MockLinkRepository{
		links:           make(map[string]*model.CommitteeLink),
		folders:         make(map[string]*model.CommitteeLinkFolder),
		linkRevisions:   make(map[string]uint64),
		folderRevisions: make(map[string]uint64),
		folderNameKeys:  make(map[string]string),
	}
}

func (m *MockLinkRepository) CreateLink(ctx context.Context, link *model.CommitteeLink) error {
	slog.DebugContext(ctx, "mock link repository: creating link", "uid", link.UID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[link.UID] = link
	m.linkRevisions[link.UID] = 1
	return nil
}

func (m *MockLinkRepository) GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error) {
	if linkUID == "" {
		return nil, 0, errs.NewValidation("link UID cannot be empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	link, ok := m.links[linkUID]
	if !ok || link.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("link not found", fmt.Errorf("link UID: %s", linkUID))
	}
	return link, m.linkRevisions[linkUID], nil
}

func (m *MockLinkRepository) ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*model.CommitteeLink
	for _, l := range m.links {
		if l.CommitteeUID == committeeUID {
			result = append(result, l)
		}
	}
	return result, nil
}

func (m *MockLinkRepository) DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	link, ok := m.links[linkUID]
	if !ok || link.CommitteeUID != committeeUID {
		return errs.NewNotFound("link not found", fmt.Errorf("link UID: %s", linkUID))
	}
	if m.linkRevisions[linkUID] != revision {
		return errs.NewConflict("link has been modified or deleted")
	}
	delete(m.links, linkUID)
	delete(m.linkRevisions, linkUID)
	return nil
}

func (m *MockLinkRepository) CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error {
	slog.DebugContext(ctx, "mock link repository: creating folder", "uid", folder.UID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.folders[folder.UID] = folder
	m.folderRevisions[folder.UID] = 1
	return nil
}

func (m *MockLinkRepository) GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error) {
	if folderUID == "" {
		return nil, 0, errs.NewValidation("folder UID cannot be empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	folder, ok := m.folders[folderUID]
	if !ok || folder.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("folder not found", fmt.Errorf("folder UID: %s", folderUID))
	}
	return folder, m.folderRevisions[folderUID], nil
}

func (m *MockLinkRepository) ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*model.CommitteeLinkFolder
	for _, f := range m.folders {
		if f.CommitteeUID == committeeUID {
			result = append(result, f)
		}
	}
	return result, nil
}

func (m *MockLinkRepository) DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	folder, ok := m.folders[folderUID]
	if !ok || folder.CommitteeUID != committeeUID {
		return errs.NewNotFound("folder not found", fmt.Errorf("folder UID: %s", folderUID))
	}
	if m.folderRevisions[folderUID] != revision {
		return errs.NewConflict("folder has been modified or deleted")
	}
	delete(m.folders, folderUID)
	delete(m.folderRevisions, folderUID)
	return nil
}

func (m *MockLinkRepository) UniqueLinkFolderName(ctx context.Context, folder *model.CommitteeLinkFolder) (string, error) {
	uniqueKey := fmt.Sprintf("%s|%s", folder.CommitteeUID, folder.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.folderNameKeys[uniqueKey]; exists {
		return uniqueKey, errs.NewConflict("folder with the same name already exists for this committee")
	}
	m.folderNameKeys[uniqueKey] = folder.UID
	return uniqueKey, nil
}

func (m *MockLinkRepository) DeleteUniqueLinkFolderName(ctx context.Context, uniqueKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.folderNameKeys, uniqueKey)
	return nil
}
