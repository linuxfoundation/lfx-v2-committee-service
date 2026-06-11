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
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"

	"github.com/nats-io/nats.go/jetstream"
)

type storage struct {
	client *NATSClient
}

// Create persists a new committee and its optional settings in the NATS JetStream KV store.
func (s *storage) Create(ctx context.Context, committee *model.Committee) error {

	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}

	committeeBaseBytes, errMarshal := json.Marshal(committee.CommitteeBase)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee base", errMarshal)
	}

	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, committee.CommitteeBase.UID, committeeBaseBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create committee", errCreate)
	}

	slog.DebugContext(ctx, "created committee in NATS storage",
		"committee_uid", committee.CommitteeBase.UID,
		"revision", rev,
	)

	// Create settings if they exist
	if committee.CommitteeSettings != nil {
		committee.CommitteeSettings.UID = committee.CommitteeBase.UID
		settingsBytes, errMarshalSettings := json.Marshal(committee.CommitteeSettings)
		if errMarshalSettings != nil {
			return errs.NewUnexpected("failed to marshal committee settings", errMarshalSettings)
		}

		rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeSettings].Create(ctx, committee.CommitteeBase.UID, settingsBytes)
		if errCreate != nil {
			return errs.NewUnexpected("failed to create committee settings", errCreate)
		}

		slog.DebugContext(ctx, "created committee settings in NATS storage",
			"committee_uid", committee.CommitteeBase.UID,
			"revision", rev,
		)
	}

	return nil
}

// UniqueNameProject enforces a uniqueness constraint on the committee name within a project
// by creating a lookup key in the KV store. It returns the lookup key and a conflict error
// if a committee with the same name already exists for the project.
func (s *storage) UniqueNameProject(ctx context.Context, committee *model.Committee) (string, error) {

	uniqueKey := fmt.Sprintf(constants.KVLookupPrefix, committee.BuildIndexKey(ctx))
	_, errUnique := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, uniqueKey, []byte(committee.CommitteeBase.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return uniqueKey, errs.NewConflict("committee with the same name for the project already exists")
		}
		return uniqueKey, errs.NewUnexpected("failed to create unique key for committee", errUnique)
	}
	return uniqueKey, nil
}

// UniqueSSOGroupName enforces a uniqueness constraint on the committee's SSO group name
// by creating a lookup key in the KV store. It returns the lookup key and a conflict error
// if a committee with the same SSO group name already exists.
func (s *storage) UniqueSSOGroupName(ctx context.Context, committee *model.Committee) (string, error) {

	ssoGroupKey := fmt.Sprintf(constants.KVLookupSSOGroupNamePrefix, committee.SSOGroupName)
	_, errSSO := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, ssoGroupKey, []byte(committee.CommitteeBase.UID))
	if errSSO != nil {
		if errors.Is(errSSO, jetstream.ErrKeyExists) {
			return ssoGroupKey, errs.NewConflict("committee with the same SSO group name already exists")
		}
		return ssoGroupKey, errs.NewUnexpected("failed to create unique key for SSO group name", errSSO)
	}
	return ssoGroupKey, nil
}

// get retrieves a model from the NATS KV store by bucket and UID.
// It unmarshals the data into the provided model and returns the revision.
// If the UID is empty, it returns a validation error.
// It can be used for any that has the similar need for fetching data by UID.
func (s *storage) get(ctx context.Context, bucket, uid string, model any, onlyRevision bool) (uint64, error) {

	if uid == "" {
		return 0, errs.NewValidation("committee UID cannot be empty")
	}

	data, errGet := s.client.kvStore[bucket].Get(ctx, uid)
	if errGet != nil {
		return 0, errGet
	}

	if !onlyRevision {
		errUnmarshal := json.Unmarshal(data.Value(), &model)
		if errUnmarshal != nil {
			return 0, errUnmarshal
		}
	}

	return data.Revision(), nil

}

// GetBase retrieves a committee's base data and its current revision from the KV store by UID.
func (s *storage) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {

	committee := &model.CommitteeBase{}

	rev, errGet := s.get(ctx, constants.KVBucketNameCommittees, uid, committee, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
		}
		return nil, 0, errs.NewUnexpected("failed to get committee", errGet)
	}

	return committee, rev, nil
}

