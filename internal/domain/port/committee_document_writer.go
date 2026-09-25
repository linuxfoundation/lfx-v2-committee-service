// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeDocumentWriter handles document write operations.
type CommitteeDocumentWriter interface {
	CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error
	PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error
	DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error
	// UniqueDocumentName enforces uniqueness of document name per committee. Returns the lookup key.
	UniqueDocumentName(ctx context.Context, doc *model.CommitteeDocument) (string, error)
	// DeleteUniqueDocumentName removes a document name uniqueness reservation by its lookup key.
	DeleteUniqueDocumentName(ctx context.Context, uniqueKey string) error
}

// CommitteeDocumentReaderWriter combines read and write interfaces for document storage.
type CommitteeDocumentReaderWriter interface {
	CommitteeDocumentReader
	CommitteeDocumentWriter
}
