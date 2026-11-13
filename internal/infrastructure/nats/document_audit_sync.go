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

	"github.com/google/uuid"
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
//
// Each reservation carries a token only this call knows, so a read-back can tell the key it created from one that
// merely holds the same document uid. The old name's key is read before the write and released by the revision it had
// then, so a key that was released and taken again in the meantime is left alone. A write that returned an error but
// is found to have landed is a success: the caller gets the record and the index is updated as after any update.
func (s *storage) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	if doc == nil {
		return errs.NewValidation("document cannot be nil")
	}
	docBytes, errMarshal := json.Marshal(doc)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal document", errMarshal)
	}
	kv := s.client.kvStore[constants.KVBucketNameCommitteeDocuments]
	attrs := []any{"document_uid", doc.UID, "committee_uid", doc.CommitteeUID}

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

	var oldRev, reserved uint64
	var reservation string
	if renamed {
		// Read before anything is reserved, so a failure here changes nothing. A key that is gone, or that another
		// document holds, is not this document's to release (oldRev stays 0).
		oldEntry, errOld := kv.Get(ctx, oldKey)
		switch {
		case errOld == nil:
			if lookupHeldBy(string(oldEntry.Value()), doc.UID) {
				oldRev = oldEntry.Revision()
			}
		case !errors.Is(errOld, jetstream.ErrKeyNotFound):
			return errs.NewUnexpected("failed to read the document's current name key before update", errOld)
		}

		reservation = fmt.Sprintf("%s#%s", doc.UID, uuid.NewString())
		rev, errReserve := kv.Create(ctx, newKey, []byte(reservation))
		if errReserve != nil {
			if isJetStreamCASConflict(errReserve) {
				// A key that holds this document's own uid is not adopted: two renames of the same document to the same
				// name would share the reservation, and the one that loses would release the key the winner now uses.
				return errs.NewConflict("document with the same name already exists for this committee")
			}
			// Not a refusal, so the reservation may have been stored with its reply lost: look for the key this call made.
			settleDocumentReservation(ctx, kv, newKey, reservation, errReserve, attrs)
			return errs.NewUnexpected("failed to create unique key for document name", errReserve)
		}
		reserved = rev
	}

	if _, errUpdate := kv.Update(ctx, doc.UID, docBytes, revision); errUpdate != nil {
		if renamed {
			if isJetStreamCASConflict(errUpdate) {
				// Refused outright, so the record keeps its old name and the one reserved for it is nobody's. It is
				// released by the revision it was reserved at, so a key another writer took over is left alone.
				releaseLookupKey(ctx, kv, newKey, reserved, errUpdate, attrs...)
			} else if settleDocumentRename(ctx, kv, doc, revision, oldKey, newKey, oldRev, reserved, errUpdate) {
				return nil
			}
		}
		return writeError(ctx, kv, doc.UID, "document not found", "document has been modified since it was last read", "failed to update document metadata", errUpdate)
	}

	if renamed {
		releaseOldDocumentName(ctx, kv, oldKey, oldRev, attrs)
	}
	return nil
}

// releaseOldDocumentName releases the name a record has left, by the revision its key had before the write. A key that
// was not the document's (oldRev 0) or that changed hands since is left alone.
func releaseOldDocumentName(ctx context.Context, kv jetstream.KeyValue, oldKey string, oldRev uint64, attrs []any) {
	if oldRev == 0 {
		return
	}
	releaseLookupKey(ctx, kv, oldKey, oldRev, nil, attrs...)
}