// GetRevision retrieves only the current revision number for a committee without unmarshaling its data.
func (s *storage) GetRevision(ctx context.Context, uid string) (uint64, error) {
	return s.get(ctx, constants.KVBucketNameCommittees, uid, &model.CommitteeBase{}, true)
}

// ListAllUIDs returns all active committee UIDs from the KV store, excluding secondary index keys.
func (s *storage) ListAllUIDs(ctx context.Context) ([]string, error) {
	keys, err := s.client.kvStore[constants.KVBucketNameCommittees].ListKeys(ctx)
	if err != nil {
		return nil, errs.NewUnexpected("failed to list keys from committees bucket", err)
	}

	var uids []string
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") || strings.HasPrefix(key, constants.KVSlugPrefix) {
			continue
		}
		uids = append(uids, key)
	}

	return uids, nil
}

// GetSettings retrieves a committee's settings and its current revision from the KV store by committee UID.
func (s *storage) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {

	settings := &model.CommitteeSettings{}

	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeSettings, uid, settings, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("committee settings not found", fmt.Errorf("committee UID: %s", uid))
		}
		return nil, 0, errs.NewUnexpected("failed to get committee settings", errGet)
	}

	return settings, rev, nil
}

// UpdateBase updates a committee's base data in the KV store using optimistic locking via the provided revision.
func (s *storage) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {

	// Marshal the committee base data
	committeeBaseBytes, errMarshal := json.Marshal(committee.CommitteeBase)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee base", errMarshal)
	}

	// Update the committee base using optimistic locking (revision check)
	newRevision, errUpdate := s.client.kvStore[constants.KVBucketNameCommittees].Update(ctx, committee.CommitteeBase.UID, committeeBaseBytes, revision)
	if errUpdate != nil {
		if errors.Is(errUpdate, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", committee.CommitteeBase.UID))
		}
		return errs.NewUnexpected("failed to update committee base", errUpdate)
	}

	slog.DebugContext(ctx, "updated committee base in NATS storage",
		"committee_uid", committee.CommitteeBase.UID,
		"old_revision", revision,
		"new_revision", newRevision,
	)

	return nil
}

// UpdateHasMailingList implements CommitteeBaseWriter.
func (s *storage) UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error) {
	committee := &model.CommitteeBase{}
	rev, err := s.get(ctx, constants.KVBucketNameCommittees, uid, committee, false)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, false, errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
		}
		return nil, false, errs.NewUnexpected("failed to get committee", err)
	}

	if committee.HasMailingList == hasMailingList {
		return nil, false, nil
	}

	committee.HasMailingList = hasMailingList
	committee.UpdatedAt = time.Now()

	data, err := json.Marshal(committee)
	if err != nil {
		return nil, false, errs.NewUnexpected("failed to marshal committee", err)
	}

	newRevision, err := s.client.kvStore[constants.KVBucketNameCommittees].Update(ctx, uid, data, rev)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, false, errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
		}
		return nil, false, errs.NewUnexpected("failed to update committee has_mailing_list flag", err)
	}

	slog.DebugContext(ctx, "updated has_mailing_list in NATS storage",
		"committee_uid", uid,
		"has_mailing_list", hasMailingList,
		"old_revision", rev,
		"new_revision", newRevision,
	)

	return committee, true, nil
}

// UpdateSetting updates a committee's settings in the KV store using optimistic locking via the provided revision.
func (s *storage) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {

	// Marshal the committee settings data
	settingsBytes, errMarshal := json.Marshal(settings)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee settings", errMarshal)
	}

	// Update the committee settings using optimistic locking (revision check)
	newRevision, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeSettings].Update(ctx, settings.UID, settingsBytes, revision)
	if errUpdate != nil {
		if errors.Is(errUpdate, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("committee settings not found", fmt.Errorf("committee UID: %s", settings.UID))
		}
		return errs.NewUnexpected("failed to update committee settings", errUpdate)
	}

	slog.DebugContext(ctx, "updated committee settings in NATS storage",
		"committee_uid", settings.UID,
		"old_revision", revision,
		"new_revision", newRevision,
	)

	return nil
}

