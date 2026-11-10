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
	"strings"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	memberCreatePending   = "pending"
	memberCreateCommitted = "committed"
	memberCreateAborted   = "aborted"
)

// The member UID is also the operation's stable identity. Keeping the lifecycle
// at that primary key fences delayed Create calls, including pre-protocol ones.
// Only pending records retain notification intent; committed records retain a
// small completion token, and aborted records contain no personal member data.
type memberCreateRecord struct {
	model.CommitteeMemberBase
	Create *memberCreateState `json:"_create,omitempty"`
}

type memberCreateState struct {
	State    string                   `json:"state"`
	Token    string                   `json:"token,omitempty"`
	Intent   *port.MemberCreateIntent `json:"intent,omitempty"`
	Identity *memberCreateIdentity    `json:"identity,omitempty"`
}

var _ port.RecoverableCommitteeMemberWriter = (*storage)(nil)

// SupportsMemberCreateRecovery reports the KV protocol's availability.
func (s *storage) SupportsMemberCreateRecovery() bool { return true }

func decodeMemberCreateRecord(data []byte) (*memberCreateRecord, error) {
	var record memberCreateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.Create != nil {
		switch record.Create.State {
		case memberCreatePending, memberCreateCommitted, memberCreateAborted:
		default:
			return nil, errs.NewServiceUnavailable("member creation state is unavailable")
		}
	}
	return &record, nil
}

func (r *memberCreateRecord) member() *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: r.CommitteeMemberBase}
}

func memberCreateTemporary(err error) error {
	return errs.NewServiceUnavailable("member creation is not yet confirmed; retry later", err)
}

// memberCreateOperationKey is a stable preparation identity, even when acquiring
// the uniqueness reservation never landed. It is not a membership lookup.
func memberCreateOperationKey(ctx context.Context, committeeUID, email string) string {
	member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{CommitteeUID: committeeUID, Email: email}}
	return fmt.Sprintf(constants.KVLookupMemberCreatePrefix, member.BuildIndexKey(ctx))
}

// FindMemberCreate selects the original snapshot BEFORE shared current identity
// and group validation. Ordinary readers deliberately cannot see pending state.
func (s *storage) FindMemberCreate(ctx context.Context, committeeUID, email string) (*port.MemberCreatePreparation, error) {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	key := memberCreateOperationKey(ctx, committeeUID, email)
	op, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return s.findExistingMemberCreate(ctx, committeeUID, email)
	}
	if err != nil {
		return nil, memberCreateTemporary(err)
	}
	prepared, err := decodeMemberCreateRecord(op.Value())
	if err != nil || prepared.Create == nil || prepared.UID == "" {
		return nil, memberCreateTemporary(err)
	}
	entry, err := kv.Get(ctx, prepared.UID)
	var record *memberCreateRecord
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		if prepared.Create.State == memberCreatePending {
			record = prepared
		} else {
			// A terminal UID without its primary fence must be fenced again
			// before allowing a new generation (never purge the old UID).
			data, _ := json.Marshal(abortedMemberCreate(prepared.UID))
			if _, err := kv.Create(ctx, prepared.UID, data); err != nil {
				return nil, memberCreateTemporary(err)
			}
			return s.findExistingMemberCreate(ctx, committeeUID, email)
		}
	case err != nil:
		return nil, memberCreateTemporary(err)
	default:
		record, err = decodeMemberCreateRecord(entry.Value())
		if err != nil || record.UID != prepared.UID || record.Create == nil {
			return nil, memberCreateTemporary(err)
		}
	}
	if record.Create.State == memberCreateAborted {
		if err := s.retireMemberCreate(ctx, key, record.UID, memberCreateAborted); err != nil {
			return nil, err
		}
		return s.findExistingMemberCreate(ctx, committeeUID, email)
	}
	if record.CommitteeUID != committeeUID || normalizeMemberCreateEmail(record.Email) != normalizeMemberCreateEmail(email) {
		if record.Create.State == memberCreatePending {
			return nil, memberCreateTemporary(nil)
		}
		if err := s.retireMemberCreate(ctx, key, record.UID, memberCreateCommitted); err != nil {
			return nil, err
		}
		return s.findExistingMemberCreate(ctx, committeeUID, email)
	}
	if record.Create.State == memberCreateCommitted {
		if err := s.retireMemberCreate(ctx, key, record.UID, memberCreateCommitted); err != nil {
			return nil, err
		}
		return s.confirmExistingMemberCreate(ctx, record, entry)
	}
	if record.Create.Intent == nil {
		return nil, memberCreateTemporary(nil)
	}
	return &port.MemberCreatePreparation{Member: record.member(), Intent: *record.Create.Intent, Pending: true}, nil
}

