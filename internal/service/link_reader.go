// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// CommitteeLinkDataReader defines use case operations for reading links and folders.
type CommitteeLinkDataReader interface {
	GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error)
	ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error)
	GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error)
	ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error)
}

type linkReaderOrchestrator struct {
	linkReader port.CommitteeLinkReader
}

type LinkReaderOption func(*linkReaderOrchestrator)

func WithLinkReader(r port.CommitteeLinkReader) LinkReaderOption {
	return func(o *linkReaderOrchestrator) {
		o.linkReader = r
	}
}

func NewLinkReaderOrchestrator(opts ...LinkReaderOption) CommitteeLinkDataReader {
	o := &linkReaderOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.linkReader == nil {
		panic("link reader is required")
	}
	return o
}

func (o *linkReaderOrchestrator) GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error) {
	return o.linkReader.GetLink(ctx, committeeUID, linkUID)
}

func (o *linkReaderOrchestrator) ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	return o.linkReader.ListLinks(ctx, committeeUID)
}

func (o *linkReaderOrchestrator) GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error) {
	return o.linkReader.GetLinkFolder(ctx, committeeUID, folderUID)
}

func (o *linkReaderOrchestrator) ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	return o.linkReader.ListLinkFolders(ctx, committeeUID)
}