// Delete removes a committee's base data and associated settings from the KV store.
// It uses optimistic locking for the base record and silently ignores missing settings.
func (s *storage) Delete(ctx context.Context, uid string, revision uint64) error {

	// Delete committee base
	errDeleteBase := s.client.kvStore[constants.KVBucketNameCommittees].Delete(ctx, uid, jetstream.LastRevision(revision))
	if errDeleteBase != nil {
		if errors.Is(errDeleteBase, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
		}
		return errs.NewUnexpected("failed to delete committee base", errDeleteBase)
	}

	// Delete committee settings if they exist
	errDeleteSettings := s.client.kvStore[constants.KVBucketNameCommitteeSettings].Delete(ctx, uid)
	if errDeleteSettings != nil {
		if errors.Is(errDeleteSettings, jetstream.ErrKeyNotFound) {
			slog.WarnContext(ctx, "committee settings not found for deletion", "committee_uid", uid)
			return nil // Settings not found is not an error
		}
		return errs.NewUnexpected("failed to delete committee settings", errDeleteSettings)
	}

	return nil
}

// ================== CommitteeMemberReader implementation ==================

// GetMember retrieves a committee member by member UID
func (s *storage) GetMember(ctx context.Context, memberUID string) (*model.CommitteeMember, uint64, error) {

	member := &model.CommitteeMember{}

	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeMembers, memberUID, member, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("committee member not found", fmt.Errorf("member UID: %s", memberUID))
		}
		return nil, 0, errs.NewUnexpected("failed to get committee member", errGet)
	}

	return member, rev, nil
}

// ListMembersByCommittee retrieves all members for a given committee UID using the secondary index.
// It performs a server-side filtered scan of keys matching
// "lookup/committee-members-by-committee/<committeeUID>.*" so only members of the target
// committee are fetched, rather than scanning the entire bucket.
func (s *storage) ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {
	if committeeUID == "" {
		return nil, errs.NewValidation("committee UID cannot be empty")
	}

	slog.DebugContext(ctx, "listing committee members from NATS storage", "committee_uid", committeeUID)

	filter := fmt.Sprintf(constants.KVLookupMembersByCommitteeFilter, committeeUID)
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeMembers].ListKeysFiltered(ctx, filter)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list member index keys for committee", errKeys)
	}

	var members []*model.CommitteeMember

	// Each key is "lookup/committee-members-by-committee/<committeeUID>.<memberUID>".
	// Extract the member UID from the suffix after the last dot.
	for key := range keys.Keys() {
		dotIdx := strings.LastIndex(key, ".")
		if dotIdx < 0 || dotIdx == len(key)-1 {
			slog.WarnContext(ctx, "skipping malformed member index key",
				"key", key,
				"committee_uid", committeeUID,
			)
			continue
		}
		// UIDs are UUIDs (RFC 4122 hex + hyphens only) and never contain dots,
		// so LastIndex is safe as the committee/member separator.
		memberUID := key[dotIdx+1:]

		member := &model.CommitteeMember{}
		_, errGet := s.get(ctx, constants.KVBucketNameCommitteeMembers, memberUID, member, false)
		if errGet != nil {
			slog.WarnContext(ctx, "failed to get member while listing by committee",
				"member_uid", memberUID,
				"error", errGet,
				"committee_uid", committeeUID,
			)
			continue
		}

		members = append(members, member)
	}

	slog.DebugContext(ctx, "retrieved committee members from NATS storage",
		"committee_uid", committeeUID,
		"member_count", len(members),
	)

	return members, nil
}

// ListAllMembers retrieves every member across all committees via a full bucket scan.
// It is intended only for backfill/repair operations (e.g. the members-by-committee-index
// CLI subcommand) that need to read all members without relying on the secondary index.
func (s *storage) ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "listing all committee members from NATS storage")

	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeMembers].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee members bucket", errKeys)
	}

	var members []*model.CommitteeMember

	for key := range keys.Keys() {
		// Skip all secondary-index keys.
		if strings.HasPrefix(key, "lookup/") {
			continue
		}

		member := &model.CommitteeMember{}
		_, errGet := s.get(ctx, constants.KVBucketNameCommitteeMembers, key, member, false)
		if errGet != nil {
			slog.WarnContext(ctx, "failed to get member while listing all",
				"key", key,
				"error", errGet,
			)
			continue
		}

		members = append(members, member)
	}

	slog.DebugContext(ctx, "retrieved all committee members from NATS storage",
		"member_count", len(members),
	)

	return members, nil
}