// CreateMemberRecoverable persists only the snapshot selected and validated by
// this invocation. Discovering another pending UID requires selection/validation
// on a retry, never silently substituting unvalidated fields at commit time.
func (s *storage) CreateMemberRecoverable(ctx context.Context, member *model.CommitteeMember, intent port.MemberCreateIntent) (*port.MemberCreateResult, error) {
	if member == nil || member.UID == "" || member.CommitteeUID == "" || strings.TrimSpace(member.Email) == "" {
		return nil, errs.NewValidation("member UID, committee UID and email are required")
	}
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	if entry, err := kv.Get(ctx, member.UID); err == nil {
		prior, decodeErr := decodeMemberCreateRecord(entry.Value())
		if decodeErr != nil || (prior.Create != nil && prior.Create.State == memberCreateAborted) {
			return nil, memberCreateTemporary(decodeErr)
		}
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, memberCreateTemporary(err)
	}
	opKey := memberCreateOperationKey(ctx, member.CommitteeUID, member.Email)
	selected, err := s.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
	if err != nil {
		return nil, err
	}
	record := &memberCreateRecord{CommitteeMemberBase: member.CommitteeMemberBase, Create: &memberCreateState{State: memberCreatePending, Intent: &intent}}
	if selected != nil {
		if !selected.Pending {
			return &port.MemberCreateResult{Member: selected.Member}, nil
		}
		if selected.Member.UID != member.UID {
			return nil, memberCreateTemporary(nil)
		}
		record = &memberCreateRecord{CommitteeMemberBase: selected.Member.CommitteeMemberBase, Create: &memberCreateState{State: memberCreatePending, Intent: &selected.Intent}}
		// Member inputs and intent remain immutable. Only parent metadata that
		// the service refreshed during current validation may change on resume.
		record.CommitteeName, record.CommitteeCategory = member.CommitteeName, member.CommitteeCategory
		record.ProjectUID, record.ProjectSlug = member.ProjectUID, member.ProjectSlug
		if err := s.indexMemberCreateIdentity(ctx, record); err != nil {
			return nil, err
		}
	} else {
		// Install discovery before the preparation: an identity event can fence
		// this UID even when the preparation/primary write has not landed yet.
		if err := s.indexMemberCreateIdentity(ctx, record); err != nil {
			return nil, err
		}
		data, _ := json.Marshal(record)
		previous, err := kv.Get(ctx, opKey)
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound):
			_, err = kv.Create(ctx, opKey, data)
		case err == nil:
			prior, decodeErr := decodeMemberCreateRecord(previous.Value())
			if decodeErr != nil || prior.Create == nil || prior.Create.State == memberCreatePending || prior.UID == record.UID {
				return nil, memberCreateTemporary(decodeErr)
			}
			_, err = kv.Update(ctx, opKey, data, previous.Revision())
		}
		if err != nil {
			return nil, memberCreateTemporary(err)
		}
	}
	data, _ := json.Marshal(record)
	if _, err := kv.Create(ctx, record.UID, data); err != nil {
		state, _ := readRecordState(ctx, kv, record.UID, data)
		if state != recordMatches {
			current, readErr := kv.Get(ctx, record.UID)
			samePending := false
			if readErr == nil {
				prior, decodeErr := decodeMemberCreateRecord(current.Value())
				if decodeErr == nil && prior.Create != nil && prior.Create.State == memberCreateAborted {
					if err := s.AbortMemberCreate(ctx, record.member()); err != nil {
						return nil, err
					}
					return nil, memberCreateTemporary(nil)
				}
				samePending = decodeErr == nil && prior.UID == record.UID && prior.Create != nil && prior.Create.State == memberCreatePending
			}
			// Refreshed parent/identity metadata can differ from the pending primary.
			// That same UID must still acquire its reservation before completion.
			if !samePending {
				// Another completion may already have won this same operation.
				return s.finishMemberCreate(ctx, record.UID, buildMemberLookupKeys(ctx, record.member()).unique, opKey, record.member())
			}
		}
	}
	unique := buildMemberLookupKeys(ctx, record.member()).unique
	for range 4 {
		_, err := reserveOwnedKey(ctx, kv, record.UID, unique)
		if err == nil {
			return s.finishMemberCreate(ctx, record.UID, unique, opKey, record.member())
		}
		// In particular, never erase intent after an ambiguous acquisition.
		if !isJetStreamCASConflict(err) {
			return nil, memberCreateTemporary(err)
		}
		lock, err := kv.Get(ctx, unique)
		if err != nil {
			return nil, memberCreateTemporary(err)
		}
		owner := string(lock.Value())
		if owner == record.UID {
			return s.finishMemberCreate(ctx, owner, unique, opKey, record.member())
		}
		entry, err := kv.Get(ctx, owner)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			fence, _ := json.Marshal(abortedMemberCreate(owner))
			_, _ = kv.Create(ctx, owner, fence)
			entry, err = kv.Get(ctx, owner)
		}
		if err != nil {
			return nil, memberCreateTemporary(err)
		}
		stored, err := decodeMemberCreateRecord(entry.Value())
		if err != nil || stored.UID != owner {
			return nil, memberCreateTemporary(err)
		}
		if stored.Create != nil && stored.Create.State == memberCreateAborted {
			if err := kv.Purge(ctx, unique, jetstream.LastRevision(lock.Revision())); err != nil {
				return nil, memberCreateTemporary(err)
			}
			continue
		}
		if stored.CommitteeUID != member.CommitteeUID || normalizeMemberCreateEmail(stored.Email) != normalizeMemberCreateEmail(member.Email) {
			return nil, memberCreateTemporary(nil)
		}
		if stored.Create == nil || stored.Create.State == memberCreateCommitted {
			if _, err := s.confirmExistingMemberCreate(ctx, stored, entry); err != nil {
				return nil, err
			}
		}
		// The conflicting owner is authoritative. Fence this uncommitted
		// candidate before moving preparation to that owner's snapshot.
		if err := s.AbortMemberCreate(ctx, record.member()); err != nil {
			return nil, err
		}
		previous, err := kv.Get(ctx, opKey)
		if err != nil {
			return nil, memberCreateTemporary(err)
		}
		prior, err := decodeMemberCreateRecord(previous.Value())
		if err != nil || prior.UID != record.UID || prior.Create == nil || prior.Create.State != memberCreateAborted {
			return nil, memberCreateTemporary(err)
		}
		value, _ := json.Marshal(stored)
		if stored.Create.State == memberCreateCommitted {
			value, _ = json.Marshal(terminalMemberCreate(owner, memberCreateCommitted))
		}
		if _, err := kv.Update(ctx, opKey, value, previous.Revision()); err != nil {
			return nil, memberCreateTemporary(err)
		}
		if stored.Create.State == memberCreateCommitted {
			return &port.MemberCreateResult{Member: stored.member()}, nil
		}
		// A legacy/in-flight snapshot has not passed this invocation's
		// current validation. The next retry selects it before validation.
		return nil, memberCreateTemporary(nil)
	}
	return nil, memberCreateTemporary(nil)
}

