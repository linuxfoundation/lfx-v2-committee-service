// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// DocumentMetadataBackend is what the composite needs of a store of the committee documents' metadata: the request path
// and the list and update the audit-sync tooling uses. The PostgreSQL store is one.
type DocumentMetadataBackend interface {
	CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error
	GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
	DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error)
	UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error
	DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error
	ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
}

// DocumentKVBackend is the NATS side of the documents: the metadata in KV and the files in the Object Store.
type DocumentKVBackend interface {
	DocumentMetadataBackend
	port.CommitteeDocumentReaderWriter
}

// Documents is the composite of the committee documents, built like Store: the primary store decides every result of the
// metadata and is embedded, so every method not overridden goes to it unchanged. In dual mode each write of the metadata
// is copied to PostgreSQL from what KV holds after it, with the revision KV gave it, and a sampled fraction of reads is
// compared; see Store for what that guarantees.
//
// The files are not copied and are not compared: PostgreSQL holds metadata only, and the file of a document is in the NATS
// Object Store in every mode, so the KV store is needed in postgres mode too, for the file reads, the file writes and
// the deletion of a file. Nothing in KV is written for the metadata in postgres mode.
//
// A file and its metadata are two writes the caller makes one after the other, and the composite keeps that order: the
// file is put first, then the metadata is created, and the caller removes the file when the create fails. Deleting the
// metadata removes the file with it, whichever store decided: the NATS adapter does it itself, and in postgres mode the composite does after the metadata is gone, best
// effort and logged, since the metadata is already gone and the file can no longer be reached through the API.
type Documents struct {
	DocumentMetadataBackend
	core
	kv DocumentKVBackend
	pg DocumentMetadataBackend
}

var _ port.CommitteeDocumentReaderWriter = (*Documents)(nil)

// NewDocuments returns the composite of the documents for the mode; see New. kv is needed in every mode, for the files.
func NewDocuments(mode Mode, kv DocumentKVBackend, pg DocumentMetadataBackend, opts ...Option) (*Documents, error) {
	if kv == nil {
		return nil, errors.New("the documents need the KV store in every storage mode, for the files")
	}
	primary, err := selectPrimary(mode, DocumentMetadataBackend(kv), pg)
	if err != nil {
		return nil, err
	}
	return &Documents{DocumentMetadataBackend: primary, core: newCore(mode, opts), kv: kv, pg: pg}, nil
}

// GetDocumentFile returns the file of a document, which is in the Object Store in every mode.
func (d *Documents) GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error) {
	return d.kv.GetDocumentFile(ctx, documentUID)
}

// PutDocumentFile stores the file of a document in the Object Store, in every mode.
func (d *Documents) PutDocumentFile(ctx context.Context, documentUID string, fileData []byte) error {
	return d.kv.PutDocumentFile(ctx, documentUID, fileData)
}

// DeleteDocumentFile removes the file of a document from the Object Store, in every mode. A file that is not there is not
// an error.
func (d *Documents) DeleteDocumentFile(ctx context.Context, documentUID string) error {
	return d.kv.DeleteDocumentFile(ctx, documentUID)
}

// CreateDocumentMetadata creates the document's metadata.
func (d *Documents) CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error {
	if err := d.DocumentMetadataBackend.CreateDocumentMetadata(ctx, doc); err != nil {
		return err
	}
	if d.mode == ModeDual {
		committeeUID, uid := doc.CommitteeUID, doc.UID
		d.replicate(ctx, "create_document", uid, func(ctx context.Context) error {
			return copyCreated(ctx, func(ctx context.Context) (*model.CommitteeDocument, uint64, error) {
				return d.kv.GetDocumentMetadata(ctx, committeeUID, uid)
			}, d.pg.CreateDocumentMetadata, func(ctx context.Context, revision uint64) error {
				return d.deleteInPostgreSQL(ctx, committeeUID, uid, revision)
			}, func(ctx context.Context) error { return d.copyUpdate(ctx, committeeUID, uid) })
		})
	}
	return nil
}

// UpdateDocumentMetadata replaces the document's metadata, found by its uid, guarded by the revision.
func (d *Documents) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if err := d.DocumentMetadataBackend.UpdateDocumentMetadata(ctx, doc, revision); err != nil {
		return err
	}
	if d.mode == ModeDual {
		committeeUID, uid := doc.CommitteeUID, doc.UID
		d.replicate(ctx, "update_document", uid, func(ctx context.Context) error { return d.copyUpdate(ctx, committeeUID, uid) })
	}
	return nil
}

