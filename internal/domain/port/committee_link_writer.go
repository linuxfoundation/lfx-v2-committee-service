// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeLinkWriter handles link and folder write operations.
type CommitteeLinkWriter interface {
	// CreateLink persists a link. A nil link, or one without a UID or a CommitteeUID, returns
	// errs.Validation, and a uid that another link already holds errs.Conflict.
	CreateLink(ctx context.Context, link *model.CommitteeLink) error
	// DeleteLink removes the link when the revision is still current: a stale revision returns
	// errs.Conflict, and a link that is missing or belongs to another committee errs.NotFound.
	DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64) error
	// CreateLinkFolder creates a folder. Folder names are unique per committee; a second create
	// with the same name returns errs.Conflict and leaves storage unchanged, and so does a uid that
	// another folder already holds. A nil folder, or one without a UID or a CommitteeUID, returns
	// errs.Validation.
	CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error
	// DeleteLinkFolder removes the folder when the revision is still current: a stale revision returns
	// errs.Conflict, and a folder that is missing or belongs to another committee errs.NotFound. Its
	// name is free again; the links in it are left alone.
	DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error
}

// CommitteeLinkReaderWriter combines read and write interfaces for link/folder storage.
type CommitteeLinkReaderWriter interface {
	CommitteeLinkReader
	CommitteeLinkWriter
}
