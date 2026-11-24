// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// A folder handed back by the mock is the caller's to edit: renaming it must not change what is stored, so the
// uniqueness check still sees the name the folder held, and a refused rename leaves the folder and its name as they were.
func TestMockLinkRepository_UpdateLinkFolder(t *testing.T) {
	ctx := context.Background()
	repo := NewMockLinkRepository()
	require.NoError(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "f-1", CommitteeUID: "committee-1", Name: "Legal", CreatedBy: &model.CommitteeUser{Username: "creator"}}))
	require.NoError(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "f-2", CommitteeUID: "committee-1", Name: "Meetings"}))

	t.Run("a duplicate rename of a folder read back from the mock is a conflict and changes nothing", func(t *testing.T) {
		folder, revision, err := repo.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		folder.Name = "Meetings" // held by f-2
		folder.CreatedBy.Username = "someone-else"

		var conflict errs.Conflict
		require.ErrorAs(t, repo.UpdateLinkFolder(ctx, folder, revision), &conflict)

		stored, storedRevision, err := repo.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		assert.Equal(t, "Legal", stored.Name, "a refused rename must not change the stored folder")
		assert.Equal(t, "creator", stored.CreatedBy.Username, "the audit user is a copy too")
		assert.Equal(t, revision, storedRevision)
		require.ErrorAs(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "f-3", CommitteeUID: "committee-1", Name: "Legal"}), &conflict, "the old name is still held")
	})

	t.Run("a rename of a folder read back from the mock moves the name", func(t *testing.T) {
		folder, revision, err := repo.GetLinkFolder(ctx, "committee-1", "f-1")
		require.NoError(t, err)
		folder.Name = "Legal 2026"
		require.NoError(t, repo.UpdateLinkFolder(ctx, folder, revision))

		require.NoError(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "f-4", CommitteeUID: "committee-1", Name: "Legal"}), "the old name is free")
		var conflict errs.Conflict
		require.ErrorAs(t, repo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "f-5", CommitteeUID: "committee-1", Name: "Legal 2026"}), &conflict, "the new name is held")
	})
}
