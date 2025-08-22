// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go/jetstream"
)

type storage struct {
	client *NATSClient
}

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

func (s *storage) GetRevision(ctx context.Context, uid string) (uint64, error) {
	return s.get(ctx, constants.KVBucketNameCommittees, uid, &model.CommitteeBase{}, true)
}

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

// GetMemberRevision retrieves the revision number for a committee member
func (s *storage) GetMemberRevision(ctx context.Context, memberUID string) (uint64, error) {
	return s.get(ctx, constants.KVBucketNameCommitteeMembers, memberUID, &model.CommitteeMember{}, true)
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
func (s *storage) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) error {
	return errs.NewUnexpected("committee member update not yet implemented")
}

// DeleteMember removes a committee member
func (s *storage) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	return errs.NewUnexpected("committee member deletion not yet implemented")
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

func (s *storage) IsReady(ctx context.Context) error {
	return s.client.IsReady(ctx)
}

func NewStorage(client *NATSClient) port.CommitteeReaderWriter {
	return &storage{
		client: client,
	}
}
