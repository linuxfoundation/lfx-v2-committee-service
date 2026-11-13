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

// CommitteeLinkDataWriter defines use case operations for writing links and folders.
type CommitteeLinkDataWriter interface {
	CreateLink(ctx context.Context, link *model.CommitteeLink, sync bool) (*model.CommitteeLink, error)
	// UpdateLink replaces the link's name, URL, description and folder, guarded by the revision. An absent folder moves
	// the link to the root. The link's identity and who created it are kept.
	UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64, sync bool) (*model.CommitteeLink, error)
	DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64, sync bool) error
	CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, sync bool) (*model.CommitteeLinkFolder, error)
	DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64, sync bool) error
}

type linkWriterOrchestrator struct {
	linkWriter         port.CommitteeLinkWriter
	linkReader         port.CommitteeLinkReader
	documentLister     port.CommitteeDocumentLister
	committeePublisher port.CommitteePublisher
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

// WithDocumentListerForWriter sets the lister DeleteLinkFolder uses to find the documents that are still in a folder.
func WithDocumentListerForWriter(l port.CommitteeDocumentLister) LinkWriterOption {
	return func(o *linkWriterOrchestrator) {
		o.documentLister = l
	}
}

func WithLinkPublisher(p port.CommitteePublisher) LinkWriterOption {
	return func(o *linkWriterOrchestrator) {
		o.committeePublisher = p
	}
}

func NewLinkWriterOrchestrator(opts ...LinkWriterOption) CommitteeLinkDataWriter {
	o := &linkWriterOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.linkWriter == nil {
		panic("link writer is required")
	}
	if o.linkReader == nil {
		panic("link reader is required for writer orchestrator")
	}
	if o.documentLister == nil {
		// Without it a folder that only holds documents would look empty and could be deleted.
		panic("document lister is required for writer orchestrator")
	}
	return o
}

func (o *linkWriterOrchestrator) CreateLink(ctx context.Context, link *model.CommitteeLink, sync bool) (*model.CommitteeLink, error) {
	if link == nil {
		return nil, errs.NewValidation("link is required")
	}
	if link.Name == "" {
		return nil, errs.NewValidation("link name is required")
	}
	if link.CommitteeUID == "" {
		return nil, errs.NewValidation("committee UID is required")
	}
	if link.URL == "" {
		return nil, errs.NewValidation("URL is required")
	}
	if link.FolderUID != nil && *link.FolderUID != "" {
		if _, _, err := o.linkReader.GetLinkFolder(ctx, link.CommitteeUID, *link.FolderUID); err != nil {
			return nil, err
		}
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

	o.publishLinkIndexerMessage(ctx, model.ActionCreated, link, sync)
	o.publishLinkCreatedEvent(ctx, link)

	return link, nil
}

func (o *linkWriterOrchestrator) UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64, sync bool) (*model.CommitteeLink, error) {
	if link == nil {
		return nil, errs.NewValidation("link is required")
	}
	if link.UID == "" {
		return nil, errs.NewValidation("link UID is required")
	}
	if link.CommitteeUID == "" {
		return nil, errs.NewValidation("committee UID is required")
	}
	if link.Name == "" {
		return nil, errs.NewValidation("link name is required")
	}
	if link.URL == "" {
		return nil, errs.NewValidation("URL is required")
	}

	// Found through the committee first: a link of another committee is not found, which is what lets the store
	// replace it by uid alone, and what is not editable (identity, creator, creation time) is taken from it.
	existing, _, err := o.linkReader.GetLink(ctx, link.CommitteeUID, link.UID)
	if err != nil {
		return nil, err
	}

	var folderUID *string
	if link.FolderUID != nil && *link.FolderUID != "" {
		if _, _, err := o.linkReader.GetLinkFolder(ctx, link.CommitteeUID, *link.FolderUID); err != nil {
			// The folder is named in the request, not in the path, so it being absent is a bad request, as it is when
			// a document is uploaded into one: a 404 on this route would read as the link itself being gone.
			var notFound errs.NotFound
			if errors.As(err, &notFound) {
				return nil, errs.NewValidation(fmt.Sprintf("folder %s does not exist in this committee", *link.FolderUID), err)
			}
			return nil, err
		}
		folderUID = link.FolderUID
	}

	updated := *existing
	updated.Name = link.Name
	updated.URL = link.URL
	updated.Description = link.Description
	updated.FolderUID = folderUID
	if link.UpdatedBy != nil {
		updated.UpdatedBy = link.UpdatedBy
	}
	updated.UpdatedAt = time.Now().UTC()

	if err := o.linkWriter.UpdateLink(ctx, &updated, revision); err != nil {
		return nil, err
	}

	slog.DebugContext(ctx, "updated committee link",
		"link_uid", updated.UID,
		"committee_uid", updated.CommitteeUID,
	)

	o.publishLinkIndexerMessage(ctx, model.ActionUpdated, &updated, sync)

	return &updated, nil
}

func (o *linkWriterOrchestrator) DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64, sync bool) error {
	link, _, err := o.linkReader.GetLink(ctx, committeeUID, linkUID)
	if err != nil {
		return err
	}
	if err := o.linkWriter.DeleteLink(ctx, committeeUID, linkUID, revision); err != nil {
		return err
	}

	o.publishLinkIndexerMessage(ctx, model.ActionDeleted, link, sync)

	return nil
}

