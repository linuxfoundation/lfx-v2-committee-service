// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func testAuditUser() *model.CommitteeUser {
	return &model.CommitteeUser{Name: "First Last", Username: "first-last", Email: "first.last@example.com", Avatar: "https://example.com/avatar.png"}
}

// testLink returns a link with every field set.
func testLink(uid, committeeUID string) *model.CommitteeLink {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	folder := "f-1"
	return &model.CommitteeLink{
		UID: uid, CommitteeUID: committeeUID, FolderUID: &folder, Name: "Charter", URL: "https://example.com/charter",
		Description: "The charter", CreatedBy: testAuditUser(), UpdatedBy: testAuditUser(), CreatedAt: created, UpdatedAt: created.Add(time.Hour),
	}
}

func testFolder(uid, committeeUID, name string) *model.CommitteeLinkFolder {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &model.CommitteeLinkFolder{
		UID: uid, CommitteeUID: committeeUID, Name: name, CreatedBy: testAuditUser(), UpdatedBy: testAuditUser(),
		CreatedAt: created, UpdatedAt: created.Add(time.Hour),
	}
}

func linkUIDs(links []*model.CommitteeLink) []string {
	uids := []string{}
	for _, l := range links {
		uids = append(uids, l.UID)
	}
	return uids
}

func folderUIDs(folders []*model.CommitteeLinkFolder) []string {
	uids := []string{}
	for _, f := range folders {
		uids = append(uids, f.UID)
	}
	return uids
}

func seedLinks(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	for _, l := range []*model.CommitteeLink{testLink("l-1", "committee-1"), testLink("l-2", "committee-1"), testLink("l-3", "committee-2")} {
		require.NoError(t, store.CreateLink(context.Background(), l))
	}
	return store
}

func seedFolders(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	for _, f := range []*model.CommitteeLinkFolder{
		testFolder("f-1", "committee-1", "Legal"), testFolder("f-2", "committee-1", "Meetings"), testFolder("f-3", "committee-2", "Legal"),
	} {
		require.NoError(t, store.CreateLinkFolder(context.Background(), f))
	}
	return store
}

// raceWriters runs write from several goroutines at once and returns how many succeeded. Every
// failure must be a conflict.
func raceWriters(t *testing.T, write func() error) int {
	t.Helper()
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = write()
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
			continue
		}
		requireConflict(t, err)
	}
	return wins
}

