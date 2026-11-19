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
	GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error)
	// DocumentNameExists reports whether a document with the given name already exists in the
	// committee. It is a fast pre-check only; CreateDocumentMetadata is the authoritative
	// uniqueness enforcement.
	DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error)
}

// CommitteeDocumentLister lists the documents of a committee. The link writer needs it to refuse deleting a folder that
// still holds a document, which the link reader cannot see: a folder is reached through the link store, but a document
// records its folder in its own metadata.
type CommitteeDocumentLister interface {
	// ListAllDocuments returns the committee's documents, or every document when committeeUID is empty. When it fails
	// part way it may return the documents it did read together with the error, so a caller looking for one document
	// must check what came back before it gives up on the error.
	ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
}