func (o *linkWriterOrchestrator) CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, sync bool) (*model.CommitteeLinkFolder, error) {
	if folder == nil {
		return nil, errs.NewValidation("folder is required")
	}
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

	if err := o.linkWriter.CreateLinkFolder(ctx, folder); err != nil {
		return nil, err
	}

	slog.DebugContext(ctx, "created committee link folder",
		"folder_uid", folder.UID,
		"committee_uid", folder.CommitteeUID,
	)

	o.publishLinkFolderIndexerMessage(ctx, model.ActionCreated, folder, sync)

	return folder, nil
}

func (o *linkWriterOrchestrator) DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64, sync bool) error {
	folder, _, err := o.linkReader.GetLinkFolder(ctx, committeeUID, folderUID)
	if err != nil {
		return err
	}

	// Block deletion if folder contains links
	links, err := o.linkReader.ListLinks(ctx, committeeUID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.FolderUID != nil && *l.FolderUID == folderUID {
			return errs.NewValidation("folder cannot be deleted because it contains links; remove all links from the folder first")
		}
	}

	// Block deletion if folder contains documents. A document holds its folder in its own metadata, so the link list
	// above cannot see it, and deleting the folder would leave the document pointing at a folder that is gone.
	if err := o.assertFolderHasNoDocuments(ctx, committeeUID, folderUID); err != nil {
		return err
	}

	if err := o.linkWriter.DeleteLinkFolder(ctx, committeeUID, folderUID, revision); err != nil {
		return err
	}

	o.publishLinkFolderIndexerMessage(ctx, model.ActionDeleted, folder, sync)

	return nil
}

// assertFolderHasNoDocuments returns errs.Validation when a document of the committee is in the folder. It fails closed:
// when the documents cannot be listed completely and none of the ones that were listed is in the folder, the error is
// returned rather than the folder being treated as empty. A document that was listed is always enough to refuse, even
// when the list also failed.
func (o *linkWriterOrchestrator) assertFolderHasNoDocuments(ctx context.Context, committeeUID, folderUID string) error {
	docs, listErr := o.documentLister.ListAllDocuments(ctx, committeeUID)
	for _, d := range docs {
		if d != nil && d.CommitteeUID == committeeUID && d.FolderUID != nil && *d.FolderUID == folderUID {
			return errs.NewValidation("folder cannot be deleted because it contains documents; remove all documents from the folder first")
		}
	}
	if listErr != nil {
		slog.WarnContext(ctx, "could not list documents to check that the folder is empty, refusing to delete it",
			"committee_uid", committeeUID,
			"folder_uid", folderUID,
			"error", listErr,
		)
		return listErr
	}
	return nil
}

