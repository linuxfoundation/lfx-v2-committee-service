// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeLinkReader handles link and folder reading operations.
type CommitteeLinkReader interface {
	GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error)
	ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error)
	GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error)
	ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error)
}