// findExistingMemberCreate confirms a reservation owner without validating replacement
// import fields or creating a throwaway operation. Legacy memberships stay visible.
func (s *storage) findExistingMemberCreate(ctx context.Context, committeeUID, email string) (*port.MemberCreatePreparation, error) {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{CommitteeUID: committeeUID, Email: email}}
	lock, err := kv.Get(ctx, buildMemberLookupKeys(ctx, member).unique)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, memberCreateTemporary(err)
	}
	entry, err := kv.Get(ctx, string(lock.Value()))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, memberCreateTemporary(err)
	}
	record, err := decodeMemberCreateRecord(entry.Value())
	if err != nil || record.UID != string(lock.Value()) {
		return nil, memberCreateTemporary(err)
	}
	if record.Create != nil && record.Create.State == memberCreateAborted {
		return nil, nil
	}
	if record.CommitteeUID != committeeUID || normalizeMemberCreateEmail(record.Email) != normalizeMemberCreateEmail(email) {
		return nil, memberCreateTemporary(nil)
	}
	return s.confirmExistingMemberCreate(ctx, record, entry)
}

// confirmExistingMemberCreate fences legacy rollback before repairing owned indexes.
// A visible membership must never become a pending candidate that current policy can abort.
func (s *storage) confirmExistingMemberCreate(ctx context.Context, record *memberCreateRecord, entry jetstream.KeyValueEntry) (*port.MemberCreatePreparation, error) {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	data := entry.Value()
	if record.Create == nil {
		record.Create = &memberCreateState{State: memberCreateCommitted, Token: uuid.NewString()}
		data, _ = json.Marshal(record)
		if _, err := kv.Update(ctx, record.UID, data, entry.Revision()); err != nil {
			return nil, memberCreateTemporary(err)
		}
	}
	if record.Create.State != memberCreateCommitted {
		return nil, memberCreateTemporary(nil)
	}
	keys := buildMemberLookupKeys(ctx, record.member())
	var written []string
	for _, key := range append([]string{keys.unique}, memberKeyStrings(keys.indexes())...) {
		if _, err := reserveOwnedKey(ctx, kv, record.UID, key); err != nil {
			s.releaseStaleMemberKeys(ctx, record.UID, written)
			return nil, memberCreateTemporary(err)
		}
		written = append(written, key)
	}
	if state, _ := readRecordState(ctx, kv, record.UID, data); state != recordMatches {
		s.releaseStaleMemberKeys(ctx, record.UID, written)
		return nil, memberCreateTemporary(nil)
	}
	return &port.MemberCreatePreparation{Member: record.member()}, nil
}