// EachMember streams every committee member via a full bucket scan, invoking fn per member without
// accumulating the whole set in memory (backfill/repair). Secondary-index keys are skipped; a
// per-member read error is logged and skipped; the first error fn returns stops iteration.
func (s *storage) EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeMembers].ListKeys(ctx)
	if errKeys != nil {
		return errs.NewUnexpected("failed to list keys from committee members bucket", errKeys)
	}
	defer func() { _ = keys.Stop() }()

	for key := range keys.Keys() {
		// Skip all secondary-index keys.
		if strings.HasPrefix(key, "lookup/") {
			continue
		}

		member := &model.CommitteeMember{}
		if _, errGet := s.get(ctx, constants.KVBucketNameCommitteeMembers, key, member, false); errGet != nil {
			slog.WarnContext(ctx, "failed to get member while streaming all", "key", key, "error", errGet)
			continue
		}

		if err := fn(member); err != nil {
			return err
		}
	}
	return nil
}

// GetMemberRevision retrieves the revision number for a committee member
func (s *storage) GetMemberRevision(ctx context.Context, memberUID string) (uint64, error) {
	rev, err := s.get(ctx, constants.KVBucketNameCommitteeMembers, memberUID, &model.CommitteeMember{}, true)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return 0, errs.NewNotFound("committee member not found", fmt.Errorf("member UID: %s", memberUID))
		}
		// Return validation errors (e.g. empty UID) and other typed errors as-is
		// so callers receive the correct error kind instead of an unexpected wrapper.
		var valErr errs.Validation
		if errors.As(err, &valErr) {
			return 0, err
		}
		return 0, errs.NewUnexpected("failed to get committee member revision", err)
	}
	return rev, nil
}

// ================== CommitteeMemberWriter implementation ==================

// CreateMember creates a new committee member
func (s *storage) CreateMember(ctx context.Context, member *model.CommitteeMember) error {

	if member == nil {
		return errs.NewValidation("committee member cannot be nil")
	}

	memberBytes, errMarshal := json.Marshal(member)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee member", errMarshal)
	}

	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeMembers].Create(ctx, member.UID, memberBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create committee member", errCreate)
	}

	slog.DebugContext(ctx, "created committee member in NATS storage",
		"committee_uid", member.CommitteeUID,
		"member_uid", member.UID,
		"revision", rev,
	)

	return nil
}

// UpdateMember updates an existing committee member
func (s *storage) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {

	if member == nil {
		return nil, errs.NewValidation("committee member cannot be nil")
	}

	// Marshal the committee member data
	memberBytes, errMarshal := json.Marshal(member)
	if errMarshal != nil {
		return nil, errs.NewUnexpected("failed to marshal committee member", errMarshal)
	}

	// Update the committee member using optimistic locking (revision check)
	newRevision, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeMembers].Update(ctx, member.UID, memberBytes, revision)
	if errUpdate != nil {
		if errors.Is(errUpdate, jetstream.ErrKeyNotFound) {
			return nil, errs.NewNotFound("committee member not found", fmt.Errorf("member UID: %s", member.UID))
		}
		return nil, errs.NewUnexpected("failed to update committee member", errUpdate)
	}

	slog.DebugContext(ctx, "updated committee member in NATS storage",
		"committee_uid", member.CommitteeUID,
		"member_uid", member.UID,
		"old_revision", revision,
		"new_revision", newRevision,
	)

	return member, nil
}

// DeleteMember removes a committee member
func (s *storage) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	slog.DebugContext(ctx, "deleting committee member from storage",
		"member_uid", uid,
		"revision", revision,
	)

	// Delete the member record with optimistic locking
	err := s.client.kvStore[constants.KVBucketNameCommitteeMembers].Delete(ctx, uid, jetstream.LastRevision(revision))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			slog.WarnContext(ctx, "committee member not found for deletion",
				"member_uid", uid,
				"revision", revision,
			)
			return errs.NewNotFound("committee member not found")
		}
		slog.ErrorContext(ctx, "failed to delete committee member from storage",
			"error", err,
			"member_uid", uid,
			"revision", revision,
		)
		return errs.NewUnexpected("failed to delete committee member", err)
	}

	slog.DebugContext(ctx, "committee member deleted successfully from storage",
		"member_uid", uid,
	)

	return nil
}