func TestStore_CreateLink(t *testing.T) {
	ctx := context.Background()
	store := seedLinks(t)

	t.Run("round-trips every field", func(t *testing.T) {
		want := testLink("l-new", "committee-1")
		require.NoError(t, store.CreateLink(ctx, want))
		got, revision, err := store.GetLink(ctx, "committee-1", "l-new")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, want, got)
	})

	t.Run("a link with no folder or audit users round-trips as empty", func(t *testing.T) {
		want := testLink("l-bare", "committee-1")
		want.FolderUID, want.CreatedBy, want.UpdatedBy, want.Description = nil, nil, nil, ""
		require.NoError(t, store.CreateLink(ctx, want))
		got, _, err := store.GetLink(ctx, "committee-1", "l-bare")
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("two links may share a name; the same uid is a conflict; a nil link is invalid", func(t *testing.T) {
		require.NoError(t, store.CreateLink(ctx, testLink("l-same-name", "committee-1")))
		requireConflict(t, store.CreateLink(ctx, testLink("l-1", "committee-9")))
		var validation errs.Validation
		require.ErrorAs(t, store.CreateLink(ctx, nil), &validation)
	})
}

func TestStore_GetLink(t *testing.T) {
	ctx := context.Background()
	store := seedLinks(t)

	link, _, err := store.GetLink(ctx, "committee-1", "l-1")
	require.NoError(t, err)
	assert.Equal(t, "l-1", link.UID)

	_, _, err = store.GetLink(ctx, "committee-1", "missing")
	requireNotFound(t, err)
	_, _, err = store.GetLink(ctx, "committee-2", "l-1")
	requireNotFound(t, err)

	var validation errs.Validation
	_, _, err = store.GetLink(ctx, "committee-1", "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListLinks(t *testing.T) {
	ctx := context.Background()
	store := seedLinks(t)

	links, err := store.ListLinks(ctx, "committee-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"l-1", "l-2"}, linkUIDs(links))

	links, err = store.ListLinks(ctx, "no-such-committee")
	require.NoError(t, err)
	assert.Empty(t, links)
}

func TestStore_ListAllLinks(t *testing.T) {
	ctx := context.Background()
	store := seedLinks(t)

	links, err := store.ListAllLinks(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"l-1", "l-2", "l-3"}, linkUIDs(links))

	links, err = store.ListAllLinks(ctx, "committee-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"l-3"}, linkUIDs(links))
}

func TestStore_UpdateLink(t *testing.T) {
	ctx := context.Background()
	store := seedLinks(t)
	current := func(t *testing.T, uid string) (*model.CommitteeLink, uint64) {
		t.Helper()
		link, revision, err := store.GetLink(ctx, "committee-1", uid)
		require.NoError(t, err)
		return link, revision
	}

	t.Run("updates the link and replaces the revision", func(t *testing.T) {
		link, revision := current(t, "l-1")
		link.Name, link.FolderUID = "Renamed", nil
		require.NoError(t, store.UpdateLink(ctx, link, revision))
		got, newRevision := current(t, "l-1")
		assert.Equal(t, link, got)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		link, revision := current(t, "l-2")
		link.Name = "first writer"
		require.NoError(t, store.UpdateLink(ctx, link, revision))
		link.Name = "second writer"
		requireConflict(t, store.UpdateLink(ctx, link, revision))
		got, _ := current(t, "l-2")
		assert.Equal(t, "first writer", got.Name)
	})

	t.Run("a missing link is not found and a nil one is invalid", func(t *testing.T) {
		link, revision := current(t, "l-2")
		link.UID = "missing"
		requireNotFound(t, store.UpdateLink(ctx, link, revision))
		var validation errs.Validation
		require.ErrorAs(t, store.UpdateLink(ctx, nil, 1), &validation)
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, revision := current(t, "l-1")
		wins := raceWriters(t, func() error {
			link := *original
			return store.UpdateLink(ctx, &link, revision)
		})
		assert.Equal(t, 1, wins)
	})
}

func TestStore_DeleteLink(t *testing.T) {
	ctx := context.Background()
	store := seedLinks(t)
	_, revision, err := store.GetLink(ctx, "committee-1", "l-1")
	require.NoError(t, err)

	var validation errs.Validation
	require.ErrorAs(t, store.DeleteLink(ctx, "committee-1", "", revision), &validation)
	requireConflict(t, store.DeleteLink(ctx, "committee-1", "l-1", revision+1000))
	// A revision beyond the column range never matches a row.
	requireConflict(t, store.DeleteLink(ctx, "committee-1", "l-1", math.MaxUint64))
	requireNotFound(t, store.DeleteLink(ctx, "committee-2", "l-1", revision))
	_, _, err = store.GetLink(ctx, "committee-1", "l-1")
	require.NoError(t, err, "a rejected delete changes nothing")

	require.NoError(t, store.DeleteLink(ctx, "committee-1", "l-1", revision))
	_, _, err = store.GetLink(ctx, "committee-1", "l-1")
	requireNotFound(t, err)
	requireNotFound(t, store.DeleteLink(ctx, "committee-1", "l-1", revision))
}

func TestStore_CreateLinkFolder(t *testing.T) {
	ctx := context.Background()
	store := seedFolders(t)

	t.Run("round-trips every field", func(t *testing.T) {
		want := testFolder("f-new", "committee-1", "Reports")
		require.NoError(t, store.CreateLinkFolder(ctx, want))
		got, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-new")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, want, got)
	})

	t.Run("a name already in the committee is a conflict, but only on an exact match", func(t *testing.T) {
		requireConflict(t, store.CreateLinkFolder(ctx, testFolder("f-dup", "committee-1", "Legal")))
		_, _, err := store.GetLinkFolder(ctx, "committee-1", "f-dup")
		requireNotFound(t, err)

		require.NoError(t, store.CreateLinkFolder(ctx, testFolder("f-case", "committee-1", "legal")))
		require.NoError(t, store.CreateLinkFolder(ctx, testFolder("f-other", "committee-3", "Legal")))
	})

	t.Run("the same uid is a conflict; a nil folder is invalid", func(t *testing.T) {
		requireConflict(t, store.CreateLinkFolder(ctx, testFolder("f-1", "committee-9", "Elsewhere")))
		var validation errs.Validation
		require.ErrorAs(t, store.CreateLinkFolder(ctx, nil), &validation)
	})
}

func TestStore_GetLinkFolder(t *testing.T) {
	ctx := context.Background()
	store := seedFolders(t)

	folder, _, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
	require.NoError(t, err)
	assert.Equal(t, "Legal", folder.Name)

	_, _, err = store.GetLinkFolder(ctx, "committee-1", "missing")
	requireNotFound(t, err)
	_, _, err = store.GetLinkFolder(ctx, "committee-2", "f-1")
	requireNotFound(t, err)

	var validation errs.Validation
	_, _, err = store.GetLinkFolder(ctx, "committee-1", "")
	require.ErrorAs(t, err, &validation)
}

func TestStore_ListLinkFolders(t *testing.T) {
	ctx := context.Background()
	store := seedFolders(t)

	folders, err := store.ListLinkFolders(ctx, "committee-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"f-1", "f-2"}, folderUIDs(folders))

	folders, err = store.ListLinkFolders(ctx, "no-such-committee")
	require.NoError(t, err)
	assert.Empty(t, folders)
}

func TestStore_ListAllLinkFolders(t *testing.T) {
	ctx := context.Background()
	store := seedFolders(t)

	folders, err := store.ListAllLinkFolders(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"f-1", "f-2", "f-3"}, folderUIDs(folders))

	folders, err = store.ListAllLinkFolders(ctx, "committee-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"f-3"}, folderUIDs(folders))
}

func TestStore_UpdateLinkFolder(t *testing.T) {
	ctx := context.Background()
	store := seedFolders(t)
	current := func(t *testing.T, uid string) (*model.CommitteeLinkFolder, uint64) {
		t.Helper()
		folder, revision, err := store.GetLinkFolder(ctx, "committee-1", uid)
		require.NoError(t, err)
		return folder, revision
	}

	t.Run("updates the folder and replaces the revision", func(t *testing.T) {
		folder, revision := current(t, "f-1")
		folder.Name = "Legal documents"
		require.NoError(t, store.UpdateLinkFolder(ctx, folder, revision))
		got, newRevision := current(t, "f-1")
		assert.Equal(t, folder, got)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("a stale revision is a conflict and changes nothing", func(t *testing.T) {
		folder, revision := current(t, "f-2")
		folder.Name = "first writer"
		require.NoError(t, store.UpdateLinkFolder(ctx, folder, revision))
		folder.Name = "second writer"
		requireConflict(t, store.UpdateLinkFolder(ctx, folder, revision))
		got, _ := current(t, "f-2")
		assert.Equal(t, "first writer", got.Name)
	})

	t.Run("taking another folder's name in the committee is a conflict", func(t *testing.T) {
		require.NoError(t, store.CreateLinkFolder(ctx, testFolder("f-taken", "committee-1", "Taken")))
		folder, revision := current(t, "f-2")
		folder.Name = "Taken"
		requireConflict(t, store.UpdateLinkFolder(ctx, folder, revision))
	})

	t.Run("a missing folder is not found and a nil one is invalid", func(t *testing.T) {
		folder, revision := current(t, "f-2")
		folder.UID = "missing"
		requireNotFound(t, store.UpdateLinkFolder(ctx, folder, revision))
		var validation errs.Validation
		require.ErrorAs(t, store.UpdateLinkFolder(ctx, nil, 1), &validation)
	})
}

func TestStore_DeleteLinkFolder(t *testing.T) {
	ctx := context.Background()
	store := seedFolders(t)
	_, revision, err := store.GetLinkFolder(ctx, "committee-1", "f-1")
	require.NoError(t, err)

	var validation errs.Validation
	require.ErrorAs(t, store.DeleteLinkFolder(ctx, "committee-1", "", revision), &validation)
	requireConflict(t, store.DeleteLinkFolder(ctx, "committee-1", "f-1", revision+1000))
	requireNotFound(t, store.DeleteLinkFolder(ctx, "committee-2", "f-1", revision))
	_, _, err = store.GetLinkFolder(ctx, "committee-1", "f-1")
	require.NoError(t, err, "a rejected delete changes nothing")

	require.NoError(t, store.DeleteLinkFolder(ctx, "committee-1", "f-1", revision))
	requireNotFound(t, store.DeleteLinkFolder(ctx, "committee-1", "f-1", revision))

	// The name is free again, so a new folder can take it.
	require.NoError(t, store.CreateLinkFolder(ctx, testFolder("f-again", "committee-1", "Legal")))
}
