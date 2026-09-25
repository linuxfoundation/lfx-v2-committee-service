// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// DocumentAuditSyncStorage provides list/update access for document audit user migration.
type DocumentAuditSyncStorage interface {
	ListAllLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error)
	ListAllLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error)
	ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
	GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error)
	GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error)
	GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
	UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error
	UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error
	UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error
}
