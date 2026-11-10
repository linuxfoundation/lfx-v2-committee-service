// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// kvRepairStore is the KV store as the copy back from PostgreSQL uses it. The documents' metadata is created by the
// document store, which owns the document name lookup keys; everything else is the main storage.
type kvRepairStore struct {
	*storage
	documents port.CommitteeDocumentReaderWriter
}

var _ port.KVRepairStore = (*kvRepairStore)(nil)

// NewKVRepairStore returns the KV store for the tooling that copies the data back from PostgreSQL.
func NewKVRepairStore(client *NATSClient) port.KVRepairStore {
	return &kvRepairStore{storage: NewStorageBackend(client), documents: NewDocumentStorage(client)}
}

// CreateDocumentMetadata creates the metadata of a document, reserving its name for the committee.
func (r *kvRepairStore) CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error {
	return r.documents.CreateDocumentMetadata(ctx, doc)
}
