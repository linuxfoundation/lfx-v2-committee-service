// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeLinkWriter handles link and folder write operations.
type CommitteeLinkWriter interface {
	CreateLink(ctx context.Context, link *model.CommitteeLink) error
	DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64) error
	CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error
	DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error
	// UniqueLinkFolderName enforces uniqueness of folder name per committee. Returns the lookup key.
	UniqueLinkFolderName(ctx context.Context, folder *model.CommitteeLinkFolder) (string, error)
	// DeleteUniqueLinkFolderName removes a folder name uniqueness reservation by its lookup key.
	DeleteUniqueLinkFolderName(ctx context.Context, uniqueKey string) error
}

// CommitteeLinkReaderWriter combines read and write interfaces for link/folder storage.
type CommitteeLinkReaderWriter interface {
	CommitteeLinkReader
	CommitteeLinkWriter
}
