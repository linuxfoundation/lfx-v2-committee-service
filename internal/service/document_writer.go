// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// CommitteeDocumentDataWriter defines use case operations for writing documents.
type CommitteeDocumentDataWriter interface {
	UploadDocument(ctx context.Context, doc *model.CommitteeDocument, fileData []byte, sync bool) (*model.CommitteeDocument, error)
	DeleteDocument(ctx context.Context, committeeUID, documentUID string, revision uint64, sync bool) error
}

type documentWriterOrchestrator struct {
	docWriter          port.CommitteeDocumentWriter
	docReader          port.CommitteeDocumentReader
	committeePublisher port.CommitteePublisher
}

type DocumentWriterOption func(*documentWriterOrchestrator)

func WithDocumentWriter(w port.CommitteeDocumentWriter) DocumentWriterOption {
	return func(o *documentWriterOrchestrator) {
		o.docWriter = w
	}
}

func WithDocumentReaderForWriter(r port.CommitteeDocumentReader) DocumentWriterOption {
	return func(o *documentWriterOrchestrator) {
		o.docReader = r
	}
}

func WithDocumentPublisher(p port.CommitteePublisher) DocumentWriterOption {
	return func(o *documentWriterOrchestrator) {
		o.committeePublisher = p
	}
}

func NewDocumentWriterOrchestrator(opts ...DocumentWriterOption) CommitteeDocumentDataWriter {
	o := &documentWriterOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.docWriter == nil {
		panic("document writer is required")
	}
	if o.docReader == nil {
		panic("document reader is required for writer orchestrator")
	}
	return o
}

func (o *documentWriterOrchestrator) UploadDocument(ctx context.Context, doc *model.CommitteeDocument, fileData []byte, sync bool) (*model.CommitteeDocument, error) {
	if doc == nil {
		return nil, errs.NewValidation("document is required")
	}
	if doc.Name == "" {
		return nil, errs.NewValidation("document name is required")
	}
	if doc.CommitteeUID == "" {
		return nil, errs.NewValidation("committee UID is required")
	}
	if doc.FileName == "" {
		return nil, errs.NewValidation("file name is required")
	}
	if len(fileData) == 0 {
		return nil, errs.NewValidation("file data is required")
	}
	if doc.ContentType == "" {
		return nil, errs.NewValidation("content type is required")
	}
	if int64(len(fileData)) > model.MaxDocumentFileSize {
		return nil, errs.NewValidation(fmt.Sprintf("file size exceeds maximum allowed size of %d bytes", model.MaxDocumentFileSize))
	}
	if !model.AllowedDocumentContentTypes[doc.ContentType] {
		return nil, errs.NewValidation(fmt.Sprintf("content type %q is not allowed", doc.ContentType))
	}

	doc.UID = uuid.New().String()
	doc.FileSize = int64(len(fileData))
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now

	uniqueKey, err := o.docWriter.UniqueDocumentName(ctx, doc)
	if err != nil {
		return nil, err
	}

	// Store file first; if this fails, roll back the name reservation.
	if err := o.docWriter.PutDocumentFile(ctx, doc.UID, fileData); err != nil {
		if errCleanup := o.docWriter.DeleteUniqueDocumentName(ctx, uniqueKey); errCleanup != nil {
			slog.WarnContext(ctx, "failed to rollback document name reservation",
				"unique_key", uniqueKey,
				"error", errCleanup,
			)
		}
		return nil, err
	}

	if err := o.docWriter.CreateDocumentMetadata(ctx, doc); err != nil {
		// NOTE: The file written by PutDocumentFile above is not rolled back here.
		// This is an accepted trade-off: metadata creation failures are rare, orphaned
		// files are bounded by the 10 GB object store limit, and normal deletes clean
		// up files. A DeleteDocumentFile port method could be added if orphan volume
		// becomes a concern.
		// Roll back the uniqueness reservation so the name can be reused
		if errCleanup := o.docWriter.DeleteUniqueDocumentName(ctx, uniqueKey); errCleanup != nil {
			slog.WarnContext(ctx, "failed to rollback document name reservation",
				"unique_key", uniqueKey,
				"error", errCleanup,
			)
		}
		return nil, err
	}

	slog.DebugContext(ctx, "uploaded committee document",
		"document_uid", doc.UID,
		"committee_uid", doc.CommitteeUID,
		"file_name", doc.FileName,
		"file_size", doc.FileSize,
	)

	o.publishDocumentIndexerMessage(ctx, model.ActionCreated, doc, sync)

	return doc, nil
}

func (o *documentWriterOrchestrator) DeleteDocument(ctx context.Context, committeeUID, documentUID string, revision uint64, sync bool) error {
	doc, _, err := o.docReader.GetDocumentMetadata(ctx, committeeUID, documentUID)
	if err != nil {
		return err
	}

	if err := o.docWriter.DeleteDocumentMetadata(ctx, committeeUID, documentUID, revision); err != nil {
		return err
	}

	// File deletion is handled fire-and-forget inside DeleteDocumentMetadata in the NATS adapter.
	// FGA tuples and index entries are cleaned up via fire-and-forget messages below.

	o.publishDocumentIndexerMessage(ctx, model.ActionDeleted, doc, sync)

	return nil
}

// publishDocumentIndexerMessage publishes an indexer message for a committee document.
// Errors are logged and do not fail the operation.
func (o *documentWriterOrchestrator) publishDocumentIndexerMessage(ctx context.Context, action model.MessageAction, doc *model.CommitteeDocument, sync bool) {
	if o.committeePublisher == nil {
		return
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
	}

	var data any
	switch action {
	case model.ActionCreated, model.ActionUpdated:
		indexerMessage.Tags = doc.Tags()
		indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
			ObjectID:             doc.UID,
			AccessCheckObject:    fmt.Sprintf("committee:%s", doc.CommitteeUID),
			AccessCheckRelation:  "viewer",
			HistoryCheckObject:   fmt.Sprintf("committee:%s", doc.CommitteeUID),
			HistoryCheckRelation: "auditor",
			SortName:             doc.Name,
			NameAndAliases:       []string{doc.Name},
			ParentRefs:           []string{fmt.Sprintf("committee:%s", doc.CommitteeUID)},
			Tags:                 doc.Tags(),
			Fulltext:             fmt.Sprintf("%s %s %s", doc.Name, doc.Description, doc.FileName),
		}
		data = doc
	case model.ActionDeleted:
		data = doc.UID
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build document indexer message",
			"error", err,
			"action", action,
			"document_uid", doc.UID,
		)
		return
	}

	if err := o.committeePublisher.Indexer(ctx, constants.IndexCommitteeDocumentSubject, built, sync); err != nil {
		slog.WarnContext(ctx, "failed to publish document indexer message",
			"error", err,
			"action", action,
			"document_uid", doc.UID,
		)
	}
}
