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
// DocumentRevision and DocumentCommittee are not part of port.CommitteeDocumentReaderWriter, which NewDocumentStorage
// returns, so the tests and the composite store reach them through this interface: keep them implemented.
var _ interface {
	DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
	DocumentCommittee(ctx context.Context, documentUID string) (string, error)
} = (*documentStorage)(nil)

// documentStorage is what the service gets in kv mode, so it must list the documents: the API stops at startup when its
// document store is not a port.CommitteeDocumentLister.
var _ port.CommitteeDocumentLister = (*documentStorage)(nil)

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

// CreateDocumentMetadata creates the document metadata. It first reserves the (committee, document
// name) uniqueness lookup key and returns errs.Conflict if the name is already taken. A uid that
// another document holds is a conflict too, and releases the name it reserved. If writing the
// metadata record fails otherwise and the record is confirmed absent, the key is released so the
// name is not left locked.
func (ds *documentStorage) CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	if doc.UID == "" || doc.CommitteeUID == "" {
		return errs.NewValidation("document UID and CommitteeUID must be non-empty")
	}
	docBytes, errMarshal := json.Marshal(doc)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal document", errMarshal)
	}

	kv := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments]

	uniqueKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	uniqueRev, errUnique := kv.Create(ctx, uniqueKey, []byte(doc.UID))
	if errUnique != nil {
		if isJetStreamCASConflict(errUnique) {
			return errs.NewConflict("document with the same name already exists for this committee")
		}
		return errs.NewUnexpected("failed to create unique key for document name", errUnique)
	}

	rev, errCreate := kv.Create(ctx, doc.UID, docBytes)
	if errCreate != nil {
		if isJetStreamCASConflict(errCreate) {
			// A different document already holds the uid, so the name reserved for this one is nobody's.
			releaseLookupKey(ctx, kv, uniqueKey, uniqueRev, errCreate, "document_uid", doc.UID, "committee_uid", doc.CommitteeUID)
			return errs.NewConflict("document already exists")
		}
		rollbackLookupKey(ctx, kv, doc.UID, uniqueKey, errCreate, "document_uid", doc.UID, "committee_uid", doc.CommitteeUID)
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

// DocumentNameExists reports whether the name is already reserved in the committee. It reads the
// same uniqueness lookup key that CreateDocumentMetadata reserves.
func (ds *documentStorage) DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error) {
	probe := &model.CommitteeDocument{CommitteeUID: committeeUID, Name: name}
	uniqueKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, probe.BuildIndexKey(ctx))
	if _, errGet := ds.client.kvStore[constants.KVBucketNameCommitteeDocuments].Get(ctx, uniqueKey); errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return false, nil
		}
		return false, errs.NewUnexpected("failed to check document name", errGet)
	}
	return true, nil
}

// ListAllDocuments lists the committee's documents, or every document when committeeUID is empty. It is the same list the
// audit sync uses (see storage.ListAllDocuments), exposed here because in kv mode this adapter is the whole document
// store: the link writer lists the documents through it to refuse deleting a folder that still holds one. When some
// documents fail to read it returns the ones it did read together with the error.
func (ds *documentStorage) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	return (&storage{client: ds.client}).ListAllDocuments(ctx, committeeUID)
}

// DeleteDocumentFile removes the file from the object store. A missing file is not an error.
func (ds *documentStorage) DeleteDocumentFile(ctx context.Context, documentUID string) error {
	if errDel := ds.client.objStore[constants.ObjectStoreNameCommitteeDocuments].Delete(ctx, documentUID); errDel != nil {
		if errors.Is(errDel, jetstream.ErrObjectNotFound) {
			return nil
		}
		return errs.NewUnexpected("failed to delete document file", errDel)
	}
	return nil
}

// DocumentRevision returns the revision of the document found by its uid alone, whichever committee it belongs to, for the
// callers that replace a document by uid, as the audit sync does. A document that does not exist is errs.NotFound.
func (ds *documentStorage) DocumentRevision(ctx context.Context, documentUID string) (uint64, error) {
	if documentUID == "" {
		return 0, errs.NewValidation("document UID cannot be empty")
	}
	rev, errGet := ds.getMetadata(ctx, documentUID, &model.CommitteeDocument{})
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return 0, errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
		}
		return 0, errs.NewUnexpected("failed to get document revision", errGet)
	}
	return rev, nil
}

// DocumentCommittee returns the committee the document belongs to, found by its uid alone. A document that does not exist is
// errs.NotFound.
func (ds *documentStorage) DocumentCommittee(ctx context.Context, documentUID string) (string, error) {
	if documentUID == "" {
		return "", errs.NewValidation("document UID cannot be empty")
	}
	doc := &model.CommitteeDocument{}
	if _, errGet := ds.getMetadata(ctx, documentUID, doc); errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return "", errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
		}
		return "", errs.NewUnexpected("failed to get document committee", errGet)
	}
	return doc.CommitteeUID, nil
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
		return writeError(ctx, ds.client.kvStore[constants.KVBucketNameCommitteeDocuments], documentUID,
			"document not found", "document has been modified since it was last read", "failed to delete document metadata", errDelete)
	}
	// Best-effort cleanup of the name uniqueness lookup key; log if it fails
	uniqueKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	purgeOwnedLookupKey(ctx, ds.client.kvStore[constants.KVBucketNameCommitteeDocuments], uniqueKey, documentUID)
	// Best-effort deletion of the binary file from object store.
	// Metadata is already gone so the file is API-inaccessible even if this fails.
	// Logged for ops visibility; does not fail the delete response.
	if errDel := ds.DeleteDocumentFile(ctx, documentUID); errDel != nil {
		slog.WarnContext(ctx, "failed to delete document file from object store (non-fatal)",
			"document_uid", documentUID, "error", errDel)
	}
	slog.DebugContext(ctx, "deleted document metadata from NATS storage",
		"document_uid", documentUID,
		"committee_uid", committeeUID,
		"revision", revision,
	)
	return nil
}