// UniqueMember verifies if a member with the same email exists in the committee
// It stores the member UID in the KV store with the index key as the value as secondary index
// to ensure that the member is unique, avoiding concurrent operations for the same member.
func (s *storage) UniqueMember(ctx context.Context, member *model.CommitteeMember) (string, error) {
	uniqueKey := fmt.Sprintf(constants.KVLookupMemberPrefix, member.BuildIndexKey(ctx))
	_, errUnique := s.client.kvStore[constants.KVBucketNameCommitteeMembers].Create(ctx, uniqueKey, []byte(member.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return uniqueKey, errs.NewConflict("member with the same email already exists in the committee")
		}
		return uniqueKey, errs.NewUnexpected("failed to create unique key for member", errUnique)
	}
	return uniqueKey, nil
}

// IndexMemberByCommittee writes the secondary index entry
// "lookup/committee-members-by-committee/<committee_uid>.<member_uid>" → <member_uid>
// into the committee-members bucket. This enables ListMembersByCommittee to use a server-side
// filtered scan instead of a full bucket scan.
// Returns the written key (for rollback tracking) and nil on success.
// jetstream.ErrKeyExists is treated as idempotent — the entry already exists, which is fine.
func (s *storage) IndexMemberByCommittee(ctx context.Context, member *model.CommitteeMember) (string, error) {
	if member == nil {
		return "", errs.NewValidation("committee member cannot be nil")
	}
	if member.CommitteeUID == "" || member.UID == "" {
		return "", errs.NewValidation("committee member CommitteeUID and UID must be non-empty")
	}
	key := fmt.Sprintf(constants.KVLookupMembersByCommitteePrefix, member.CommitteeUID, member.UID)
	if _, err := s.client.kvStore[constants.KVBucketNameCommitteeMembers].Create(ctx, key, []byte(member.UID)); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			// Already present — idempotent; treat as success.
			return key, nil
		}
		return key, errs.NewUnexpected("failed to index member by committee", err)
	}
	return key, nil
}

// IndexMemberByOrganization writes the secondary index entry
// "lookup/committee-members-by-organization/<org_sfid>.<member_uid>" → <member_uid> into the
// committee-members bucket, so ListMembersByOrganization (Org Lens, LFXV2-1865) can use a server-side
// filtered scan instead of a full bucket scan. The org SFID is normalized to its 18-char form.
//
// Members without an organization.id are NOT org-affiliated seats, so no index entry is written and an
// empty key is returned (the caller treats that as a no-op). jetstream.ErrKeyExists is idempotent.
func (s *storage) IndexMemberByOrganization(ctx context.Context, member *model.CommitteeMember) (string, error) {
	if member == nil {
		return "", errs.NewValidation("committee member cannot be nil")
	}
	if member.UID == "" {
		return "", errs.NewValidation("committee member UID must be non-empty")
	}
	orgSFID := utils.NormalizeAccountSFID(member.Organization.ID)
	if orgSFID == "" {
		// No organization affiliation → nothing to index. Not an error.
		return "", nil
	}
	key := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, orgSFID, member.UID)
	if _, err := s.client.kvStore[constants.KVBucketNameCommitteeMembers].Create(ctx, key, []byte(member.UID)); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return key, nil
		}
		return key, errs.NewUnexpected("failed to index member by organization", err)
	}
	return key, nil
}

// ListMembersByOrganization retrieves all committee members held by an organization (by the SFID on
// committee_member.organization.id) using the by-organization secondary index. It performs a
// server-side filtered scan of "lookup/committee-members-by-organization/<org_sfid>.*" so only the
// org's members are fetched. The org SFID is normalized to its 18-char form before scanning.
func (s *storage) ListMembersByOrganization(ctx context.Context, orgSFID string) ([]*model.CommitteeMember, error) {
	orgSFID = utils.NormalizeAccountSFID(orgSFID)
	if orgSFID == "" {
		return nil, errs.NewValidation("organization SFID cannot be empty")
	}

	slog.DebugContext(ctx, "listing committee members by organization from NATS storage", "org_sfid", orgSFID)

	filter := fmt.Sprintf(constants.KVLookupMembersByOrganizationFilter, orgSFID)
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeMembers].ListKeysFiltered(ctx, filter)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list member index keys for organization", errKeys)
	}
	defer func() { _ = keys.Stop() }()

	var members []*model.CommitteeMember

	// Each key is "lookup/committee-members-by-organization/<org_sfid>.<member_uid>".
	// The member UID (a UUID, no dots) is the suffix after the last dot.
	for key := range keys.Keys() {
		dotIdx := strings.LastIndex(key, ".")
		if dotIdx < 0 || dotIdx == len(key)-1 {
			slog.WarnContext(ctx, "skipping malformed org member index key", "key", key, "org_sfid", orgSFID)
			continue
		}
		memberUID := key[dotIdx+1:]

		member := &model.CommitteeMember{}
		_, errGet := s.get(ctx, constants.KVBucketNameCommitteeMembers, memberUID, member, false)
		if errGet != nil {
			slog.WarnContext(ctx, "failed to get member while listing by organization",
				"member_uid", memberUID, "error", errGet, "org_sfid", orgSFID)
			continue
		}

		// Defensive consistency check: the secondary index is a hint, the member record is the source
		// of truth. The org-change stale-key cleanup runs in a background goroutine, so a lagging or
		// failed cleanup can leave an index key pointing at a member whose organization.id has since
		// changed. Skip such entries so a stale key never leaks a seat into another org's list.
		if memberOrg := utils.NormalizeAccountSFID(member.Organization.ID); memberOrg != orgSFID {
			slog.WarnContext(ctx, "skipping stale org member index entry; member org no longer matches",
				"member_uid", memberUID, "index_org_sfid", orgSFID, "member_org_sfid", memberOrg)
			continue
		}

		members = append(members, member)
	}

	slog.DebugContext(ctx, "retrieved committee members by organization from NATS storage",
		"org_sfid", orgSFID, "member_count", len(members))

	return members, nil
}

