// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go/jetstream"
)

// ListAllLinkFolders lists committee link folders. When committeeUID is empty, all folders are returned.
func (s *storage) ListAllLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	if committeeUID != "" {
		return s.ListLinkFolders(ctx, committeeUID)
	}
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeFolders].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee folders bucket", errKeys)
	}
	folders := make([]*model.CommitteeLinkFolder, 0)
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		folder := &model.CommitteeLinkFolder{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeFolders, key, folder, false); err != nil {
			slog.WarnContext(ctx, "failed to get folder during list, skipping", "key", key, "error", err)
			continue
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

// ListAllLinks lists committee links. When committeeUID is empty, all links are returned.
func (s *storage) ListAllLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	if committeeUID != "" {
		return s.ListLinks(ctx, committeeUID)
	}
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeLinks].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee links bucket", errKeys)
	}
	links := make([]*model.CommitteeLink, 0)
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		link := &model.CommitteeLink{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeLinks, key, link, false); err != nil {
			slog.WarnContext(ctx, "failed to get link during list, skipping", "key", key, "error", err)
			continue
		}
		links = append(links, link)
	}
	return links, nil
}

// ListAllDocuments lists committee document metadata. When committeeUID is empty, all documents are returned.
func (s *storage) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeDocuments].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee documents bucket", errKeys)
	}
	docs := make([]*model.CommitteeDocument, 0)
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		doc := &model.CommitteeDocument{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeDocuments, key, doc, false); err != nil {
			slog.WarnContext(ctx, "failed to get document during list, skipping", "key", key, "error", err)
			continue
		}
		if committeeUID != "" && doc.CommitteeUID != committeeUID {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// UpdateLink persists link metadata with optimistic concurrency.
func (s *storage) UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error {
	if link == nil {
		return errs.NewValidation("link cannot be nil")
	}
	linkBytes, errMarshal := json.Marshal(link)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal link", errMarshal)
	}
	if _, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeLinks].Update(ctx, link.UID, linkBytes, revision); errUpdate != nil {
		if isJetStreamCASConflict(errUpdate) {
			return errs.NewConflict("link has been modified since it was last read")
		}
		return errs.NewUnexpected("failed to update link", errUpdate)
	}
	return nil
}

// UpdateLinkFolder persists folder metadata with optimistic concurrency.
func (s *storage) UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error {
	if folder == nil {
		return errs.NewValidation("folder cannot be nil")
	}
	folderBytes, errMarshal := json.Marshal(folder)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal folder", errMarshal)
	}
	if _, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeFolders].Update(ctx, folder.UID, folderBytes, revision); errUpdate != nil {
		if isJetStreamCASConflict(errUpdate) {
			return errs.NewConflict("folder has been modified since it was last read")
		}
		return errs.NewUnexpected("failed to update folder", errUpdate)
	}
	return nil
}

// UpdateDocumentMetadata persists document metadata with optimistic concurrency.
func (s *storage) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	docBytes, errMarshal := json.Marshal(doc)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal document", errMarshal)
	}
	if _, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeDocuments].Update(ctx, doc.UID, docBytes, revision); errUpdate != nil {
		if isJetStreamCASConflict(errUpdate) {
			return errs.NewConflict("document has been modified since it was last read")
		}
		return errs.NewUnexpected("failed to update document metadata", errUpdate)
	}
	return nil
}

// Compile-time check that storage implements DocumentAuditSyncStorage.
var _ port.DocumentAuditSyncStorage = (*storage)(nil)

// GetDocumentMetadata on storage delegates to the documents KV bucket for sync CLI use.
func (s *storage) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	if documentUID == "" {
		return nil, 0, errs.NewValidation("document UID cannot be empty")
	}
	doc := &model.CommitteeDocument{}
	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeDocuments, documentUID, doc, false)
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
