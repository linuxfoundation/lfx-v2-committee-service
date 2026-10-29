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
	// CreateLinkFolder creates a folder. Folder names are unique per committee; a second create
	// with the same name returns errs.Conflict and leaves storage unchanged.
	CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error
	DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error
}

// CommitteeLinkReaderWriter combines read and write interfaces for link/folder storage.
type CommitteeLinkReaderWriter interface {
	CommitteeLinkReader
	CommitteeLinkWriter
}
