// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
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

func (m *mockLinkStorage) UpdateLink(_ context.Context, link *model.CommitteeLink, _ uint64) error {
	m.links[link.UID] = link
	return nil
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

func TestUpdateLink(t *testing.T) {
	const committeeUID = "committee-1"
	const linkUID = "link-1"

	ctx := context.Background()
	creator := &model.CommitteeUser{Username: "first-last", Name: "First Last"}
	editor := &model.CommitteeUser{Username: "other-editor", Name: "Other Editor"}
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	folderRef := func(uid string) *string { return &uid }

	// setup builds a repository holding one link in committee-1 (in existingFolder, or at the root when nil), two folders
	// of committee-1 and one of committee-2, and the orchestrator over it. The revision is the link's current one.
	setup := func(t *testing.T, existingFolder *string) (*mock.MockLinkRepository, *mock.MockCommitteePublisher, service.CommitteeLinkDataWriter, uint64) {
		t.Helper()
		repo := mock.NewMockLinkRepository()
		require.NoError(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "folder-a", CommitteeUID: committeeUID, Name: "Folder A"}))
		require.NoError(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "folder-b", CommitteeUID: committeeUID, Name: "Folder B"}))
		require.NoError(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "folder-other", CommitteeUID: "committee-2", Name: "Elsewhere"}))
		require.NoError(t, repo.CreateLink(ctx, &model.CommitteeLink{
			UID:          linkUID,
			CommitteeUID: committeeUID,
			FolderUID:    existingFolder,
			Name:         "Old name",
			URL:          "https://example.com/old",
			Description:  "Old description",
			CreatedBy:    creator,
			UpdatedBy:    creator,
			CreatedAt:    createdAt,
			UpdatedAt:    createdAt,
		}))
		publisher := &mock.MockCommitteePublisher{}
		orch := service.NewLinkWriterOrchestrator(
			service.WithLinkWriter(repo),
			service.WithLinkReaderForWriter(repo),
			service.WithDocumentListerForWriter(&fakeDocumentLister{}),
			service.WithLinkPublisher(publisher),
		)
		_, revision, err := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		return repo, publisher, orch, revision
	}

	// request is a complete update of linkUID in committee-1; a test overrides what it is about.
	request := func(mutate func(*model.CommitteeLink)) *model.CommitteeLink {
		link := &model.CommitteeLink{
			UID:          linkUID,
			CommitteeUID: committeeUID,
			Name:         "New name",
			URL:          "https://example.com/new",
			Description:  "New description",
			UpdatedBy:    editor,
		}
		if mutate != nil {
			mutate(link)
		}
		return link
	}

	// requireUnchanged asserts the link is exactly as setup left it and nothing was published.
	requireUnchanged := func(t *testing.T, repo *mock.MockLinkRepository, publisher *mock.MockCommitteePublisher, revision uint64) {
		t.Helper()
		stored, storedRevision, err := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		assert.Equal(t, "Old name", stored.Name)
		assert.Equal(t, "https://example.com/old", stored.URL)
		assert.Equal(t, revision, storedRevision, "a refused update must not move the revision")
		assert.Empty(t, publisher.IndexerMessages, "a refused update must not tell the indexer anything")
	}

	t.Run("replaces the editable fields and keeps the identity and who created the link", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)
		before := time.Now().UTC()

		updated, err := orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.FolderUID = folderRef("folder-a") }), revision, false)

		require.NoError(t, err)
		stored, storedRevision, err := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		assert.Equal(t, updated, stored)
		assert.Equal(t, "New name", stored.Name)
		assert.Equal(t, "https://example.com/new", stored.URL)
		assert.Equal(t, "New description", stored.Description)
		require.NotNil(t, stored.FolderUID)
		assert.Equal(t, "folder-a", *stored.FolderUID)
		assert.Equal(t, linkUID, stored.UID)
		assert.Equal(t, committeeUID, stored.CommitteeUID)
		assert.Equal(t, creator, stored.CreatedBy)
		assert.Equal(t, createdAt, stored.CreatedAt)
		assert.Equal(t, editor, stored.UpdatedBy)
		assert.False(t, stored.UpdatedAt.Before(before), "updated_at must be the time of the update")
		assert.Equal(t, revision+1, storedRevision)

		require.Len(t, publisher.IndexerMessages, 1)
		assert.Equal(t, constants.IndexCommitteeLinkSubject, publisher.IndexerSubjects[0])
		message, ok := publisher.IndexerMessages[0].(*model.CommitteeIndexerMessage)
		require.True(t, ok, "the indexer is sent a committee indexer message")
		assert.Equal(t, model.ActionUpdated, message.Action)
		assert.Contains(t, message.Tags, "folder_uid:folder-a", "the folder tag is what a folder's contents are found by")
		require.NotNil(t, message.IndexingConfig)
		assert.Equal(t, "New name", message.IndexingConfig.SortName)
		assert.Contains(t, message.IndexingConfig.ParentRefs, "committee_link_folder:folder-a")
	})

	t.Run("moves a link between folders and then back to the root", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, folderRef("folder-a"))

		_, err := orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.FolderUID = folderRef("folder-b") }), revision, false)
		require.NoError(t, err)
		stored, revision, err := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		require.NotNil(t, stored.FolderUID)
		assert.Equal(t, "folder-b", *stored.FolderUID)

		// An omitted folder is the root, so the next request names none.
		_, err = orch.UpdateLink(ctx, request(nil), revision, false)
		require.NoError(t, err)
		stored, _, err = repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		assert.Nil(t, stored.FolderUID)

		require.Len(t, publisher.IndexerMessages, 2)
		rootMessage, ok := publisher.IndexerMessages[1].(*model.CommitteeIndexerMessage)
		require.True(t, ok, "the indexer is sent a committee indexer message")
		for _, tag := range rootMessage.Tags {
			assert.NotContains(t, tag, "folder_uid:", "a link at the root carries no folder tag")
		}
		assert.Equal(t, []string{"committee:" + committeeUID}, rootMessage.IndexingConfig.ParentRefs)
	})

	t.Run("treats an empty folder uid as the root", func(t *testing.T) {
		repo, _, orch, revision := setup(t, folderRef("folder-a"))

		_, err := orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.FolderUID = folderRef("") }), revision, false)

		require.NoError(t, err)
		stored, _, err := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		assert.Nil(t, stored.FolderUID, "an empty uid must not be stored as a folder that does not exist")
	})

	t.Run("clears the description when the request has none", func(t *testing.T) {
		repo, _, orch, revision := setup(t, nil)

		_, err := orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.Description = "" }), revision, false)

		require.NoError(t, err)
		stored, _, err := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, err)
		assert.Empty(t, stored.Description)
	})

	t.Run("a revision that has moved on is a conflict and changes nothing", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)

		_, err := orch.UpdateLink(ctx, request(nil), revision, false)
		require.NoError(t, err)
		publisher.IndexerMessages, publisher.IndexerSubjects = nil, nil

		// The same, now stale, revision again.
		_, err = orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.Name = "Lost update" }), revision, false)

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		stored, _, getErr := repo.GetLink(ctx, committeeUID, linkUID)
		require.NoError(t, getErr)
		assert.Equal(t, "New name", stored.Name, "the update that lost must not overwrite the one that won")
		assert.Empty(t, publisher.IndexerMessages)
	})

	t.Run("refuses a target folder that is missing or belongs to another committee", func(t *testing.T) {
		for _, folder := range []string{"no-such-folder", "folder-other"} {
			t.Run(folder, func(t *testing.T) {
				repo, publisher, orch, revision := setup(t, nil)

				_, err := orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.FolderUID = folderRef(folder) }), revision, false)

				// The folder is named in the request body, so this is a bad request, not a missing link.
				var validation errs.Validation
				require.ErrorAs(t, err, &validation)
				assert.Contains(t, err.Error(), "does not exist in this committee")
				requireUnchanged(t, repo, publisher, revision)
			})
		}
	})

	t.Run("does not find a link of another committee", func(t *testing.T) {
		repo, publisher, orch, revision := setup(t, nil)

		_, err := orch.UpdateLink(ctx, request(func(l *model.CommitteeLink) { l.CommitteeUID = "committee-2" }), revision, false)

		var notFound errs.NotFound
		require.ErrorAs(t, err, &notFound)
		requireUnchanged(t, repo, publisher, revision)
	})

	t.Run("refuses an incomplete request", func(t *testing.T) {
		tests := []struct {
			name   string
			link   *model.CommitteeLink
			wantIn string
		}{
			{name: "no link", link: nil, wantIn: "link is required"},
			{name: "no link uid", link: request(func(l *model.CommitteeLink) { l.UID = "" }), wantIn: "link UID"},
			{name: "no committee uid", link: request(func(l *model.CommitteeLink) { l.CommitteeUID = "" }), wantIn: "committee UID"},
			{name: "no name", link: request(func(l *model.CommitteeLink) { l.Name = "" }), wantIn: "name"},
			{name: "no url", link: request(func(l *model.CommitteeLink) { l.URL = "" }), wantIn: "URL"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				repo, publisher, orch, revision := setup(t, nil)

				_, err := orch.UpdateLink(ctx, tt.link, revision, false)

				var validation errs.Validation
				require.ErrorAs(t, err, &validation)
				assert.Contains(t, err.Error(), tt.wantIn)
				requireUnchanged(t, repo, publisher, revision)
			})
		}
	})
}
