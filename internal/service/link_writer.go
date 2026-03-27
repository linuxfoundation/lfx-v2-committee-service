// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// CommitteeLinkDataWriter defines use case operations for writing links and folders.
type CommitteeLinkDataWriter interface {
	CreateLink(ctx context.Context, link *model.CommitteeLink) (*model.CommitteeLink, error)
	DeleteLink(ctx context.Context, committeeUID, linkUID string) error
	CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) (*model.CommitteeLinkFolder, error)
	DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string) error
}

type linkWriterOrchestrator struct {
	linkWriter port.CommitteeLinkWriter
	linkReader port.CommitteeLinkReader
}

type LinkWriterOption func(*linkWriterOrchestrator)

func WithLinkWriter(w port.CommitteeLinkWriter) LinkWriterOption {
	return func(o *linkWriterOrchestrator) {
		o.linkWriter = w
	}
}

func WithLinkReaderForWriter(r port.CommitteeLinkReader) LinkWriterOption {
	return func(o *linkWriterOrchestrator) {
		o.linkReader = r
	}
}

func NewLinkWriterOrchestrator(opts ...LinkWriterOption) CommitteeLinkDataWriter {
	o := &linkWriterOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *linkWriterOrchestrator) CreateLink(ctx context.Context, link *model.CommitteeLink) (*model.CommitteeLink, error) {
	if link.Name == "" {
		return nil, errs.NewValidation("link name is required")
	}
	if link.CommitteeUID == "" {
		return nil, errs.NewValidation("committee UID is required")
	}
	if link.URL == "" {
		return nil, errs.NewValidation("URL is required")
	}

	link.UID = uuid.New().String()
	now := time.Now().UTC()
	link.CreatedAt = now
	link.UpdatedAt = now

	if err := o.linkWriter.CreateLink(ctx, link); err != nil {
		return nil, err
	}

	slog.DebugContext(ctx, "created committee link",
		"link_uid", link.UID,
		"committee_uid", link.CommitteeUID,
	)

	return link, nil
}

func (o *linkWriterOrchestrator) DeleteLink(ctx context.Context, committeeUID, linkUID string) error {
	_, rev, err := o.linkReader.GetLink(ctx, committeeUID, linkUID)
	if err != nil {
		return err
	}
	return o.linkWriter.DeleteLink(ctx, committeeUID, linkUID, rev)
}

func (o *linkWriterOrchestrator) CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) (*model.CommitteeLinkFolder, error) {
	if folder.Name == "" {
		return nil, errs.NewValidation("folder name is required")
	}
	if folder.CommitteeUID == "" {
		return nil, errs.NewValidation("committee UID is required")
	}

	folder.UID = uuid.New().String()
	now := time.Now().UTC()
	folder.CreatedAt = now
	folder.UpdatedAt = now

	if _, err := o.linkWriter.UniqueLinkFolderName(ctx, folder); err != nil {
		return nil, err
	}

	if err := o.linkWriter.CreateLinkFolder(ctx, folder); err != nil {
		return nil, err
	}

	slog.DebugContext(ctx, "created committee link folder",
		"folder_uid", folder.UID,
		"committee_uid", folder.CommitteeUID,
	)

	return folder, nil
}

func (o *linkWriterOrchestrator) DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string) error {
	_, rev, err := o.linkReader.GetLinkFolder(ctx, committeeUID, folderUID)
	if err != nil {
		return err
	}
	return o.linkWriter.DeleteLinkFolder(ctx, committeeUID, folderUID, rev)
}