func memberKeyStrings(indexes []memberIndexKey) []string {
	keys := make([]string, 0, len(indexes))
	for _, index := range indexes {
		keys = append(keys, index.key)
	}
	return keys
}

// AbortMemberCreatesForIdentity visits only matching identity-index entries after
// a once-per-bucket compatibility bootstrap. Failures remain retryable for ACK/NAK.
func (s *storage) AbortMemberCreatesForIdentity(ctx context.Context, username, email string) error {
	member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{Username: username, Email: email}}
	identity := newMemberCreateIdentity(ctx, member)
	if identity.EmailHash == "" && identity.UsernameHash == "" {
		return nil
	}
	if err := s.bootstrapMemberCreateIdentities(ctx); err != nil {
		return err
	}
	filter := fmt.Sprintf(constants.KVLookupMemberCreatesByEmailFilter, identity.EmailHash)
	if identity.EmailHash == "" {
		filter = fmt.Sprintf(constants.KVLookupMemberCreatesByUsernameFilter, identity.UsernameHash)
	}
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	watcher, err := kv.Watch(ctx, filter, jetstream.MetaOnly(), jetstream.IgnoreDeletes())
	if err != nil {
		return memberCreateTemporary(err)
	}
	return visitMemberCreateSnapshot(ctx, watcher, func(key jetstream.KeyValueEntry) error {
		entry, err := kv.Get(ctx, key.Key())
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return memberCreateTemporary(err)
		}
		var indexed memberCreateIdentity
		if err := json.Unmarshal(entry.Value(), &indexed); err != nil || indexed.UID == "" || indexed.OperationKey == "" {
			return memberCreateTemporary(err)
		}
		if (identity.EmailHash != "" && identity.EmailHash != indexed.EmailHash) ||
			(identity.UsernameHash != "" && identity.UsernameHash != indexed.UsernameHash) {
			return nil
		}
		return s.abortIndexedMemberCreate(ctx, &indexed)
	})
}

func normalizeMemberCreateEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func terminalMemberCreate(uid, state string) *memberCreateRecord {
	return &memberCreateRecord{CommitteeMemberBase: model.CommitteeMemberBase{UID: uid}, Create: &memberCreateState{State: state}}
}