// settleDocumentReservation looks for the reservation a rename's Create may have stored before its reply was lost. The
// key is released only when it holds this call's token, by the revision it has, so a key that merely holds the same
// document uid (another rename's) is left alone. A key that is not there is nothing to release. When the key cannot be
// read the state is not known, so it is kept, which is logged.
func settleDocumentReservation(ctx context.Context, kv jetstream.KeyValue, newKey, reservation string, createErr error, attrs []any) {
	ctx, cancel := recoveryCtx(ctx, rollbackTimeout)
	defer cancel()

	entry, errGet := kv.Get(ctx, newKey)
	if errGet != nil {
		if !errors.Is(errGet, jetstream.ErrKeyNotFound) {
			slog.ErrorContext(ctx, "could not read the new name's key after a reservation error; if it was stored the name stays locked until it is removed",
				append([]any{"error", errGet, "create_error", createErr, log.PriorityCritical()}, attrs...)...)
		}
		return
	}
	if string(entry.Value()) != reservation {
		return
	}
	purgeLookupKey(ctx, kv, newKey, createErr, attrs, jetstream.LastRevision(entry.Revision()))
}

// settleDocumentRename resolves a rename whose write returned an error that is not a refusal, so it is not known
// whether the record now holds the new name. The record is read back on a context detached from the request, and the
// lookup keys follow what it holds:
//   - the new name: the write landed, so the old name is released (a record that is exactly what was written is a
//     success, which is returned so the caller completes the update; one changed since is not);
//   - neither name, or the old name at a later revision: the write can no longer land, since it needs the revision it
//     was sent at, so the new name's key is released by its revision, and the old name's key too when the record has left it;
//   - the old name at the revision it was sent at: the write may still be on its way, and a key released now could be
//     taken by another document before it lands, so both keys are kept and the condition is logged;
//   - unreadable: the state is not known, so both keys are kept.
//
// Every release is guarded by the revision the key had, so a key that changed hands is left alone. A crash between
// reserving the new name and writing the record leaves the same lock, and nothing here can settle it.
func settleDocumentRename(ctx context.Context, kv jetstream.KeyValue, doc *model.CommitteeDocument, revision uint64, oldKey, newKey string, oldRev, reserved uint64, writeErr error) bool {
	ctx, cancel := recoveryCtx(ctx, rollbackTimeout)
	defer cancel()
	attrs := []any{"document_uid", doc.UID, "committee_uid", doc.CommitteeUID}

	entry, errGet := kv.Get(ctx, doc.UID)
	if errGet != nil {
		slog.ErrorContext(ctx, "could not read the document back after a rename write error; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"error", errGet, "write_error", writeErr, log.PriorityCritical()}, attrs...)...)
		return false
	}
	stored := &model.CommitteeDocument{}
	if errDecode := json.Unmarshal(entry.Value(), stored); errDecode != nil {
		slog.ErrorContext(ctx, "could not decode the document after a rename write error; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"error", errDecode, "write_error", writeErr, log.PriorityCritical()}, attrs...)...)
		return false
	}

	storedKey := fmt.Sprintf(constants.KVLookupDocumentPrefix, stored.BuildIndexKey(ctx))
	switch {
	case storedKey == newKey:
		releaseOldDocumentName(ctx, kv, oldKey, oldRev, attrs)
		return sameDocumentRecord(stored, doc)
	case storedKey == oldKey && entry.Revision() == revision:
		slog.ErrorContext(ctx, "a rename write errored and the record still holds the old name; keeping the new name's key because the write may still land, and it stays locked until it is removed",
			append([]any{"write_error", writeErr, "new_key", newKey, log.PriorityCritical()}, attrs...)...)
		return false
	default:
		if storedKey != oldKey {
			releaseOldDocumentName(ctx, kv, oldKey, oldRev, attrs)
		}
		releaseLookupKey(ctx, kv, newKey, reserved, writeErr, attrs...)
		return false
	}
}

// sameDocumentRecord reports whether the stored record is what a rename wrote: the fields an update changes, and the
// time it stamped.
func sameDocumentRecord(stored, written *model.CommitteeDocument) bool {
	folder := func(d *model.CommitteeDocument) string {
		if d.FolderUID == nil {
			return ""
		}
		return *d.FolderUID
	}
	return stored.Name == written.Name && stored.Description == written.Description &&
		folder(stored) == folder(written) && stored.UpdatedAt.Equal(written.UpdatedAt)
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
