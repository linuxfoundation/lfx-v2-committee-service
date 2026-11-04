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
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// LinkStore is what the link and folder suite needs of a backend: the link and folder reader and writer,
// plus the list and update methods the audit-sync tooling uses.
type LinkStore interface {
	port.CommitteeLinkReaderWriter
	port.DocumentAuditSyncStorage
}

// LinkFactory builds a fresh, empty link store.
type LinkFactory func(t *testing.T) LinkStore

func linkUIDs(links []*model.CommitteeLink) []string {
	uids := make([]string, 0, len(links))
	for _, l := range links {
		uids = append(uids, l.UID)
	}
	return uids
}

func folderUIDs(folders []*model.CommitteeLinkFolder) []string {
	uids := make([]string, 0, len(folders))
	for _, f := range folders {
		uids = append(uids, f.UID)
	}
	return uids
}

// RunLinkContract runs the committee link cases against the backend: create, get, list, update with the
// revision, and delete scoped to the committee.
func RunLinkContract(t *testing.T, backend Backend, newStore LinkFactory) {
	t.Run(backend.Name, func(t *testing.T) { runLinkContract(t, newStore) })
}

func runLinkContract(t *testing.T, newStore LinkFactory) {
	ctx := context.Background()
	seed := func(t *testing.T) LinkStore {
		t.Helper()
		store := newStore(t)
		for _, l := range []*model.CommitteeLink{NewLink("l-1", "committee-1"), NewLink("l-2", "committee-1"), NewLink("l-3", "committee-2")} {
			require.NoError(t, store.CreateLink(ctx, l))
		}
		return store
	}

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := NewLink("l-1", "committee-1")
		require.NoError(t, store.CreateLink(ctx, want))
		got, revision, err := store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeLink(want), normalizeLink(got))
	})

	t.Run("a link with no folder, description or audit users reads back empty", func(t *testing.T) {
		store := newStore(t)
		want := NewLink("l-bare", "committee-1")
		want.FolderUID, want.CreatedBy, want.UpdatedBy, want.Description = nil, nil, nil, ""
		require.NoError(t, store.CreateLink(ctx, want))
		got, _, err := store.GetLink(ctx, "committee-1", "l-bare")
		require.NoError(t, err)
		assert.Equal(t, normalizeLink(want), normalizeLink(got))
	})

	t.Run("a link is found within its committee only, and an empty uid is invalid", func(t *testing.T) {
		store := seed(t)
		_, _, err := store.GetLink(ctx, "committee-1", "missing")
		requireNotFound(t, err)
		_, _, err = store.GetLink(ctx, "committee-2", "l-1")
		requireNotFound(t, err)
		_, _, err = store.GetLink(ctx, "committee-1", "")
		requireValidation(t, err)
	})

	t.Run("create rejects a nil link and missing uids", func(t *testing.T) {
		store := newStore(t)
		requireValidation(t, store.CreateLink(ctx, nil))
		requireValidation(t, store.CreateLink(ctx, NewLink("", "committee-1")))
		requireValidation(t, store.CreateLink(ctx, NewLink("l-x", "")))
	})

	t.Run("two links may share a name, and the same uid is a conflict", func(t *testing.T) {
		store := seed(t)
		require.NoError(t, store.CreateLink(ctx, NewLink("l-same-name", "committee-1")))
		requireConflict(t, store.CreateLink(ctx, NewLink("l-1", "committee-9")))
		other, _, err := store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err)
		assert.Equal(t, "committee-1", other.CommitteeUID, "a refused create changes nothing")
	})

	t.Run("ListLinks lists the committee's links, and ListAllLinks every link or the committee's", func(t *testing.T) {
		store := newStore(t)
		links, err := store.ListAllLinks(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, links)

		store = seed(t)
		links, err = store.ListLinks(ctx, "committee-1")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"l-1", "l-2"}, linkUIDs(links))
		links, err = store.ListLinks(ctx, "no-such-committee")
		require.NoError(t, err)
		assert.Empty(t, links)
		links, err = store.ListAllLinks(ctx, "")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"l-1", "l-2", "l-3"}, linkUIDs(links))
		links, err = store.ListAllLinks(ctx, "committee-2")
		require.NoError(t, err)
		assert.Equal(t, []string{"l-3"}, linkUIDs(links))
	})

	t.Run("UpdateLink replaces the link and the revision", func(t *testing.T) {
		store := seed(t)
		link, revision, err := store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err)
		link.Name = "Renamed"
		link.URL = "https://example.org/renamed"
		link.Description = "Updated"
		link.FolderUID = nil
		link.UpdatedBy = &model.CommitteeUser{Name: "First Last", Username: "first-last-2", Email: "first.last2@example.com"}
		require.NoError(t, store.UpdateLink(ctx, link, revision))

		got, newRevision, err := store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err)
		assert.Equal(t, normalizeLink(link), normalizeLink(got), "every edited field is stored")
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("UpdateLink with a stale revision fails and changes nothing", func(t *testing.T) {
		store := seed(t)
		link, revision, err := store.GetLink(ctx, "committee-1", "l-2")
		require.NoError(t, err)
		link.Name = "first writer"
		require.NoError(t, store.UpdateLink(ctx, link, revision))

		link.Name = "second writer"
		requireConflict(t, store.UpdateLink(ctx, link, revision))
		got, _, err := store.GetLink(ctx, "committee-1", "l-2")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.Name)
	})

	t.Run("UpdateLink of a missing link is not found, and a nil link is invalid", func(t *testing.T) {
		store := newStore(t)
		requireNotFound(t, store.UpdateLink(ctx, NewLink("ghost", "committee-1"), 1))
		requireValidation(t, store.UpdateLink(ctx, nil, 1))
	})

	t.Run("DeleteLink removes the link within its committee, with the revision", func(t *testing.T) {
		store := seed(t)
		_, revision, err := store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err)

		requireValidation(t, store.DeleteLink(ctx, "committee-1", "", revision))
		requireConflict(t, store.DeleteLink(ctx, "committee-1", "l-1", revision+1000))
		requireNotFound(t, store.DeleteLink(ctx, "committee-2", "l-1", revision))
		_, _, err = store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err, "a rejected delete changes nothing")

		require.NoError(t, store.DeleteLink(ctx, "committee-1", "l-1", revision))
		_, _, err = store.GetLink(ctx, "committee-1", "l-1")
		requireNotFound(t, err)
		requireNotFound(t, store.DeleteLink(ctx, "committee-1", "l-1", revision))
		links, err := store.ListLinks(ctx, "committee-1")
		require.NoError(t, err)
		assert.Equal(t, []string{"l-2"}, linkUIDs(links))
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := seed(t)
		link, revision, err := store.GetLink(ctx, "committee-1", "l-1")
		require.NoError(t, err)
		wins := race(t, func(writer int) error {
			update := *link
			update.Description = fmt.Sprintf("writer %d", writer)
			return store.UpdateLink(ctx, &update, revision)
		}, requireConflict)
		assert.Equal(t, 1, wins)
	})
}

