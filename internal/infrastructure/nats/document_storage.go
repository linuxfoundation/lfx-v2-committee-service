// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go/jetstream"
)

// documentStorage is a dedicated infrastructure adapter for committee document storage.
// It uses NATS KV for metadata and NATS Object Store for file data.
// Keeping this separate from the shared storage struct allows swapping to S3 (or another
// backend) in the future by implementing port.CommitteeDocumentReaderWriter in a new adapter
// and updating only the provider.
type documentStorage struct {
	client *NATSClient
}

// NewDocumentStorage constructs a NATS-backed CommitteeDocumentReaderWriter.
func NewDocumentStorage(client *NATSClient) port.CommitteeDocumentReaderWriter {
	return &documentStorage{client: client}
}

// getMetadata retrieves and JSON-unmarshals a document metadata entry from the KV bucket.
func (ds *documentStorage) getMetadata(ctx context.Context, uid string, target any) (uint64, error) {
	entry, err := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Get(ctx, uid)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(entry.Value(), target); err != nil {
		return 0, errs.NewUnexpected("failed to unmarshal document metadata", err)
	}
	return entry.Revision(), nil
}

func (ds *documentStorage) CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	docBytes, errMarshal := json.Marshal(doc)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal document", errMarshal)
	}
	rev, errCreate := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Create(ctx, doc.UID, docBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create document metadata", errCreate)
	}
	slog.DebugContext(ctx, "created document metadata in NATS storage",
		"document_uid", doc.UID,
		"committee_uid", doc.CommitteeUID,
		"revision", rev,
	)
	return nil
}

func (ds *documentStorage) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	if documentUID == "" {
		return nil, 0, errs.NewValidation("document UID cannot be empty")
	}
	doc := &model.CommitteeDocument{}
	rev, errGet := ds.getMetadata(ctx, documentUID, doc)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
		}
		return nil, 0, errs.NewUnexpected("failed to get document metadata", errGet)
	}
	if doc.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("document not found", fmt.Errorf("document UID: %s does not belong to committee: %s", documentUID, committeeUID))
	}
	return doc, rev, nil
}

func (ds *documentStorage) PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error {
	reader := bytes.NewReader(fileData)
	_, errPut := ds.client.objStore[constants.ObjectStoreNameCommitteeDocuments].Put(ctx, jetstream.ObjectMeta{
		Name: documentUID,
	}, reader)
	if errPut != nil {
		return errs.NewUnexpected("failed to store document file", errPut)
	}
	slog.DebugContext(ctx, "stored document file in NATS object store", "document_uid", documentUID, "size", len(fileData))
	return nil
}

func (ds *documentStorage) GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error) {
	result, errGet := ds.client.objStore[constants.ObjectStoreNameCommitteeDocuments].Get(ctx, documentUID)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrObjectNotFound) {
			return nil, errs.NewNotFound("document file not found", fmt.Errorf("document UID: %s", documentUID))
		}
		return nil, errs.NewUnexpected("failed to get document file", errGet)
	}
	defer func() { _ = result.Close() }()
	data, errRead := io.ReadAll(result)
	if errRead != nil {
		return nil, errs.NewUnexpected("failed to read document file data", errRead)
	}
	return data, nil
}

func (ds *documentStorage) UniqueDocumentName(ctx context.Context, doc *model.CommitteeDocument) (string, error) {
	uniqueKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	_, errUnique := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Create(ctx, uniqueKey, []byte(doc.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return uniqueKey, errs.NewConflict("document with the same name already exists for this committee")
		}
		return uniqueKey, errs.NewUnexpected("failed to create unique key for document name", errUnique)
	}
	return uniqueKey, nil
}

func (ds *documentStorage) DeleteUniqueDocumentName(ctx context.Context, uniqueKey string) error {
	errPurge := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Purge(ctx, uniqueKey)
	if errPurge != nil && !errors.Is(errPurge, jetstream.ErrKeyNotFound) {
		return errs.NewUnexpected("failed to delete document name uniqueness key", errPurge)
	}
	return nil
}

func (ds *documentStorage) DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error {
	doc, _, errGet := ds.GetDocumentMetadata(ctx, committeeUID, documentUID)
	if errGet != nil {
		return errGet
	}
	errDelete := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Delete(ctx, documentUID, jetstream.LastRevision(revision))
	if errDelete != nil {
		if errors.Is(errDelete, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
		}
		var jsErr jetstream.JetStreamError
		if errors.As(errDelete, &jsErr) {
			if apiErr := jsErr.APIError(); apiErr != nil && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence {
				return errs.NewConflict("document has been modified since it was last read")
			}
		}
		return errs.NewUnexpected("failed to delete document metadata", errDelete)
	}
	// Best-effort cleanup of the name uniqueness lookup key; log if it fails
	uniqueKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	if errPurge := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Purge(ctx, uniqueKey); errPurge != nil {
		slog.WarnContext(ctx, "failed to purge document lookup key", "key", uniqueKey, "error", errPurge)
	}
	// Best-effort deletion of the binary file from object store.
	// Metadata is already gone so the file is API-inaccessible even if this fails.
	// Logged for ops visibility; does not fail the delete response.
	if errDel := ds.client.objStore[constants.ObjectStoreNameCommitteeDocuments].Delete(ctx, documentUID); errDel != nil {
		if !errors.Is(errDel, jetstream.ErrObjectNotFound) {
			slog.WarnContext(ctx, "failed to delete document file from object store (non-fatal)",
				"document_uid", documentUID, "error", errDel)
		}
	}
	slog.DebugContext(ctx, "deleted document metadata from NATS storage",
		"document_uid", documentUID,
		"committee_uid", committeeUID,
		"revision", revision,
	)
	return nil
}