func abortedMemberCreate(uid string) *memberCreateRecord {
	return terminalMemberCreate(uid, memberCreateAborted)
}

// retireMemberCreate clears current-value personal preparation data. Historical
// revisions follow the separately approved bucket retention policy.
func (s *storage) retireMemberCreate(ctx context.Context, key, uid, state string) error {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	entry, err := kv.Get(ctx, key)
	if err != nil {
		return memberCreateTemporary(err)
	}
	record, err := decodeMemberCreateRecord(entry.Value())
	if err != nil || record.Create == nil {
		return memberCreateTemporary(err)
	}
	if record.UID != uid {
		return nil // A new generation owns the operation key; never retire it.
	}
	identity := record.Create.Identity
	if identity == nil && record.Email != "" {
		identity = newMemberCreateIdentity(ctx, record.member())
	}
	terminal := terminalMemberCreate(uid, state)
	terminal.Create.Identity = identity
	data, _ := json.Marshal(terminal)
	if !bytes.Equal(entry.Value(), data) {
		if _, err := kv.Update(ctx, key, data, entry.Revision()); err != nil {
			found, _ := readRecordState(ctx, kv, key, data)
			if found != recordMatches {
				return memberCreateTemporary(err)
			}
		}
	}
	if identity == nil {
		return nil
	}
	if err := s.removeMemberCreateIdentity(ctx, identity); err != nil {
		return err
	}
	// Retain the hashed cleanup descriptor until index removal succeeds, so
	// retirement-only failures can be retried without retaining personal data.
	entry, err = kv.Get(ctx, key)
	if err != nil {
		return memberCreateTemporary(err)
	}
	if !bytes.Equal(entry.Value(), data) {
		return nil
	}
	minimal, _ := json.Marshal(terminalMemberCreate(uid, state))
	if _, err := kv.Update(ctx, key, minimal, entry.Revision()); err != nil {
		found, _ := readRecordState(ctx, kv, key, minimal)
		if found != recordMatches {
			return memberCreateTemporary(err)
		}
	}
	return nil
}

// AbortMemberCreate rejects an invalid pending snapshot with a primary CAS
// fence BEFORE releasing its reservation or removing its personal intent.
func (s *storage) AbortMemberCreate(ctx context.Context, member *model.CommitteeMember) error {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	entry, err := kv.Get(ctx, member.UID)
	fence, _ := json.Marshal(abortedMemberCreate(member.UID))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		_, err = kv.Create(ctx, member.UID, fence)
	} else if err == nil {
		record, decodeErr := decodeMemberCreateRecord(entry.Value())
		if decodeErr != nil || record.Create == nil || record.Create.State == memberCreateCommitted {
			return memberCreateTemporary(decodeErr)
		}
		if record.Create.State != memberCreateAborted {
			_, err = kv.Update(ctx, member.UID, fence, entry.Revision())
		}
	}
	if err != nil {
		found, _ := readRecordState(ctx, kv, member.UID, fence)
		if found != recordMatches {
			return memberCreateTemporary(err)
		}
	}
	keys := buildMemberLookupKeys(ctx, member)
	lock, err := kv.Get(ctx, keys.unique)
	if err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return memberCreateTemporary(err)
	}
	if err == nil && string(lock.Value()) == member.UID {
		if err := kv.Purge(ctx, keys.unique, jetstream.LastRevision(lock.Revision())); err != nil {
			return memberCreateTemporary(err)
		}
	}
	return s.retireMemberCreate(ctx, memberCreateOperationKey(ctx, member.CommitteeUID, member.Email), member.UID, memberCreateAborted)
}

