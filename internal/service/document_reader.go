// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// CommitteeDocumentDataReader defines use case operations for reading documents.
type CommitteeDocumentDataReader interface {
	GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
	GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error)
}

type documentReaderOrchestrator struct {
	docReader port.CommitteeDocumentReader
}

type DocumentReaderOption func(*documentReaderOrchestrator)

func WithDocumentReader(r port.CommitteeDocumentReader) DocumentReaderOption {
	return func(o *documentReaderOrchestrator) {
		o.docReader = r
	}
}

func NewDocumentReaderOrchestrator(opts ...DocumentReaderOption) CommitteeDocumentDataReader {
	o := &documentReaderOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.docReader == nil {
		panic("document reader is required")
	}
	return o
}

func (o *documentReaderOrchestrator) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	slog.DebugContext(ctx, "executing get document metadata use case",
		"committee_uid", committeeUID,
		"document_uid", documentUID,
	)

	doc, revision, err := o.docReader.GetDocumentMetadata(ctx, committeeUID, documentUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get document metadata",
			"error", err,
			"committee_uid", committeeUID,
			"document_uid", documentUID,
		)
		return nil, 0, err
	}

	slog.DebugContext(ctx, "document metadata retrieved successfully",
		"committee_uid", committeeUID,
		"document_uid", documentUID,
		"revision", revision,
	)

	return doc, revision, nil
}

func (o *documentReaderOrchestrator) GetDocumentFile(ctx context.Context, documentUID string) ([]byte, error) {
	slog.DebugContext(ctx, "executing get document file use case",
		"document_uid", documentUID,
	)

	data, err := o.docReader.GetDocumentFile(ctx, documentUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get document file",
			"error", err,
			"document_uid", documentUID,
		)
		return nil, err
	}

	slog.DebugContext(ctx, "document file retrieved successfully",
		"document_uid", documentUID,
		"file_size", len(data),
	)

	return data, nil
}
