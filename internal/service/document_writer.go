// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
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

	// Fast-fail on a taken name before uploading the file. This check is not authoritative (a
	// concurrent upload can still win the name); CreateDocumentMetadata below is.
	exists, err := o.docReader.DocumentNameExists(ctx, doc.CommitteeUID, doc.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errs.NewConflict("document with the same name already exists for this committee")
	}

	if err := o.docWriter.PutDocumentFile(ctx, doc.UID, fileData); err != nil {
		return nil, err
	}

	if err := o.docWriter.CreateDocumentMetadata(ctx, doc); err != nil {
		o.removeUnreferencedFile(ctx, doc, err)
		return nil, err
	}

	slog.DebugContext(ctx, "uploaded committee document",
		"document_uid", doc.UID,
		"committee_uid", doc.CommitteeUID,
		"file_name", doc.FileName,
		"file_size", doc.FileSize,
	)

	o.publishDocumentIndexerMessage(ctx, model.ActionCreated, doc, sync)
	o.publishDocumentCreatedEvent(ctx, doc)

	return doc, nil
}

// removeUnreferencedFile deletes the file uploaded for doc after CreateDocumentMetadata failed, so
// it does not accumulate in the object store. A non-conflict write error can be ambiguous (the
// metadata may have been stored before the error was reported), so unless the failure was a name
// conflict the file is only removed once reading the metadata returns NotFound. Any other result,
// including a failed read, keeps the file: deleting it under a live record would break downloads.
func (o *documentWriterOrchestrator) removeUnreferencedFile(ctx context.Context, doc *model.CommitteeDocument, createErr error) {
	var conflict errs.Conflict
	if !errors.As(createErr, &conflict) {
		_, _, errGet := o.docReader.GetDocumentMetadata(ctx, doc.CommitteeUID, doc.UID)
		var notFound errs.NotFound
		if !errors.As(errGet, &notFound) {
			slog.WarnContext(ctx, "document metadata not confirmed absent after create error; keeping uploaded file",
				"document_uid", doc.UID,
				"committee_uid", doc.CommitteeUID,
				"create_error", createErr,
				"lookup_error", errGet,
			)
			return
		}
	}

	if errCleanup := o.docWriter.DeleteDocumentFile(ctx, doc.UID); errCleanup != nil {
		slog.WarnContext(ctx, "failed to remove uploaded document file after metadata creation failed",
			"document_uid", doc.UID,
			"error", errCleanup,
		)
	}
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
		data = SanitizeCommitteeDocumentForPublish(*doc)
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

// publishDocumentCreatedEvent publishes a domain event for a newly uploaded document so that
// downstream subscribers (e.g. the notification handler) can react to it.
// Errors are logged and do not fail the upload operation.
func (o *documentWriterOrchestrator) publishDocumentCreatedEvent(ctx context.Context, doc *model.CommitteeDocument) {
	if o.committeePublisher == nil {
		return
	}

	event := model.CommitteeEvent{}
	built, err := event.Build(ctx, model.ResourceCommitteeDocument, model.ActionCreated, doc)
	if err != nil {
		slog.WarnContext(ctx, "failed to build document created event",
			"error", err,
			"document_uid", doc.UID,
		)
		return
	}

	if err := o.committeePublisher.Event(ctx, constants.CommitteeDocumentCreatedSubject, built, false); err != nil {
		slog.WarnContext(ctx, "failed to publish document created event",
			"error", err,
			"document_uid", doc.UID,
		)
	}
}
