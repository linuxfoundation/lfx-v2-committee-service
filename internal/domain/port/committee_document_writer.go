// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeDocumentWriter handles document write operations.
type CommitteeDocumentWriter interface {
	// CreateDocumentMetadata creates the document metadata. Document names are unique per
	// committee; a second create with the same name returns errs.Conflict and leaves storage
	// unchanged.
	CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error
	PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error
	DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error
	// DeleteDocumentFile removes the stored file for a document. Deleting a file that does not
	// exist is not an error.
	DeleteDocumentFile(ctx context.Context, documentUID string) error
}

// CommitteeDocumentReaderWriter combines read and write interfaces for document storage.
type CommitteeDocumentReaderWriter interface {
	CommitteeDocumentReader
	CommitteeDocumentWriter
}