// RunFolderContract runs the committee link folder cases against the backend: create, get, list, update
// with the revision, delete, and the name unique per committee.
func RunFolderContract(t *testing.T, backend Backend, newStore LinkFactory) {
	t.Run(backend.Name, func(t *testing.T) { runFolderContract(t, newStore) })
}

func runFolderContract(t *testing.T, newStore LinkFactory) {
	ctx := context.Background()
	seed := func(t *testing.T) LinkStore {
		t.Helper()
		store := newStore(t)
		for _, f := range []*model.CommitteeLinkFolder{
			NewFolder("f-1", "committee-1", "Legal"), NewFolder("f-2", "committee-1", "Meetings"), NewFolder("f-3", "committee-2", "Legal"),
		} {
			require.NoError(t, store.CreateLinkFolder(ctx, f))
		}
		return store
	}

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := NewFolder("f-1", "committee-1", "Legal")
		require.NoError(t, store.CreateLinkFolder(ctx, want))
		got, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeFolder(want), normalizeFolder(got))
	})

	t.Run("a folder is found within its committee only, and an empty uid is invalid", func(t *testing.T) {
		store := seed(t)
		_, _, err := store.GetLinkFolder(ctx, "committee-1", "missing")
		requireNotFound(t, err)
		_, _, err = store.GetLinkFolder(ctx, "committee-2", "f-1")
		requireNotFound(t, err)
		_, _, err = store.GetLinkFolder(ctx, "committee-1", "")
		requireValidation(t, err)
	})

	t.Run("create rejects a nil folder and missing uids", func(t *testing.T) {
		store := newStore(t)
		requireValidation(t, store.CreateLinkFolder(ctx, nil))
		requireValidation(t, store.CreateLinkFolder(ctx, NewFolder("", "committee-1", "Legal")))
		requireValidation(t, store.CreateLinkFolder(ctx, NewFolder("f-x", "", "Legal")))
	})

	t.Run("a folder name is unique per committee, and case-sensitive", func(t *testing.T) {
		store := seed(t)
		requireConflict(t, store.CreateLinkFolder(ctx, NewFolder("f-dup", "committee-1", "Legal")))
		_, _, err := store.GetLinkFolder(ctx, "committee-1", "f-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateLinkFolder(ctx, NewFolder("f-case", "committee-1", "legal")))
		require.NoError(t, store.CreateLinkFolder(ctx, NewFolder("f-other", "committee-3", "Legal")))
	})

	t.Run("the same uid is a conflict, whatever else the second folder holds", func(t *testing.T) {
		store := seed(t)
		requireConflict(t, store.CreateLinkFolder(ctx, NewFolder("f-1", "committee-9", "Elsewhere")))
		// A refused create reserves nothing: that name is still free in that committee.
		require.NoError(t, store.CreateLinkFolder(ctx, NewFolder("f-new", "committee-9", "Elsewhere")))

		// The same committee and name as the folder that holds the uid, but other content: not a create that
		// already landed.
		impostor := NewFolder("f-1", "committee-1", "Legal")
		impostor.CreatedBy = &model.CommitteeUser{Name: "First Last", Username: "first-last-impostor", Email: "first.last2@example.com"}
		requireConflict(t, store.CreateLinkFolder(ctx, impostor))
		got, _, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		assert.Equal(t, "first-last", got.CreatedBy.Username, "the stored folder is the first one")
	})

	t.Run("ListLinkFolders lists the committee's folders, and ListAllLinkFolders every folder or the committee's", func(t *testing.T) {
		store := newStore(t)
		folders, err := store.ListAllLinkFolders(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, folders)

		store = seed(t)
		folders, err = store.ListLinkFolders(ctx, "committee-1")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"f-1", "f-2"}, folderUIDs(folders))
		folders, err = store.ListLinkFolders(ctx, "no-such-committee")
		require.NoError(t, err)
		assert.Empty(t, folders)
		folders, err = store.ListAllLinkFolders(ctx, "")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"f-1", "f-2", "f-3"}, folderUIDs(folders))
		folders, err = store.ListAllLinkFolders(ctx, "committee-2")
		require.NoError(t, err)
		assert.Equal(t, []string{"f-3"}, folderUIDs(folders))
	})

	t.Run("UpdateLinkFolder replaces the folder and the revision", func(t *testing.T) {
		store := seed(t)
		folder, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		folder.UpdatedBy = &model.CommitteeUser{Name: "First Last", Username: "first-last-2", Email: "first.last2@example.com"}
		require.NoError(t, store.UpdateLinkFolder(ctx, folder, revision))

		got, newRevision, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		assert.Equal(t, normalizeFolder(folder), normalizeFolder(got))
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("UpdateLinkFolder with a stale revision fails and changes nothing", func(t *testing.T) {
		store := seed(t)
		folder, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-2")
		require.NoError(t, err)
		folder.UpdatedBy = &model.CommitteeUser{Name: "First Last", Username: "first-last-first", Email: "first.last2@example.com"}
		require.NoError(t, store.UpdateLinkFolder(ctx, folder, revision))

		folder.UpdatedBy = &model.CommitteeUser{Name: "First Last", Username: "first-last-second", Email: "first.last3@example.com"}
		requireConflict(t, store.UpdateLinkFolder(ctx, folder, revision))
		got, _, err := store.GetLinkFolder(ctx, "committee-1", "f-2")
		require.NoError(t, err)
		assert.Equal(t, "first-last-first", got.UpdatedBy.Username)
	})

	t.Run("UpdateLinkFolder of a missing folder is not found, and a nil folder is invalid", func(t *testing.T) {
		store := newStore(t)
		requireNotFound(t, store.UpdateLinkFolder(ctx, NewFolder("ghost", "committee-1", "Ghost"), 1))
		requireValidation(t, store.UpdateLinkFolder(ctx, nil, 1))
	})

	t.Run("DeleteLinkFolder removes the folder within its committee, frees its name, and leaves its links", func(t *testing.T) {
		store := seed(t)
		require.NoError(t, store.CreateLink(ctx, NewLink("l-in-folder", "committee-1")))
		_, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)

		requireValidation(t, store.DeleteLinkFolder(ctx, "committee-1", "", revision))
		requireConflict(t, store.DeleteLinkFolder(ctx, "committee-1", "f-1", revision+1000))
		requireNotFound(t, store.DeleteLinkFolder(ctx, "committee-2", "f-1", revision))
		_, _, err = store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err, "a rejected delete changes nothing")
		// A rejected delete leaves the name taken, in this committee and in the other.
		requireConflict(t, store.CreateLinkFolder(ctx, NewFolder("f-dup", "committee-1", "Legal")))
		requireConflict(t, store.CreateLinkFolder(ctx, NewFolder("f-dup", "committee-2", "Legal")))

		require.NoError(t, store.DeleteLinkFolder(ctx, "committee-1", "f-1", revision))
		_, _, err = store.GetLinkFolder(ctx, "committee-1", "f-1")
		requireNotFound(t, err)
		requireNotFound(t, store.DeleteLinkFolder(ctx, "committee-1", "f-1", revision))
		_, _, err = store.GetLink(ctx, "committee-1", "l-in-folder")
		require.NoError(t, err, "the links of a deleted folder are left alone")

		require.NoError(t, store.CreateLinkFolder(ctx, NewFolder("f-again", "committee-1", "Legal")), "the name is free again")
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := seed(t)
		folder, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		wins := race(t, func(writer int) error {
			update := *folder
			update.UpdatedBy = &model.CommitteeUser{Name: "First Last", Username: fmt.Sprintf("first-last-%d", writer), Email: "first.last2@example.com"}
			return store.UpdateLinkFolder(ctx, &update, revision)
		}, requireConflict)
		assert.Equal(t, 1, wins)
	})
}
