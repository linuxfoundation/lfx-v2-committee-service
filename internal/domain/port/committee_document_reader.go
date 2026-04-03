// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeDocumentReader handles document read operations.
type CommitteeDocumentReader interface {
	GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
	ListDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
	GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error)
}
