// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLinkStorage struct {
	links   map[string]*model.CommitteeLink
	folders map[string]*model.CommitteeLinkFolder
}

func newMockLinkStorage() *mockLinkStorage {
	return &mockLinkStorage{
		links:   make(map[string]*model.CommitteeLink),
		folders: make(map[string]*model.CommitteeLinkFolder),
	}
}

func (m *mockLinkStorage) CreateLink(_ context.Context, link *model.CommitteeLink) error {
	m.links[link.UID] = link
	return nil
}

func (m *mockLinkStorage) GetLink(_ context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error) {
	link, ok := m.links[linkUID]
	if !ok || link.CommitteeUID != committeeUID {
		return nil, 0, nil
	}
	return link, 1, nil
}

func (m *mockLinkStorage) ListLinks(_ context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	var result []*model.CommitteeLink
	for _, l := range m.links {
		if l.CommitteeUID == committeeUID {
			result = append(result, l)
		}
	}
	return result, nil
}

func (m *mockLinkStorage) DeleteLink(_ context.Context, _, linkUID string, _ uint64) error {
	delete(m.links, linkUID)
	return nil
}

func (m *mockLinkStorage) CreateLinkFolder(_ context.Context, folder *model.CommitteeLinkFolder) error {
	m.folders[folder.UID] = folder
	return nil
}

func (m *mockLinkStorage) GetLinkFolder(_ context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error) {
	folder, ok := m.folders[folderUID]
	if !ok || folder.CommitteeUID != committeeUID {
		return nil, 0, nil
	}
	return folder, 1, nil
}

func (m *mockLinkStorage) ListLinkFolders(_ context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	var result []*model.CommitteeLinkFolder
	for _, f := range m.folders {
		if f.CommitteeUID == committeeUID {
			result = append(result, f)
		}
	}
	return result, nil
}

func (m *mockLinkStorage) DeleteLinkFolder(_ context.Context, _, folderUID string, _ uint64) error {
	delete(m.folders, folderUID)
	return nil
}

func (m *mockLinkStorage) UniqueLinkFolderName(_ context.Context, _ *model.CommitteeLinkFolder) (string, error) {
	return "lookup-key", nil
}

func (m *mockLinkStorage) DeleteUniqueLinkFolderName(_ context.Context, _ string) error {
	return nil
}

func newLinkWriterOrch(storage *mockLinkStorage) service.CommitteeLinkDataWriter {
	return service.NewLinkWriterOrchestrator(
		service.WithLinkWriter(storage),
		service.WithLinkReaderForWriter(storage),
	)
}

func TestCreateLink_Success(t *testing.T) {
	storage := newMockLinkStorage()
	orch := newLinkWriterOrch(storage)

	link, err := orch.CreateLink(context.Background(), &model.CommitteeLink{
		CommitteeUID: "committee-1",
		Name:         "Linux Foundation",
		URL:          "https://linuxfoundation.org",
	}, false)

	require.NoError(t, err)
	assert.NotEmpty(t, link.UID)
	assert.Equal(t, "Linux Foundation", link.Name)
	assert.False(t, link.CreatedAt.IsZero())
}

func TestCreateLink_MissingName_ReturnsError(t *testing.T) {
	storage := newMockLinkStorage()
	orch := newLinkWriterOrch(storage)

	_, err := orch.CreateLink(context.Background(), &model.CommitteeLink{
		CommitteeUID: "committee-1",
		URL:          "https://example.com",
	}, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestCreateLink_MissingURL_ReturnsError(t *testing.T) {
	storage := newMockLinkStorage()
	orch := newLinkWriterOrch(storage)

	_, err := orch.CreateLink(context.Background(), &model.CommitteeLink{
		CommitteeUID: "committee-1",
		Name:         "Some Link",
	}, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "URL")
}

func TestCreateLinkFolder_Success(t *testing.T) {
	storage := newMockLinkStorage()
	orch := newLinkWriterOrch(storage)

	folder, err := orch.CreateLinkFolder(context.Background(), &model.CommitteeLinkFolder{
		CommitteeUID: "committee-1",
		Name:         "Meeting Notes",
	}, false)

	require.NoError(t, err)
	assert.NotEmpty(t, folder.UID)
	assert.Equal(t, "Meeting Notes", folder.Name)
	assert.False(t, folder.CreatedAt.IsZero())
}

func TestCreateLinkFolder_MissingName_ReturnsError(t *testing.T) {
	storage := newMockLinkStorage()
	orch := newLinkWriterOrch(storage)

	_, err := orch.CreateLinkFolder(context.Background(), &model.CommitteeLinkFolder{
		CommitteeUID: "committee-1",
	}, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}
