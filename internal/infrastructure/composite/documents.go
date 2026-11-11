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
	// DocumentRevision returns the revision of the document found by its uid alone, as the copy of an update that moved
	// the document to another committee needs.
	DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
	// DocumentCommittee returns the committee the document is in, found by its uid alone, as the delete of a document the
	// store holds under another committee than the primary store does needs.
	DocumentCommittee(ctx context.Context, documentUID string) (string, error)
}

// DocumentKVBackend is the NATS side of the documents: the metadata in KV and the files in the Object Store.
type DocumentKVBackend interface {
	DocumentMetadataBackend
	port.CommitteeDocumentReaderWriter
}

// Documents is the composite of the committee documents, built like Store: the primary store decides every result of the
// metadata and is embedded, so every method not overridden goes to it unchanged. In dual mode each write of the metadata
// is copied to PostgreSQL from what KV holds after it, with the revision KV gave it, and a sampled fraction of reads is
// compared; see Store for what that guarantees. In dual-pg mode PostgreSQL decides and KV follows, and the composite
// removes the file itself whenever PostgreSQL decided a delete, whether or not the copy of it to KV removed it too.
//
// The files are not copied and are not compared: PostgreSQL holds metadata only, and the file of a document is in the NATS
// Object Store in every mode, so the KV store is needed in postgres mode too, for the file reads, the file writes and
// the deletion of a file. Nothing in KV is written for the metadata in postgres mode.
//
// A file and its metadata are two writes the caller makes one after the other, and the composite keeps that order: the
// file is put first, then the metadata is created, and the caller removes the file when the create fails. Deleting the
// metadata removes the file with it, whichever store decided: the NATS adapter does it itself, and in postgres mode the
// composite does after the metadata is gone, best effort, counted and logged, since the metadata is already gone and the
// file can no longer be reached through the API.
type Documents struct {
	DocumentMetadataBackend
	core
	kv DocumentKVBackend
	pg DocumentMetadataBackend
	// src is the primary store the copy of the metadata reads and dst the secondary store it writes; see Store. The files
	// are in the KV store whatever the mode.
	src, dst DocumentMetadataBackend
}

var (
	_ port.CommitteeDocumentReaderWriter = (*Documents)(nil)
	// The link writer lists the documents to refuse deleting a folder that still holds one, and the service gets this store.
	_ port.CommitteeDocumentLister = (*Documents)(nil)
)

// NewDocuments returns the composite of the documents for the mode; see New. kv is needed in every mode, for the files.
func NewDocuments(mode Mode, kv DocumentKVBackend, pg DocumentMetadataBackend, opts ...Option) (*Documents, error) {
	if kv == nil {
		return nil, errors.New("the documents need the KV store in every storage mode, for the files")
	}
	primary, err := selectPrimary(mode, DocumentMetadataBackend(kv), pg)
	if err != nil {
		return nil, err
	}
	d := &Documents{DocumentMetadataBackend: primary, core: newCore(mode, opts), kv: kv, pg: pg, src: kv, dst: pg}
	if mode == ModeDualPG {
		d.src, d.dst = pg, kv
	}
	return d, nil
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
	if d.mode.HasSecondary() {
		committeeUID, uid := doc.CommitteeUID, doc.UID
		d.replicate(ctx, "create_document", uid, func(ctx context.Context) error { return d.copyCreate(ctx, committeeUID, uid) })
	}
	return nil
}

// UpdateDocumentMetadata replaces the document's metadata, found by its uid, guarded by the revision.
func (d *Documents) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if err := d.DocumentMetadataBackend.UpdateDocumentMetadata(ctx, doc, revision); err != nil {
		return err
	}
	if d.mode.HasSecondary() {
		committeeUID, uid := doc.CommitteeUID, doc.UID
		d.replicate(ctx, "update_document", uid, func(ctx context.Context) error { return d.copyUpdate(ctx, committeeUID, uid) })
	}
	return nil
}

// copyCreate copies the metadata the primary store holds to the secondary store; see copyCreated.
func (d *Documents) copyCreate(ctx context.Context, committeeUID, uid string) error {
	return copyCreated(ctx, &d.core, func(ctx context.Context) (*model.CommitteeDocument, uint64, error) {
		return d.src.GetDocumentMetadata(ctx, committeeUID, uid)
	}, d.dst.CreateDocumentMetadata, func(ctx context.Context, revision uint64) error {
		return d.deleteUnlessMoved(ctx, committeeUID, uid, revision)
	}, func(ctx context.Context) error { return d.copyUpdate(ctx, committeeUID, uid) })
}