// copyUpdate writes the metadata KV holds now to PostgreSQL; see copyUpdated.
func (d *Documents) copyUpdate(ctx context.Context, committeeUID, uid string) error {
	return copyUpdated(ctx, func(ctx context.Context) (*model.CommitteeDocument, uint64, error) {
		return d.kv.GetDocumentMetadata(ctx, committeeUID, uid)
	},
		func(ctx context.Context) (uint64, error) { return d.postgreSQLRevision(ctx, committeeUID, uid) },
		d.pg.UpdateDocumentMetadata)
}

// postgreSQLRevision returns the revision PostgreSQL holds the document at. An update replaces the metadata by uid and may
// change the committee, so the row can still be under the committee it had before, which the committee-scoped read does
// not find; it is then found by uid among all the documents. That scan is made only when the scoped read finds nothing,
// which is a document that is missing, or one that moved to another committee.
func (d *Documents) postgreSQLRevision(ctx context.Context, committeeUID, uid string) (uint64, error) {
	_, current, err := d.pg.GetDocumentMetadata(ctx, committeeUID, uid)
	if !isNotFound(err) {
		return current, err
	}
	docs, listErr := d.pg.ListAllDocuments(ctx, "")
	if listErr != nil {
		return 0, listErr
	}
	for _, doc := range docs {
		if doc.UID == uid {
			_, current, getErr := d.pg.GetDocumentMetadata(ctx, doc.CommitteeUID, uid)
			return current, getErr
		}
	}
	return 0, err
}

// DeleteDocumentMetadata removes the document's metadata when the revision is still current, and its file with it.
func (d *Documents) DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error {
	if err := d.DocumentMetadataBackend.DeleteDocumentMetadata(ctx, committeeUID, documentUID, revision); err != nil {
		return err
	}
	switch d.mode {
	case ModeDual:
		d.replicate(ctx, "delete_document", documentUID, func(ctx context.Context) error {
			return d.deleteInPostgreSQL(ctx, committeeUID, documentUID, revision)
		})
	case ModePostgres:
		// The PostgreSQL store holds the metadata only, so the file is left for the composite to remove. The metadata is
		// gone, so a file that stays is unreachable: the failure is logged and counted, and does not fail the request. Like
		// a copy to the secondary store, it does not depend on the request staying open, since the delete has happened.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.writeTimeout)
		defer cancel()
		if err := d.kv.DeleteDocumentFile(fctx, documentUID); err != nil {
			d.metrics.SecondaryWriteFailed(ctx, d.mode, "object_store", "delete_document_file", documentUID, err)
		}
	}
	return nil
}

// deleteInPostgreSQL removes the metadata from PostgreSQL after KV decided that it is gone; see deleteConverged.
func (d *Documents) deleteInPostgreSQL(ctx context.Context, committeeUID, uid string, revision uint64) error {
	return deleteConverged(ctx,
		func(ctx context.Context, revision uint64) error {
			return d.pg.DeleteDocumentMetadata(ctx, committeeUID, uid, revision)
		},
		func(ctx context.Context) (uint64, error) {
			_, current, err := d.pg.GetDocumentMetadata(ctx, committeeUID, uid)
			return current, err
		}, revision)
}

// GetDocumentMetadata returns the document's metadata and its revision.
func (d *Documents) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	doc, revision, err := d.DocumentMetadataBackend.GetDocumentMetadata(ctx, committeeUID, documentUID)
	if d.sampled() {
		d.compareRecord(ctx, "get_document", doc, revision, err, func(ctx context.Context) (any, uint64, error) {
			return d.pg.GetDocumentMetadata(ctx, committeeUID, documentUID)
		})
	}
	return doc, revision, err
}

// DocumentNameExists reports whether the name is already used in the committee.
func (d *Documents) DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error) {
	exists, err := d.DocumentMetadataBackend.DocumentNameExists(ctx, committeeUID, name)
	if err == nil && d.sampled() {
		d.compareValue(ctx, "document_name_exists", func(ctx context.Context) (bool, error) {
			other, err := d.pg.DocumentNameExists(ctx, committeeUID, name)
			return other == exists, err
		})
	}
	return exists, err
}

// ListAllDocuments lists the documents of the committee, or of every committee when the uid is empty. Only the list of a
// committee is compared: the list of every committee is for the audit-sync tooling, and storage verify is the comparison
// of the whole set.
func (d *Documents) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	docs, err := d.DocumentMetadataBackend.ListAllDocuments(ctx, committeeUID)
	if committeeUID != "" && d.sampled() {
		compareRecords(ctx, d, "list_documents", docs, err, func(ctx context.Context) ([]*model.CommitteeDocument, error) {
			return d.pg.ListAllDocuments(ctx, committeeUID)
		})
	}
	return docs, err
}
