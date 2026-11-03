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
