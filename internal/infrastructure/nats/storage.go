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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/uid"

	"github.com/nats-io/nats.go/jetstream"
)

type storage struct {
	client *NATSClient
}

func (s *storage) Create(ctx context.Context, committee *model.Committee) error {

	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}

	committee.CommitteeBase.UID = uid.New()
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

	uniqueKey := fmt.Sprintf("%s/committees/%s", constants.KVLookupPrefix, committee.BuildIndexKey(ctx))
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

	ssoGroupKey := fmt.Sprintf("%s/committee-sso-groups/%s", constants.KVLookupPrefix, committee.SSOGroupName)
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
func (s *storage) get(ctx context.Context, bucket, uid string, model any) (uint64, error) {

	if uid == "" {
		return 0, errs.NewValidation("committee UID cannot be empty")
	}

	data, errGet := s.client.kvStore[bucket].Get(ctx, uid)
	if errGet != nil {
		return 0, errGet
	}

	errUnmarshal := json.Unmarshal(data.Value(), &model)
	if errUnmarshal != nil {
		return 0, errUnmarshal
	}

	return data.Revision(), nil

}

func (s *storage) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {

	committee := &model.CommitteeBase{}

	rev, errGet := s.get(ctx, constants.KVBucketNameCommittees, uid, committee)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
		}
		return nil, 0, errs.NewUnexpected("failed to get committee", errGet)
	}

	return committee, rev, nil
}

func (s *storage) GetRevision(ctx context.Context, uid string) (uint64, error) {
	return s.get(ctx, constants.KVBucketNameCommittees, uid, &model.CommitteeBase{})
}

func (s *storage) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	return nil, 0, nil
}

func (s *storage) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	return nil
}

func (s *storage) UpdateSetting(ctx context.Context, committee *model.CommitteeSettings, revision uint64) error {
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

func NewStorage(client *NATSClient) port.CommitteeReaderWriter {
	return &storage{
		client: client,
	}
}
