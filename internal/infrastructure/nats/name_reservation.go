// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// namedRecord is a record kind whose name is unique within its committee, the name being held by a lookup key in the
// same bucket (lookup/<kind>/<sha256(committeeUID|name)>). It owns the update of such a record, which moves the lookup
// key when the name changes.
type namedRecord[T any] struct {
	s *storage
	// bucket is the KV bucket that holds the records and their lookup keys.
	bucket string
	// noun names the record kind in messages and in the "<noun>_uid" log attribute.
	noun string
	// failMsg is the message of the error returned for an unexpected write failure.
	failMsg string
	// lookupFormat formats a record's name hash into its lookup key.
	lookupFormat string
	uid          func(*T) string
	committeeUID func(*T) string
	// indexKey returns the hash of a record's committee and name.
	indexKey func(context.Context, *T) string
}

func (c namedRecord[T]) attrs(rec *T) []any {
	return []any{c.noun + "_uid", c.uid(rec), "committee_uid", c.committeeUID(rec)}
}

func (c namedRecord[T]) notFound(rec *T) error {
	return errs.NewNotFound(c.noun+" not found", fmt.Errorf("%s UID: %s", c.noun, c.uid(rec)))
}

func (c namedRecord[T]) staleMsg() string {
	return c.noun + " has been modified since it was last read"
}

// update replaces a record when the revision is still current. A changed name moves the (committee, name) uniqueness
// lookup key with it, as the PostgreSQL store's unique index does: the new name is reserved first, so a name another
// record holds is errs.Conflict and changes nothing, and the old name is released only once the record is written. A
// revision that is no longer current is a conflict before anything is reserved.
//
// Each reservation carries a token only this call knows, so a read-back can tell the key it created from one that
// merely holds the same record uid. The old name's key is read before the write and released by the revision it had
// then, so a key that was released and taken again in the meantime is left alone. The record's revision is checked
// again after that read, so the key read is known to belong to the record as the caller saw it. A write that returned
// an error but is found, byte for byte, to have landed is a success, for a rename and for an update that keeps the
// name: the caller gets the record and the index is updated as after any update.
func (c namedRecord[T]) update(ctx context.Context, rec *T, revision uint64) error {
	uid := c.uid(rec)
	data, errMarshal := json.Marshal(rec)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal "+c.noun, errMarshal)
	}
	kv := c.s.client.kvStore[c.bucket]
	attrs := c.attrs(rec)

	// The record as it is stored tells which name it holds the lookup key for.
	current := new(T)
	currentRevision, errGet := c.s.get(ctx, c.bucket, uid, current, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return c.notFound(rec)
		}
		return errs.NewUnexpected("failed to read "+c.noun+" before update", errGet)
	}
	if currentRevision != revision {
		return errs.NewConflict(c.staleMsg())
	}

	oldKey := fmt.Sprintf(c.lookupFormat, c.indexKey(ctx, current))
	newKey := fmt.Sprintf(c.lookupFormat, c.indexKey(ctx, rec))
	renamed := newKey != oldKey

	var oldRev, reserved uint64
	if renamed {
		// Read before anything is reserved, so a failure here changes nothing. A key that is gone, or that another
		// record holds, is not this record's to release (oldRev stays 0).
		oldEntry, errOld := kv.Get(ctx, oldKey)
		switch {
		case errOld == nil:
			if lookupHeldBy(string(oldEntry.Value()), uid) {
				oldRev = oldEntry.Revision()
			}
		case !errors.Is(errOld, jetstream.ErrKeyNotFound):
			return errs.NewUnexpected("failed to read the "+c.noun+"'s current name key before update", errOld)
		}
		// Another rename of this record may have changed the key since the record was read, and a reservation of its
		// own can hold the record's uid too. A record still at the caller's revision means the key was read while it
		// held the name the caller saw; otherwise nothing has been reserved and the caller retries.
		recheck, errRecheck := kv.Get(ctx, uid)
		if errRecheck != nil {
			if errors.Is(errRecheck, jetstream.ErrKeyNotFound) {
				return c.notFound(rec)
			}
			return errs.NewUnexpected("failed to read "+c.noun+" before reserving its new name", errRecheck)
		}
		if recheck.Revision() != revision {
			return errs.NewConflict(c.staleMsg())
		}

		reservation := fmt.Sprintf("%s#%s", uid, uuid.NewString())
		rev, errReserve := kv.Create(ctx, newKey, []byte(reservation))
		if errReserve != nil {
			if isJetStreamCASConflict(errReserve) {
				// A key that holds this record's own uid is not adopted: two renames of the same record to the same
				// name would share the reservation, and the one that loses would release the key the winner now uses.
				return errs.NewConflict(c.noun + " with the same name already exists for this committee")
			}
			// Not a refusal, so the reservation may have been stored with its reply lost: look for the key this call made.
			c.settleReservation(ctx, kv, newKey, reservation, errReserve, attrs)
			return errs.NewUnexpected("failed to create unique key for "+c.noun+" name", errReserve)
		}
		reserved = rev

		// A request that has ended since the name was reserved never sends the write, so nothing can land and the
		// reservation is nobody's. (An error returned after the write is sent is ambiguous and is settled below.)
		if errCtx := ctx.Err(); errCtx != nil {
			releaseLookupKey(ctx, kv, newKey, reserved, errCtx, attrs...)
			return errs.NewUnexpected("request ended before the "+c.noun+" write was sent", errCtx)
		}
	}

	if _, errUpdate := kv.Update(ctx, uid, data, revision); errUpdate != nil {
		conflict := isJetStreamCASConflict(errUpdate)
		switch {
		case !renamed:
			// No key was reserved, so there is nothing to settle but the outcome itself.
			if !conflict && c.writeLanded(ctx, kv, rec, data, errUpdate) {
				return nil
			}
		case conflict:
			// Refused outright, so the record keeps its old name and the one reserved for it is nobody's. It is
			// released by the revision it was reserved at, so a key another writer took over is left alone.
			releaseLookupKey(ctx, kv, newKey, reserved, errUpdate, attrs...)
		case c.settleRename(ctx, kv, rec, data, revision, oldKey, newKey, oldRev, reserved, errUpdate):
			return nil
		}
		return writeError(ctx, kv, uid, c.noun+" not found", c.staleMsg(), c.failMsg, errUpdate)
	}

	if renamed {
		releaseOldName(ctx, kv, oldKey, oldRev, attrs)
	}
	return nil
}

