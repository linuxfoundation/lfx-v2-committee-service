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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"

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
	var readFailures int
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		folder := &model.CommitteeLinkFolder{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeFolders, key, folder, false); err != nil {
			readFailures++
			slog.WarnContext(ctx, "failed to get folder during list, skipping", "key", key, "error", err)
			continue
		}
		folders = append(folders, folder)
	}
	if readFailures > 0 {
		return folders, errs.NewUnexpected(fmt.Sprintf("%d folder(s) failed to read during list", readFailures))
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
	var readFailures int
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		link := &model.CommitteeLink{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeLinks, key, link, false); err != nil {
			readFailures++
			slog.WarnContext(ctx, "failed to get link during list, skipping", "key", key, "error", err)
			continue
		}
		links = append(links, link)
	}
	if readFailures > 0 {
		return links, errs.NewUnexpected(fmt.Sprintf("%d link(s) failed to read during list", readFailures))
	}
	return links, nil
}

// ListAllDocuments lists committee document metadata. When committeeUID is empty, all documents are returned.
//
// The keys come from a watcher snapshot whose end is checked (see visitKVSnapshot), not from ListKeys: a listing that
// ended early would look complete there, and the folder delete reads this list to refuse a folder that still holds a
// document, so an incomplete list must be an error. When the snapshot is cut short it returns the documents of the keys
// it did receive together with that error, so a caller looking for one document can still find it.
func (s *storage) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	watcher, errWatch := s.client.kvStore[constants.KVBucketNameCommitteeDocuments].WatchAll(ctx, jetstream.MetaOnly(), jetstream.IgnoreDeletes())
	if errWatch != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee documents bucket", errWatch)
	}
	var keys []string
	errSnapshot := visitKVSnapshot(ctx, watcher, func(entry jetstream.KeyValueEntry) {
		keys = append(keys, entry.Key())
	})
	docs := make([]*model.CommitteeDocument, 0)
	var readFailures int
	for _, key := range keys {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		doc := &model.CommitteeDocument{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeDocuments, key, doc, false); err != nil {
			readFailures++
			slog.WarnContext(ctx, "failed to get document during list, skipping", "key", key, "error", err)
			continue
		}
		if committeeUID != "" && doc.CommitteeUID != committeeUID {
			continue
		}
		docs = append(docs, doc)
	}
	if errSnapshot != nil {
		return docs, errSnapshot
	}
	if readFailures > 0 {
		return docs, errs.NewUnexpected(fmt.Sprintf("%d document(s) failed to read during list", readFailures))
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
		return writeError(ctx, s.client.kvStore[constants.KVBucketNameCommitteeLinks], link.UID, "link not found", "link has been modified since it was last read", "failed to update link", errUpdate)
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
		return writeError(ctx, s.client.kvStore[constants.KVBucketNameCommitteeFolders], folder.UID, "folder not found", "folder has been modified since it was last read", "failed to update folder", errUpdate)
	}
	return nil
}

// UpdateDocumentMetadata persists document metadata with optimistic concurrency. A changed name moves the
// (committee, name) uniqueness lookup key with it, as the PostgreSQL store's unique index does: the new name is
// reserved first, so a name another document holds is errs.Conflict and changes nothing, and the old name is
// released only once the record is written. A revision that is no longer current is a conflict before anything is
// reserved.
func (s *storage) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	docBytes, errMarshal := json.Marshal(doc)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal document", errMarshal)
	}
	kv := s.client.kvStore[constants.KVBucketNameCommitteeDocuments]

	// The record as it is stored tells which name the document holds the lookup key for.
	current := &model.CommitteeDocument{}
	currentRevision, errGet := s.get(ctx, constants.KVBucketNameCommitteeDocuments, doc.UID, current, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("document not found", fmt.Errorf("document UID: %s", doc.UID))
		}
		return errs.NewUnexpected("failed to read document before update", errGet)
	}
	if currentRevision != revision {
		return errs.NewConflict("document has been modified since it was last read")
	}

	oldKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, current.BuildIndexKey(ctx))
	newKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, doc.BuildIndexKey(ctx))
	renamed := newKey != oldKey

	var reserved uint64
	if renamed {
		rev, errReserve := kv.Create(ctx, newKey, []byte(doc.UID))
		if errReserve != nil {
			if isJetStreamCASConflict(errReserve) {
				// A key that holds this document's own uid is not adopted: two renames of the same document to the same
				// name would share the reservation, and the one that loses would release the key the winner now uses.
				return errs.NewConflict("document with the same name already exists for this committee")
			}
			return errs.NewUnexpected("failed to create unique key for document name", errReserve)
		}
		reserved = rev
	}

	if _, errUpdate := kv.Update(ctx, doc.UID, docBytes, revision); errUpdate != nil {
		if renamed {
			if isJetStreamCASConflict(errUpdate) {
				// Refused outright, so the record keeps its old name and the one reserved for it is nobody's. It is
				// released by the revision it was reserved at, so a key another writer took over is left alone.
				releaseLookupKey(ctx, kv, newKey, reserved, errUpdate, "document_uid", doc.UID, "committee_uid", doc.CommitteeUID)
			} else {
				// Not a refusal, so the write may or may not have landed (a client that disconnects mid-write is the usual
				// cause): read the record back and settle the keys by what it holds.
				settleDocumentRename(ctx, kv, doc, oldKey, newKey, reserved, errUpdate)
			}
		}
		return writeError(ctx, kv, doc.UID, "document not found", "document has been modified since it was last read", "failed to update document metadata", errUpdate)
	}

	if renamed {
		purgeOwnedLookupKey(ctx, kv, oldKey, doc.UID)
	}
	return nil
}

// settleDocumentRename resolves a rename whose write returned an error that is not a refusal, so it is not known whether
// the record now holds the new name. The record is read back on a context detached from the request, and the lookup keys
// follow what it holds: the new name means the write landed, so the old name is released; anything else means it did not,
// so the name reserved for it is released, by the revision it was reserved at so that a key another writer took over is
// left alone. When the record cannot be read back the state is not known, and both keys are kept rather than free a name
// that is in use: one of them stays locked until it is removed, which is logged.
//
// A crash between reserving the new name and writing the record leaves the same lock, and nothing here can settle it.
func settleDocumentRename(ctx context.Context, kv jetstream.KeyValue, doc *model.CommitteeDocument, oldKey, newKey string, reserved uint64, writeErr error) {
	ctx, cancel := recoveryCtx(ctx, rollbackTimeout)
	defer cancel()
	attrs := []any{"document_uid", doc.UID, "committee_uid", doc.CommitteeUID}

	entry, errGet := kv.Get(ctx, doc.UID)
	if errGet != nil {
		slog.ErrorContext(ctx, "could not read the document back after a rename write error; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"error", errGet, "write_error", writeErr, log.PriorityCritical()}, attrs...)...)
		return
	}
	stored := &model.CommitteeDocument{}
	if errDecode := json.Unmarshal(entry.Value(), stored); errDecode != nil {
		slog.ErrorContext(ctx, "could not decode the document after a rename write error; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"error", errDecode, "write_error", writeErr, log.PriorityCritical()}, attrs...)...)
		return
	}

	if fmt.Sprintf(constants.KVLookupDocumentPrefix, stored.BuildIndexKey(ctx)) == newKey {
		// The write landed: the record left the old name, so its key is released, as after a confirmed write.
		purgeOwnedLookupKey(ctx, kv, oldKey, doc.UID)
		return
	}
	releaseLookupKey(ctx, kv, newKey, reserved, writeErr, attrs...)
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
