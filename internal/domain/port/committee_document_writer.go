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
	// A uid another document already holds is errs.Conflict too, and reserves nothing; an empty
	// document uid or committee uid is errs.Validation.
	CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error
	PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error
	// DeleteDocumentMetadata removes the document metadata. Whether the stored file goes with it is
	// up to the implementation: the NATS adapter removes it best-effort, the PostgreSQL store holds
	// metadata only and leaves it, so a caller that composes stores must call DeleteDocumentFile.
	// A stale revision is errs.Conflict, and a document that does not exist in the committee
	// errs.NotFound.
	DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error
	// DeleteDocumentFile removes the stored file for a document. Deleting a file that does not
	// exist is not an error.
	DeleteDocumentFile(ctx context.Context, documentUID string) error
}

// CommitteeDocumentFileDeleter removes the stored file of a document, which is all a committee delete
// needs of the document storage once the store has removed the metadata.
type CommitteeDocumentFileDeleter interface {
	// DeleteDocumentFile removes the stored file for a document. Deleting a file that does not
	// exist is not an error.
	DeleteDocumentFile(ctx context.Context, documentUID string) error
}

// CommitteeDocumentReaderWriter combines read and write interfaces for document storage.
type CommitteeDocumentReaderWriter interface {
	CommitteeDocumentReader
	CommitteeDocumentWriter
}