// releaseOldName releases the name a record has left, by the revision its key had before the write. A key that was not
// the record's (oldRev 0) or that changed hands since is left alone.
func releaseOldName(ctx context.Context, kv jetstream.KeyValue, oldKey string, oldRev uint64, attrs []any) {
	if oldRev == 0 {
		return
	}
	releaseLookupKey(ctx, kv, oldKey, oldRev, nil, attrs...)
}

// settleReservation looks for the reservation a rename's Create may have stored before its reply was lost. The key is
// released only when it holds this call's token, by the revision it has, so a key that merely holds the same record uid
// (another rename's) is left alone. A key that is not there is nothing to release. When the key cannot be read the
// state is not known, so it is kept, which is logged.
func (c namedRecord[T]) settleReservation(ctx context.Context, kv jetstream.KeyValue, newKey, reservation string, createErr error, attrs []any) {
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

// writeLanded reports whether the record holds exactly the bytes a write sent, which is the case only when that write
// landed: the stored value is what was sent, so a record changed since (an audit backfill changes users without
// touching updated_at) is not mistaken for it. A record that cannot be read is not reported as landed.
func (c namedRecord[T]) writeLanded(ctx context.Context, kv jetstream.KeyValue, rec *T, data []byte, writeErr error) bool {
	ctx, cancel := recoveryCtx(ctx, rollbackTimeout)
	defer cancel()

	entry, errGet := kv.Get(ctx, c.uid(rec))
	if errGet != nil {
		slog.WarnContext(ctx, "could not read the "+c.noun+" back after an update write error; the write is reported as failed",
			append([]any{"error", errGet, "write_error", writeErr}, c.attrs(rec)...)...)
		return false
	}
	return bytes.Equal(entry.Value(), data)
}

// settleRename resolves a rename whose write returned an error that is not a refusal, so it is not known whether the
// record now holds the new name. The record is read back on a context detached from the request, and the lookup keys
// follow what it holds:
//   - the new name: the write landed, so the old name is released (a record that is byte for byte what was written is a
//     success, which is returned so the caller completes the update; one changed since is not);
//   - deleted: the write can no longer apply, and the delete released only the name the record held when it read it, so
//     the old name's key and the new name's key are both released;
//   - neither name, or the old name at a later revision: the write can no longer land, since it needs the revision it
//     was sent at, so the new name's key is released by its revision, and the old name's key too when the record has left it;
//   - the old name at the revision it was sent at: the write may still be on its way, and a key released now could be
//     taken by another record before it lands, so both keys are kept and the condition is logged;
//   - unreadable, or older than the revision the write was sent at (a stale read from a lagging replica): the state is
//     not known, so both keys are kept.
//
// Every release is guarded by the revision the key had, so a key that changed hands is left alone. A crash between
// reserving the new name and writing the record leaves the same lock, and nothing here can settle it.
func (c namedRecord[T]) settleRename(ctx context.Context, kv jetstream.KeyValue, rec *T, data []byte, revision uint64, oldKey, newKey string, oldRev, reserved uint64, writeErr error) bool {
	ctx, cancel := recoveryCtx(ctx, rollbackTimeout)
	defer cancel()
	attrs := c.attrs(rec)

	entry, errGet := kv.Get(ctx, c.uid(rec))
	if errors.Is(errGet, jetstream.ErrKeyNotFound) {
		// A deleted record can never take the write: it needs the revision it was sent at, and the delete moved the key
		// past it. The delete released only the name the record held when it read it, so both captured keys are released.
		releaseOldName(ctx, kv, oldKey, oldRev, attrs)
		releaseLookupKey(ctx, kv, newKey, reserved, writeErr, attrs...)
		return false
	}
	if errGet != nil {
		slog.ErrorContext(ctx, "could not read the "+c.noun+" back after a rename write error; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"error", errGet, "write_error", writeErr, log.PriorityCritical()}, attrs...)...)
		return false
	}
	if entry.Revision() < revision {
		// The record was read at this revision before the write was sent, and revisions only grow, so an older one is
		// a stale read (KV reads may be served by a replica that lags the leader). It says nothing about whether the
		// pending write can still apply, so both keys are kept, as for a record that cannot be read.
		slog.ErrorContext(ctx, "the "+c.noun+" read back after a rename write error is older than the revision the write was sent at; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"write_error", writeErr, "read_revision", entry.Revision(), "write_revision", revision, log.PriorityCritical()}, attrs...)...)
		return false
	}
	stored := new(T)
	if errDecode := json.Unmarshal(entry.Value(), stored); errDecode != nil {
		slog.ErrorContext(ctx, "could not decode the "+c.noun+" after a rename write error; keeping both name keys, and the one the record does not use stays locked until it is removed",
			append([]any{"error", errDecode, "write_error", writeErr, log.PriorityCritical()}, attrs...)...)
		return false
	}

	storedKey := fmt.Sprintf(c.lookupFormat, c.indexKey(ctx, stored))
	switch {
	case storedKey == newKey:
		releaseOldName(ctx, kv, oldKey, oldRev, attrs)
		return bytes.Equal(entry.Value(), data)
	case storedKey == oldKey && entry.Revision() == revision:
		slog.ErrorContext(ctx, "a rename write errored and the record still holds the old name; keeping the new name's key because the write may still land, and it stays locked until it is removed",
			append([]any{"write_error", writeErr, "new_key", newKey, log.PriorityCritical()}, attrs...)...)
		return false
	default:
		if storedKey != oldKey {
			releaseOldName(ctx, kv, oldKey, oldRev, attrs)
		}
		releaseLookupKey(ctx, kv, newKey, reserved, writeErr, attrs...)
		return false
	}
}

// documentNames and folderNames are the two record kinds whose names are held by lookup keys.
func (s *storage) documentNames() namedRecord[model.CommitteeDocument] {
	return namedRecord[model.CommitteeDocument]{
		s: s, bucket: constants.KVBucketNameCommitteeDocuments, noun: "document", failMsg: "failed to update document metadata",
		lookupFormat: constants.KVLookupDocumentPrefix,
		uid:          func(d *model.CommitteeDocument) string { return d.UID },
		committeeUID: func(d *model.CommitteeDocument) string { return d.CommitteeUID },
		indexKey:     func(ctx context.Context, d *model.CommitteeDocument) string { return d.BuildIndexKey(ctx) },
	}
}

func (s *storage) folderNames() namedRecord[model.CommitteeLinkFolder] {
	return namedRecord[model.CommitteeLinkFolder]{
		s: s, bucket: constants.KVBucketNameCommitteeFolders, noun: "folder", failMsg: "failed to update folder",
		lookupFormat: constants.KVLookupFolderPrefix,
		uid:          func(f *model.CommitteeLinkFolder) string { return f.UID },
		committeeUID: func(f *model.CommitteeLinkFolder) string { return f.CommitteeUID },
		indexKey:     func(ctx context.Context, f *model.CommitteeLinkFolder) string { return f.BuildIndexKey(ctx) },
	}
}
