// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

var _ port.DocumentAuditSyncStorage = (*Store)(nil)

// The store holds document metadata only. The file bytes stay in the NATS Object Store, keyed by
// the document uid, so the file methods of port.CommitteeDocumentReaderWriter (GetDocumentFile,
// PutDocumentFile, DeleteDocumentFile) are not implemented here: the composite store keeps calling
// the object store for them, and deletes the file after the metadata.

var documentColumns = []string{
	"uid", "committee_uid", "folder_uid", "name", "description", "file_name", "file_size", "content_type",
	"created_by", "updated_by", "created_at", "updated_at",
}

var (
	selectDocument = "SELECT " + columnList(documentColumns) + ", revision FROM committee_documents"
	insertDocument = "INSERT INTO committee_documents (" + columnList(documentColumns) + ") VALUES (" + placeholders(len(documentColumns)) + ")"
	updateDocument = "UPDATE committee_documents SET " + assignments(documentColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"
)

const (
	documentExists   = "SELECT EXISTS (SELECT 1 FROM committee_documents WHERE uid = $1)"
	documentExistsIn = "SELECT EXISTS (SELECT 1 FROM committee_documents WHERE uid = $1 AND committee_uid = $2)"
	deleteDocument   = "DELETE FROM committee_documents WHERE uid = $1 AND committee_uid = $2 AND revision = $3"
	documentNameUsed = "SELECT EXISTS (SELECT 1 FROM committee_documents WHERE committee_uid = $1 AND name = $2)"
)

func documentValues(d *model.CommitteeDocument) ([]any, error) {
	createdBy, err := jsonObject(d.CreatedBy)
	if err != nil {
		return nil, err
	}
	updatedBy, err := jsonObject(d.UpdatedBy)
	if err != nil {
		return nil, err
	}
	return []any{
		d.UID, d.CommitteeUID, d.FolderUID, d.Name, d.Description, d.FileName, d.FileSize, d.ContentType,
		createdBy, updatedBy, d.CreatedAt, d.UpdatedAt,
	}, nil
}

func scanDocument(row pgx.Row) (*model.CommitteeDocument, uint64, error) {
	d := &model.CommitteeDocument{}
	var createdBy, updatedBy []byte
	var revision int64
	if err := row.Scan(
		&d.UID, &d.CommitteeUID, &d.FolderUID, &d.Name, &d.Description, &d.FileName, &d.FileSize, &d.ContentType,
		&createdBy, &updatedBy, &d.CreatedAt, &d.UpdatedAt, &revision,
	); err != nil {
		return nil, 0, err
	}
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	var err error
	if d.CreatedBy, err = decodeObject[model.CommitteeUser](createdBy); err != nil {
		return nil, 0, fmt.Errorf("decode created_by: %w", err)
	}
	if d.UpdatedBy, err = decodeObject[model.CommitteeUser](updatedBy); err != nil {
		return nil, 0, fmt.Errorf("decode updated_by: %w", err)
	}
	return d, uint64(revision), nil
}

func collectDocument(row pgx.CollectableRow) (*model.CommitteeDocument, error) {
	d, _, err := scanDocument(row)
	return d, err
}

// documentConflict translates a unique violation on committee_documents into the conflict the KV
// store reports for its name lookup key. It returns nil when err is not one.
func documentConflict(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok {
		return nil
	}
	if constraint == "committee_documents_committee_name_key" {
		return errs.NewConflict("document with the same name already exists for this committee")
	}
	return errs.NewConflict("document already exists")
}

// CreateDocumentMetadata persists the metadata of a new document. The (committee, name) unique
// index decides a duplicate; the name is case-sensitive.
func (s *Store) CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	if doc.UID == "" || doc.CommitteeUID == "" {
		return errs.NewValidation("document UID and CommitteeUID must be non-empty")
	}
	values, err := documentValues(doc)
	if err != nil {
		return unexpected("failed to encode document", err)
	}
	if _, err := s.pool.Exec(ctx, insertDocument, values...); err != nil {
		if conflict := documentConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to create document metadata", err)
	}
	return nil
}

// UpdateDocumentMetadata replaces the metadata when the revision is still current. Unlike KV, which
// keeps the lookup key of the name the document was created with, a changed name is checked against
// the unique index.
func (s *Store) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	values, err := documentValues(doc)
	if err != nil {
		return unexpected("failed to encode document", err)
	}
	args := append([]any{values[0], revisionArg(revision)}, values[1:]...)
	tag, err := s.pool.Exec(ctx, updateDocument, args...)
	if err != nil {
		if conflict := documentConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to update document metadata", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, documentExists, "document UID", doc.UID, "document not found", "document has been modified since it was last read")
	}
	return nil
}

// GetDocumentMetadata returns the metadata and its revision. A document of another committee is not found.
func (s *Store) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	if documentUID == "" {
		return nil, 0, errs.NewValidation("document UID cannot be empty")
	}
	doc, revision, err := scanDocument(s.pool.QueryRow(ctx, selectDocument+" WHERE uid = $1 AND committee_uid = $2", documentUID, committeeUID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", documentUID))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get document metadata", err)
	}
	return doc, revision, nil
}

// DocumentNameExists reports whether the committee already has a document with the name.
func (s *Store) DocumentNameExists(ctx context.Context, committeeUID, name string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, documentNameUsed, committeeUID, name).Scan(&exists); err != nil {
		return false, unexpected("failed to check document name", err)
	}
	return exists, nil
}

// ListAllDocuments returns the committee's documents, or every document when committeeUID is empty.
func (s *Store) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	query, args := selectDocument, []any{}
	if committeeUID != "" {
		query, args = query+" WHERE committee_uid = $1", append(args, committeeUID)
	}
	rows, err := s.pool.Query(ctx, query+" ORDER BY uid", args...)
	if err != nil {
		return nil, unexpected("failed to list documents", err)
	}
	docs, err := pgx.CollectRows(rows, collectDocument)
	if err != nil {
		return nil, unexpected("failed to list documents", err)
	}
	return docs, nil
}

// DeleteDocumentMetadata removes the metadata when the revision is still current. The file in the
// object store is not touched.
func (s *Store) DeleteDocumentMetadata(ctx context.Context, committeeUID, documentUID string, revision uint64) error {
	if documentUID == "" {
		return errs.NewValidation("document UID cannot be empty")
	}
	tag, err := s.pool.Exec(ctx, deleteDocument, documentUID, committeeUID, revisionArg(revision))
	if err != nil {
		return unexpected("failed to delete document metadata", err)
	}
	if tag.RowsAffected() == 0 {
		return s.classifyRejected(ctx, documentExistsIn, []any{documentUID, committeeUID}, "document UID", documentUID, "document not found", "document has been modified since it was last read")
	}
	return nil
}