// publishLinkIndexerMessage publishes an indexer message for a committee link.
// Errors are logged and do not fail the operation.
func (o *linkWriterOrchestrator) publishLinkIndexerMessage(ctx context.Context, action model.MessageAction, link *model.CommitteeLink, sync bool) {
	if o.committeePublisher == nil {
		return
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
	}

	var data any
	switch action {
	case model.ActionCreated, model.ActionUpdated:
		indexerMessage.Tags = link.Tags()
		parentRefs := []string{fmt.Sprintf("committee:%s", link.CommitteeUID)}
		if link.FolderUID != nil && *link.FolderUID != "" {
			parentRefs = append(parentRefs, fmt.Sprintf("committee_link_folder:%s", *link.FolderUID))
		}
		indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
			ObjectID:             link.UID,
			AccessCheckObject:    fmt.Sprintf("committee:%s", link.CommitteeUID),
			AccessCheckRelation:  "viewer",
			HistoryCheckObject:   fmt.Sprintf("committee:%s", link.CommitteeUID),
			HistoryCheckRelation: "auditor",
			SortName:             link.Name,
			NameAndAliases:       []string{link.Name},
			ParentRefs:           parentRefs,
			Tags:                 link.Tags(),
			Fulltext:             fmt.Sprintf("%s %s %s", link.Name, link.Description, link.URL),
		}
		data = SanitizeCommitteeLinkForPublish(*link)
	case model.ActionDeleted:
		data = link.UID
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build link indexer message",
			"error", err,
			"action", action,
			"link_uid", link.UID,
		)
		return
	}

	if err := o.committeePublisher.Indexer(ctx, constants.IndexCommitteeLinkSubject, built, sync); err != nil {
		slog.WarnContext(ctx, "failed to publish link indexer message",
			"error", err,
			"action", action,
			"link_uid", link.UID,
		)
	}
}

// publishLinkFolderIndexerMessage publishes an indexer message for a committee link folder.
// Errors are logged and do not fail the operation.
func (o *linkWriterOrchestrator) publishLinkFolderIndexerMessage(ctx context.Context, action model.MessageAction, folder *model.CommitteeLinkFolder, sync bool) {
	if o.committeePublisher == nil {
		return
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
	}

	var data any
	switch action {
	case model.ActionCreated, model.ActionUpdated:
		indexerMessage.Tags = folder.Tags()
		indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
			ObjectID:             folder.UID,
			AccessCheckObject:    fmt.Sprintf("committee:%s", folder.CommitteeUID),
			AccessCheckRelation:  "viewer",
			HistoryCheckObject:   fmt.Sprintf("committee:%s", folder.CommitteeUID),
			HistoryCheckRelation: "auditor",
			SortName:             folder.Name,
			NameAndAliases:       []string{folder.Name},
			ParentRefs:           []string{fmt.Sprintf("committee:%s", folder.CommitteeUID)},
			Tags:                 folder.Tags(),
			Fulltext:             folder.Name,
		}
		data = SanitizeCommitteeLinkFolderForPublish(*folder)
	case model.ActionDeleted:
		data = folder.UID
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build link folder indexer message",
			"error", err,
			"action", action,
			"folder_uid", folder.UID,
		)
		return
	}

	if err := o.committeePublisher.Indexer(ctx, constants.IndexCommitteeLinkFolderSubject, built, sync); err != nil {
		slog.WarnContext(ctx, "failed to publish link folder indexer message",
			"error", err,
			"action", action,
			"folder_uid", folder.UID,
		)
	}
}

// publishLinkCreatedEvent publishes a domain event for a newly added link so that
// downstream subscribers (e.g. the notification handler) can react to it.
// Errors are logged and do not fail the create operation.
func (o *linkWriterOrchestrator) publishLinkCreatedEvent(ctx context.Context, link *model.CommitteeLink) {
	if o.committeePublisher == nil {
		return
	}

	event := model.CommitteeEvent{}
	built, err := event.Build(ctx, model.ResourceCommitteeLink, model.ActionCreated, link)
	if err != nil {
		slog.WarnContext(ctx, "failed to build link created event",
			"error", err,
			"link_uid", link.UID,
		)
		return
	}

	if err := o.committeePublisher.Event(ctx, constants.CommitteeLinkCreatedSubject, built, false); err != nil {
		slog.WarnContext(ctx, "failed to publish link created event",
			"error", err,
			"link_uid", link.UID,
		)
	}
}
