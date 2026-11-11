// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDocumentLister answers the document list the link writer asks for when it checks that a folder is empty. A non-nil
// err is returned together with docs, as the NATS store does when some documents fail to read.
type fakeDocumentLister struct {
	docs []*model.CommitteeDocument
	err  error
}

func (f *fakeDocumentLister) ListAllDocuments(_ context.Context, _ string) ([]*model.CommitteeDocument, error) {
	return f.docs, f.err
}

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

func newLinkWriterOrch(storage *mockLinkStorage) service.CommitteeLinkDataWriter {
	return newLinkWriterOrchWithDocuments(storage, &fakeDocumentLister{})
}

func newLinkWriterOrchWithDocuments(storage *mockLinkStorage, documents *fakeDocumentLister) service.CommitteeLinkDataWriter {
	return service.NewLinkWriterOrchestrator(
		service.WithLinkWriter(storage),
		service.WithLinkReaderForWriter(storage),
		service.WithDocumentListerForWriter(documents),
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

func TestNewLinkWriterOrchestrator_RequiresDocumentLister(t *testing.T) {
	storage := newMockLinkStorage()

	// Without a lister a folder that only holds documents would look empty, so the constructor refuses to build one.
	assert.PanicsWithValue(t, "document lister is required for writer orchestrator", func() {
		service.NewLinkWriterOrchestrator(
			service.WithLinkWriter(storage),
			service.WithLinkReaderForWriter(storage),
		)
	})
}

func TestDeleteLinkFolder(t *testing.T) {
	const committeeUID = "committee-1"
	const folderUID = "folder-1"

	folderRef := func(uid string) *string { return &uid }

	tests := []struct {
		name      string
		links     []*model.CommitteeLink
		documents *fakeDocumentLister
		// wantErr is checked with errors.As; "" means the folder is deleted.
		wantValidation string
		wantListError  bool
		wantDeleted    bool
	}{
		{
			name:        "deletes an empty folder",
			documents:   &fakeDocumentLister{},
			wantDeleted: true,
		},
		{
			name: "ignores documents that are in another folder, at the top level, or in another committee",
			documents: &fakeDocumentLister{docs: []*model.CommitteeDocument{
				{UID: "doc-1", CommitteeUID: committeeUID, FolderUID: folderRef("other-folder")},
				{UID: "doc-2", CommitteeUID: committeeUID},
				{UID: "doc-3", CommitteeUID: "committee-2", FolderUID: folderRef(folderUID)},
			}},
			wantDeleted: true,
		},
		{
			name:           "refuses a folder that holds a link",
			links:          []*model.CommitteeLink{{UID: "link-1", CommitteeUID: committeeUID, FolderUID: folderRef(folderUID)}},
			documents:      &fakeDocumentLister{},
			wantValidation: "contains links",
		},
		{
			name: "refuses a folder that holds a document",
			documents: &fakeDocumentLister{docs: []*model.CommitteeDocument{
				{UID: "doc-1", CommitteeUID: committeeUID, FolderUID: folderRef(folderUID)},
			}},
			wantValidation: "contains documents",
		},
		{
			name:          "fails closed when the documents cannot be listed",
			documents:     &fakeDocumentLister{err: errors.New("documents bucket unavailable")},
			wantListError: true,
		},
		{
			name: "refuses on a document it did read even when the list also failed",
			documents: &fakeDocumentLister{
				docs: []*model.CommitteeDocument{{UID: "doc-1", CommitteeUID: committeeUID, FolderUID: folderRef(folderUID)}},
				err:  errors.New("1 document(s) failed to read during list"),
			},
			wantValidation: "contains documents",
		},
		{
			name: "fails closed when the list failed after reading only unrelated documents",
			documents: &fakeDocumentLister{
				docs: []*model.CommitteeDocument{{UID: "doc-1", CommitteeUID: committeeUID, FolderUID: folderRef("other-folder")}},
				err:  errors.New("1 document(s) failed to read during list"),
			},
			wantListError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := newMockLinkStorage()
			storage.folders[folderUID] = &model.CommitteeLinkFolder{UID: folderUID, CommitteeUID: committeeUID, Name: "Archive"}
			for _, link := range tt.links {
				storage.links[link.UID] = link
			}
			orch := newLinkWriterOrchWithDocuments(storage, tt.documents)

			err := orch.DeleteLinkFolder(context.Background(), committeeUID, folderUID, 1, false)

			switch {
			case tt.wantDeleted:
				require.NoError(t, err)
			case tt.wantListError:
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.documents.err)
			default:
				var validation errs.Validation
				require.ErrorAs(t, err, &validation)
				assert.Contains(t, err.Error(), tt.wantValidation)
			}

			_, stillThere := storage.folders[folderUID]
			assert.Equal(t, !tt.wantDeleted, stillThere, "the folder must be deleted only when it is empty")
		})
	}
}