func (s *storage) finishMemberCreate(ctx context.Context, uid, unique, opKey string, parent *model.CommitteeMember) (*port.MemberCreateResult, error) {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	entry, err := kv.Get(ctx, uid)
	if err != nil {
		return nil, memberCreateTemporary(err)
	}
	record, err := decodeMemberCreateRecord(entry.Value())
	if err != nil || record.Create == nil || record.UID != uid {
		return nil, memberCreateTemporary(err)
	}
	if buildMemberLookupKeys(ctx, record.member()).unique != unique {
		return nil, memberCreateTemporary(nil)
	}
	if record.Create.State == memberCreateCommitted {
		if err := s.retireMemberCreate(ctx, opKey, uid, memberCreateCommitted); err != nil {
			return nil, err
		}
		return &port.MemberCreateResult{Member: record.member()}, nil
	}
	if record.Create.State != memberCreatePending || record.Create.Intent == nil {
		return nil, memberCreateTemporary(nil)
	}
	op, err := kv.Get(ctx, opKey)
	if err != nil {
		return nil, memberCreateTemporary(err)
	}
	prepared, err := decodeMemberCreateRecord(op.Value())
	if err != nil || prepared.UID != uid || prepared.Create == nil || prepared.Create.State != memberCreatePending {
		return nil, memberCreateTemporary(err)
	}
	// The same CAS that commits the retained member inputs also refreshes the
	// service-derived parent fields. A delayed resume cannot overwrite a winner.
	record.CommitteeName, record.CommitteeCategory = parent.CommitteeName, parent.CommitteeCategory
	record.ProjectUID, record.ProjectSlug = parent.ProjectUID, parent.ProjectSlug
	keys := buildMemberLookupKeys(ctx, record.member())
	lock, err := kv.Get(ctx, keys.unique)
	if err != nil || string(lock.Value()) != uid {
		return nil, memberCreateTemporary(err)
	}
	var written []string
	for _, idx := range keys.indexes() {
		if _, err := reserveOwnedKey(ctx, kv, uid, idx.key); err != nil {
			return nil, memberCreateTemporary(err)
		}
		written = append(written, idx.key)
	}
	intent := *record.Create.Intent
	token := uuid.NewString()
	record.Create = &memberCreateState{State: memberCreateCommitted, Token: token}
	data, _ := json.Marshal(record)
	if _, err := kv.Update(ctx, uid, data, entry.Revision()); err != nil {
		latest, readErr := kv.Get(ctx, uid)
		if readErr != nil {
			return nil, memberCreateTemporary(readErr)
		}
		current, decodeErr := decodeMemberCreateRecord(latest.Value())
		if decodeErr != nil || current.Create == nil || current.Create.State != memberCreateCommitted {
			s.releaseStaleMemberKeys(ctx, uid, written)
			return nil, memberCreateTemporary(err)
		}
		if current.Create.Token != token {
			s.releaseStaleMemberKeys(ctx, uid, written)
			if buildMemberLookupKeys(ctx, current.member()).unique != unique {
				return nil, memberCreateTemporary(nil)
			}
			if err := s.retireMemberCreate(ctx, opKey, uid, memberCreateCommitted); err != nil {
				return nil, err
			}
			return &port.MemberCreateResult{Member: current.member()}, nil
		}
	}
	if err := s.retireMemberCreate(ctx, opKey, uid, memberCreateCommitted); err != nil {
		// This invocation already won the primary CAS. Cleanup must not suppress
		// its only normal publication opportunity; discovery retries retirement.
		slog.WarnContext(ctx, "committed member preparation cleanup remains retryable", "member_uid", uid, "error", err)
	}
	return &port.MemberCreateResult{Member: record.member(), Added: true, Intent: intent}, nil
}