// ================== CommitteeInviteReader implementation ==================

// GetInvite retrieves a committee invite by invite UID
func (s *storage) GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error) {

	invite := &model.CommitteeInvite{}

	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeInvites, uid, invite, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("committee invite not found", fmt.Errorf("invite UID: %s", uid))
		}
		return nil, 0, errs.NewUnexpected("failed to get committee invite", errGet)
	}

	return invite, rev, nil
}

// ListInvites retrieves all invites for a given committee UID
func (s *storage) ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	slog.DebugContext(ctx, "listing committee invites from NATS storage", "committee_uid", committeeUID)

	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeInvites].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee invites bucket", errKeys)
	}

	var invites []*model.CommitteeInvite

	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}

		invite := &model.CommitteeInvite{}
		_, errGet := s.get(ctx, constants.KVBucketNameCommitteeInvites, key, invite, false)
		if errGet != nil {
			slog.WarnContext(ctx, "failed to get invite while listing",
				"key", key,
				"error", errGet,
				"committee_uid", committeeUID)
			continue
		}

		if invite.CommitteeUID == committeeUID {
			invites = append(invites, invite)
		}
	}

	slog.DebugContext(ctx, "retrieved committee invites from NATS storage",
		"committee_uid", committeeUID,
		"invite_count", len(invites),
	)

	return invites, nil
}

// ================== CommitteeInviteWriter implementation ==================

// CreateInvite creates a new committee invite
func (s *storage) CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error {

	if invite == nil {
		return errs.NewValidation("committee invite cannot be nil")
	}

	inviteBytes, errMarshal := json.Marshal(invite)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee invite", errMarshal)
	}

	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeInvites].Create(ctx, invite.UID, inviteBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create committee invite", errCreate)
	}

	slog.DebugContext(ctx, "created committee invite in NATS storage",
		"committee_uid", invite.CommitteeUID,
		"invite_uid", invite.UID,
		"revision", rev,
	)

	return nil
}

// UpdateInvite updates an existing committee invite
func (s *storage) UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error {

	if invite == nil {
		return errs.NewValidation("committee invite cannot be nil")
	}

	inviteBytes, errMarshal := json.Marshal(invite)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee invite", errMarshal)
	}

	newRevision, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeInvites].Update(ctx, invite.UID, inviteBytes, revision)
	if errUpdate != nil {
		if errors.Is(errUpdate, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("committee invite not found", fmt.Errorf("invite UID: %s", invite.UID))
		}
		return errs.NewUnexpected("failed to update committee invite", errUpdate)
	}

	slog.DebugContext(ctx, "updated committee invite in NATS storage",
		"invite_uid", invite.UID,
		"old_revision", revision,
		"new_revision", newRevision,
	)

	return nil
}