// copyUpdate writes the metadata the primary store holds now to the secondary store; see copyUpdated.
func (d *Documents) copyUpdate(ctx context.Context, committeeUID, uid string) error {
	return copyUpdated(ctx, &d.core, func(ctx context.Context) (*model.CommitteeDocument, uint64, error) {
		return d.src.GetDocumentMetadata(ctx, committeeUID, uid)
	},
		func(ctx context.Context) (*model.CommitteeDocument, uint64, error) {
			return d.dstDocument(ctx, committeeUID, uid)
		},
		d.dst.UpdateDocumentMetadata, func(ctx context.Context) error { return d.copyCreate(ctx, committeeUID, uid) })
}

// dstDocument reads the document from the secondary store. An update replaces the metadata by uid and may change the
// committee, so the row can still be under the committee it had before, which the committee-scoped read does not find: it
// is then looked up by uid alone, and what it is returned as differs in content from the primary store's, which has moved.
func (d *Documents) dstDocument(ctx context.Context, committeeUID, uid string) (*model.CommitteeDocument, uint64, error) {
	doc, revision, err := d.dst.GetDocumentMetadata(ctx, committeeUID, uid)
	if !isNotFound(err) {
		return doc, revision, err
	}
	revision, revisionErr := d.dst.DocumentRevision(ctx, uid)
	if revisionErr != nil {
		return nil, 0, revisionErr
	}
	return &model.CommitteeDocument{}, revision, nil
}

// DeleteDocumentMetadata removes the document's metadata when the revision is still current, and its file with it.
func (d *Documents) DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error {
	if err := d.DocumentMetadataBackend.DeleteDocumentMetadata(ctx, committeeUID, documentUID, revision); err != nil {
		return err
	}
	if d.mode.HasSecondary() {
		d.replicate(ctx, "delete_document", documentUID, func(ctx context.Context) error {
			return d.deleteInSecondary(ctx, committeeUID, documentUID, revision)
		})
	}
	if d.mode.PostgresIsPrimary() {
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

// deleteUnlessMoved is the delete of the copy of a create that finds the document gone from the primary store under the
// committee it was created in. The committee-scoped read does not tell a delete from a move to another committee, and a
// delete of the secondary store's document removes the file with it, so the primary store is asked by uid: a document it
// still has was moved, and what it is now is copied, which moves the secondary store's document too; the file stays.
func (d *Documents) deleteUnlessMoved(ctx context.Context, committeeUID, uid string, revision uint64) error {
	current, err := d.src.DocumentCommittee(ctx, uid)
	if err == nil {
		return d.copyUpdate(ctx, current, uid)
	}
	if !isNotFound(err) {
		return err
	}
	return d.deleteInSecondary(ctx, committeeUID, uid, revision)
}

// deleteInSecondary removes the metadata from the secondary store after the primary store decided that it is gone; see
// deleteConverged. The secondary store may still hold the document under the committee it had before a move whose copy
// failed, so it is deleted under the committee the secondary store has it in.
func (d *Documents) deleteInSecondary(ctx context.Context, committeeUID, uid string, revision uint64) error {
	committeeUID, err := d.dstCommittee(ctx, committeeUID, uid)
	if isNotFound(err) {
		return nil // not there: where a delete leaves it
	}
	if err != nil {
		return err
	}
	return deleteConverged(ctx, &d.core,
		func(ctx context.Context, revision uint64) error {
			return d.dst.DeleteDocumentMetadata(ctx, committeeUID, uid, revision)
		},
		func(ctx context.Context) (uint64, error) {
			_, current, err := d.dst.GetDocumentMetadata(ctx, committeeUID, uid)
			return current, err
		}, revision)
}

// dstCommittee returns the committee the secondary store holds the document in: the one given, or, when the document is
// not there but is under another committee, that one, found by uid. It is NotFound when the secondary store does not have it.
func (d *Documents) dstCommittee(ctx context.Context, committeeUID, uid string) (string, error) {
	_, _, err := d.dst.GetDocumentMetadata(ctx, committeeUID, uid)
	if !isNotFound(err) {
		return committeeUID, err
	}
	return d.dst.DocumentCommittee(ctx, uid)
}

// GetDocumentMetadata returns the document's metadata and its revision.
func (d *Documents) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	doc, revision, err := d.DocumentMetadataBackend.GetDocumentMetadata(ctx, committeeUID, documentUID)
	if d.sampled() {
		d.compareRecord(ctx, "get_document", doc, revision, err, func(ctx context.Context) (any, uint64, error) {
			return d.dst.GetDocumentMetadata(ctx, committeeUID, documentUID)
		})
	}
	return doc, revision, err
}

// DocumentNameExists reports whether the name is already used in the committee.
func (d *Documents) DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error) {
	exists, err := d.DocumentMetadataBackend.DocumentNameExists(ctx, committeeUID, name)
	if err == nil && d.sampled() {
		d.compareValue(ctx, "document_name_exists", func(ctx context.Context) (bool, error) {
			other, err := d.dst.DocumentNameExists(ctx, committeeUID, name)
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
			return d.dst.ListAllDocuments(ctx, committeeUID)
		})
	}
	return docs, err
}