// memberCreateVisible gates all member reads, including revision-only reads,
// exports and strict scans. Legacy records without lifecycle metadata are visible.
func memberCreateVisible(data []byte) (bool, error) {
	var metadata struct {
		Create *memberCreateState `json:"_create"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return false, err
	}
	if metadata.Create == nil || metadata.Create.State == memberCreateCommitted {
		return true, nil
	}
	if metadata.Create.State == memberCreatePending || metadata.Create.State == memberCreateAborted {
		return false, nil
	}
	return false, errs.NewServiceUnavailable("member creation state is unavailable")
}

func preserveMemberCreateState(ctx context.Context, kv jetstream.KeyValue, uid string, data []byte) ([]byte, error) {
	entry, err := kv.Get(ctx, uid)
	if err != nil {
		return nil, err
	}
	stored, err := decodeMemberCreateRecord(entry.Value())
	if err != nil {
		return nil, err
	}
	if stored.Create == nil {
		return data, nil
	}
	if stored.Create.State != memberCreateCommitted {
		return nil, jetstream.ErrKeyNotFound
	}
	var updated memberCreateRecord
	if err := json.Unmarshal(data, &updated); err != nil {
		return nil, err
	}
	updated.Create = stored.Create
	return json.Marshal(updated)
}

// deleteMemberRecord retains a content-free fence for recoverable members, so a
// delayed original Create cannot resurrect a member after a successful delete.
func deleteMemberRecord(ctx context.Context, kv jetstream.KeyValue, uid string, revision uint64) error {
	entry, err := kv.Get(ctx, uid)
	if err != nil {
		return err
	}
	record, decodeErr := decodeMemberCreateRecord(entry.Value())
	if decodeErr != nil || record.Create == nil {
		return kv.Delete(ctx, uid, jetstream.LastRevision(revision))
	}
	if record.Create.State != memberCreateCommitted {
		return jetstream.ErrKeyNotFound
	}
	fence := abortedMemberCreate(uid)
	fence.Create.Token = uuid.NewString()
	data, _ := json.Marshal(fence)
	if _, err := kv.Update(ctx, uid, data, revision); err != nil {
		state, _ := readRecordState(ctx, kv, uid, data)
		if state != recordMatches && !isJetStreamCASConflict(err) {
			// A landed fence survives as a key, so only this exact token/record
			// can confirm our delete after a failed or cancelled first read-back.
			rctx, cancel := detachedCtx(ctx)
			defer cancel()
			state, _ = readRecordState(rctx, kv, uid, data)
		}
		if state != recordMatches || isJetStreamCASConflict(err) {
			return err
		}
	}
	return nil
}

// memberCreateIdentity contains only hashed lookup identities and operation
// coordinates, never a member snapshot or notification intent.
type memberCreateIdentity struct {
	UID          string `json:"uid"`
	OperationKey string `json:"operation_key"`
	EmailHash    string `json:"email_hash,omitempty"`
	UsernameHash string `json:"username_hash,omitempty"`
}

func newMemberCreateIdentity(ctx context.Context, member *model.CommitteeMember) *memberCreateIdentity {
	return &memberCreateIdentity{UID: member.UID,
		OperationKey: memberCreateOperationKey(ctx, member.CommitteeUID, member.Email),
		EmailHash:    member.BuildEmailIndexKey(ctx), UsernameHash: member.BuildUsernameIndexKey(ctx)}
}

func (i *memberCreateIdentity) keys() []string {
	var keys []string
	if i.EmailHash != "" {
		keys = append(keys, fmt.Sprintf(constants.KVLookupMemberCreatesByEmailPrefix, i.EmailHash, i.UID))
	}
	if i.UsernameHash != "" {
		keys = append(keys, fmt.Sprintf(constants.KVLookupMemberCreatesByUsernamePrefix, i.UsernameHash, i.UID))
	}
	return keys
}

func (s *storage) indexMemberCreateIdentity(ctx context.Context, record *memberCreateRecord) error {
	identity := newMemberCreateIdentity(ctx, record.member())
	record.Create.Identity = identity
	data, _ := json.Marshal(identity)
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	for _, key := range identity.keys() {
		if _, err := kv.Create(ctx, key, data); err != nil {
			found, _ := readRecordState(ctx, kv, key, data)
			if found != recordMatches {
				return memberCreateTemporary(err)
			}
		}
	}
	return nil
}

func (s *storage) removeMemberCreateIdentity(ctx context.Context, identity *memberCreateIdentity) error {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	data, _ := json.Marshal(identity)
	for _, key := range identity.keys() {
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil || !bytes.Equal(entry.Value(), data) {
			return memberCreateTemporary(err)
		}
		if err := kv.Purge(ctx, key, jetstream.LastRevision(entry.Revision())); err != nil {
			if _, readErr := kv.Get(ctx, key); !errors.Is(readErr, jetstream.ErrKeyNotFound) {
				return memberCreateTemporary(err)
			}
		}
	}
	return nil
}

// bootstrapMemberCreateIdentities upgrades pre-index preparations once, not once
// per identity event. All writers must maintain indexes before this marker is used.
func (s *storage) bootstrapMemberCreateIdentities(ctx context.Context) error {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	if _, err := kv.Get(ctx, constants.KVMemberCreateIdentityIndexReady); err == nil {
		return nil
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return memberCreateTemporary(err)
	}
	watcher, err := kv.WatchAll(ctx, jetstream.MetaOnly(), jetstream.IgnoreDeletes())
	if err != nil {
		return memberCreateTemporary(err)
	}
	if err := visitMemberCreateSnapshot(ctx, watcher, func(key jetstream.KeyValueEntry) error {
		if !strings.HasPrefix(key.Key(), fmt.Sprintf(constants.KVLookupMemberCreatePrefix, "")) {
			return nil
		}
		entry, err := kv.Get(ctx, key.Key())
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return memberCreateTemporary(err)
		}
		record, err := decodeMemberCreateRecord(entry.Value())
		if err != nil {
			return memberCreateTemporary(err)
		}
		if record.Create != nil && record.Create.State == memberCreatePending {
			return s.indexMemberCreateIdentity(ctx, record)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := kv.Create(ctx, constants.KVMemberCreateIdentityIndexReady, []byte("1")); err != nil {
		found, _ := readRecordState(ctx, kv, constants.KVMemberCreateIdentityIndexReady, []byte("1"))
		if found != recordMatches {
			return memberCreateTemporary(err)
		}
	}
	return nil
}

// abortIndexedMemberCreate also fences index-before-preparation races. Stale
// indexes are removed only after the referenced UID is terminal or fenced.
func (s *storage) abortIndexedMemberCreate(ctx context.Context, identity *memberCreateIdentity) error {
	kv := s.client.kvStore[constants.KVBucketNameCommitteeMembers]
	entry, err := kv.Get(ctx, identity.OperationKey)
	if err == nil {
		record, decodeErr := decodeMemberCreateRecord(entry.Value())
		if decodeErr != nil || record.Create == nil {
			return memberCreateTemporary(decodeErr)
		}
		if record.UID == identity.UID && record.Create.State == memberCreatePending {
			if err := s.AbortMemberCreate(ctx, record.member()); err != nil {
				current, readErr := kv.Get(ctx, identity.UID)
				if readErr != nil {
					return err
				}
				committed, decodeErr := decodeMemberCreateRecord(current.Value())
				if decodeErr != nil || committed.Create == nil || committed.Create.State != memberCreateCommitted {
					return err
				}
				if err := s.retireMemberCreate(ctx, identity.OperationKey, identity.UID, memberCreateCommitted); err != nil {
					return err
				}
			}
			return s.removeMemberCreateIdentity(ctx, identity)
		}
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return memberCreateTemporary(err)
	}
	entry, err = kv.Get(ctx, identity.UID)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		fence, _ := json.Marshal(abortedMemberCreate(identity.UID))
		if _, err := kv.Create(ctx, identity.UID, fence); err != nil {
			return memberCreateTemporary(err)
		}
	case err != nil:
		return memberCreateTemporary(err)
	default:
		record, decodeErr := decodeMemberCreateRecord(entry.Value())
		if decodeErr != nil {
			return memberCreateTemporary(decodeErr)
		}
		if record.Create != nil && record.Create.State == memberCreatePending {
			if err := s.AbortMemberCreate(ctx, record.member()); err != nil {
				return err
			}
		}
	}
	return s.removeMemberCreateIdentity(ctx, identity)
}

// visitMemberCreateSnapshot requires the watcher's initial-snapshot sentinel.
// KeyLister has no error channel: a truncated listing must not mark bootstrap
// ready or acknowledge an identity cleanup that only visited part of its index.
func visitMemberCreateSnapshot(ctx context.Context, watcher jetstream.KeyWatcher, visit func(jetstream.KeyValueEntry) error) error {
	defer func() { _ = watcher.Stop() }()
	for {
		select {
		case <-ctx.Done():
			return memberCreateTemporary(ctx.Err())
		case entry, ok := <-watcher.Updates():
			if !ok {
				return memberCreateTemporary(nil)
			}
			if entry == nil {
				return ctx.Err()
			}
			if err := visit(entry); err != nil {
				return err
			}
		}
	}
}