// UniqueInvite verifies if an invite with the same email exists for the committee
func (s *storage) UniqueInvite(ctx context.Context, invite *model.CommitteeInvite) (string, error) {
	uniqueKey := fmt.Sprintf(constants.KVLookupInvitePrefix, invite.BuildIndexKey(ctx))
	_, errUnique := s.client.kvStore[constants.KVBucketNameCommitteeInvites].Create(ctx, uniqueKey, []byte(invite.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return uniqueKey, errs.NewConflict("invite for the same email already exists in the committee")
		}
		return uniqueKey, errs.NewUnexpected("failed to create unique key for invite", errUnique)
	}
	return uniqueKey, nil
}

// ================== CommitteeApplicationReader implementation ==================

// GetApplication retrieves a committee application by application UID
func (s *storage) GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error) {

	application := &model.CommitteeApplication{}

	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeApplications, uid, application, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("committee application not found", fmt.Errorf("application UID: %s", uid))
		}
		return nil, 0, errs.NewUnexpected("failed to get committee application", errGet)
	}

	return application, rev, nil
}

// ListApplications retrieves all applications for a given committee UID
func (s *storage) ListApplications(ctx context.Context, committeeUID string) ([]*model.CommitteeApplication, error) {
	slog.DebugContext(ctx, "listing committee applications from NATS storage", "committee_uid", committeeUID)

	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeApplications].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee applications bucket", errKeys)
	}

	var applications []*model.CommitteeApplication

	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}

		application := &model.CommitteeApplication{}
		_, errGet := s.get(ctx, constants.KVBucketNameCommitteeApplications, key, application, false)
		if errGet != nil {
			slog.WarnContext(ctx, "failed to get application while listing",
				"key", key,
				"error", errGet,
				"committee_uid", committeeUID)
			continue
		}

		if application.CommitteeUID == committeeUID {
			applications = append(applications, application)
		}
	}

	slog.DebugContext(ctx, "retrieved committee applications from NATS storage",
		"committee_uid", committeeUID,
		"application_count", len(applications),
	)

	return applications, nil
}

// ================== CommitteeApplicationWriter implementation ==================

// CreateApplication creates a new committee application
func (s *storage) CreateApplication(ctx context.Context, application *model.CommitteeApplication) error {

	if application == nil {
		return errs.NewValidation("committee application cannot be nil")
	}

	applicationBytes, errMarshal := json.Marshal(application)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee application", errMarshal)
	}

	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeApplications].Create(ctx, application.UID, applicationBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create committee application", errCreate)
	}

	slog.DebugContext(ctx, "created committee application in NATS storage",
		"committee_uid", application.CommitteeUID,
		"application_uid", application.UID,
		"revision", rev,
	)

	return nil
}

// UpdateApplication updates an existing committee application
func (s *storage) UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error {

	if application == nil {
		return errs.NewValidation("committee application cannot be nil")
	}

	applicationBytes, errMarshal := json.Marshal(application)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee application", errMarshal)
	}

	newRevision, errUpdate := s.client.kvStore[constants.KVBucketNameCommitteeApplications].Update(ctx, application.UID, applicationBytes, revision)
	if errUpdate != nil {
		if errors.Is(errUpdate, jetstream.ErrKeyNotFound) {
			return errs.NewNotFound("committee application not found", fmt.Errorf("application UID: %s", application.UID))
		}
		return errs.NewUnexpected("failed to update committee application", errUpdate)
	}

	slog.DebugContext(ctx, "updated committee application in NATS storage",
		"application_uid", application.UID,
		"old_revision", revision,
		"new_revision", newRevision,
	)

	return nil
}

// UniqueApplication verifies if an application from the same user exists for the committee
func (s *storage) UniqueApplication(ctx context.Context, application *model.CommitteeApplication) (string, error) {
	uniqueKey := fmt.Sprintf(constants.KVLookupApplicationPrefix, application.BuildIndexKey(ctx))
	_, errUnique := s.client.kvStore[constants.KVBucketNameCommitteeApplications].Create(ctx, uniqueKey, []byte(application.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return uniqueKey, errs.NewConflict("application from the same user already exists in the committee")
		}
		return uniqueKey, errs.NewUnexpected("failed to create unique key for application", errUnique)
	}
	return uniqueKey, nil
}

// IsReady checks whether the underlying NATS client connection is healthy and ready to serve requests.
func (s *storage) IsReady(ctx context.Context) error {
	return s.client.IsReady(ctx)
}

// NewStorage creates a new NATS JetStream KV-backed storage that implements the CommitteeReaderWriter port.
func NewStorage(client *NATSClient) port.CommitteeReaderWriter {
	return &storage{
		client: client,
	}
}
