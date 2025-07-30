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

// checkUniqueness is responsible for ensuring that the committee and SSO group names are unique.
// To ensure uniqueness for the committee and sso group name, we create secondary keys
// in the KV store. This is done to prevent duplicate committees with the same index key
// or SSO group name.
func (s *storage) checkUniqueness(ctx context.Context, committee *model.Committee) (map[string][]string, error) {

	// all the keys that will be created
	// will be stored in this map, so we can rollback if needed
	keyMapping := make(map[string][]string)

	uniqueKey := fmt.Sprintf("%s/committees/%s", constants.KVLookupPrefix, committee.BuildIndexKey(ctx))
	_, errUnique := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, uniqueKey, []byte(committee.CommitteeBase.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return keyMapping, errs.NewConflict("committee with the same index key already exists")
		}
		return keyMapping, errs.NewUnexpected("failed to create unique key for committee", errUnique)
	}
	keyMapping[constants.KVBucketNameCommittees] = append(keyMapping[constants.KVBucketNameCommittees], uniqueKey)

	if committee.SSOGroupName != "" {
		ssoGroupKey := fmt.Sprintf("%s/committee-sso-groups/%s", constants.KVLookupPrefix, committee.SSOGroupName)
		_, errSSO := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, ssoGroupKey, []byte(committee.CommitteeBase.UID))
		if errSSO != nil {
			if errors.Is(errSSO, jetstream.ErrKeyExists) {
				return keyMapping, errs.NewConflict("committee with the same SSO group name already exists")
			}
			return keyMapping, errs.NewUnexpected("failed to create unique key for SSO group name", errSSO)
		}
		keyMapping[constants.KVBucketNameCommittees] = append(keyMapping[constants.KVBucketNameCommittees], ssoGroupKey)
	}

	return keyMapping, nil
}

func (s *storage) Create(ctx context.Context, committee *model.Committee) error {

	var (
		keyMapping map[string][]string
		rollback   bool
	)

	defer func() {
		// validate atomicity
		if rollback {
			for bucket, keys := range keyMapping {
				for _, key := range keys {
					if err := s.client.kvStore[bucket].Delete(ctx, key); err != nil {
						slog.ErrorContext(ctx, "error rolling back committee creation",
							"key", key,
							"error", err,
						)
					}
				}
				slog.ErrorContext(ctx, "rolled back committee creation due to error",
					"committee_uid", committee.CommitteeBase.UID,
					"bucket", bucket,
				)
			}

		}
	}()

	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}

	keyMapping, errCheck := s.checkUniqueness(ctx, committee)
	if errCheck != nil {
		rollback = true
		return errCheck
	}

	committee.CommitteeBase.UID = uid.New()
	committeeBaseBytes, errMarshal := json.Marshal(committee.CommitteeBase)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal committee base", errMarshal)
	}

	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, committee.CommitteeBase.UID, committeeBaseBytes)
	if errCreate != nil {
		rollback = true
		return errs.NewUnexpected("failed to create committee", errCreate)
	}
	keyMapping[constants.KVBucketNameCommittees] = append(keyMapping[constants.KVBucketNameCommittees], committee.CommitteeBase.UID)

	slog.DebugContext(ctx, "created committee in NATS storage",
		"committee_uid", committee.CommitteeBase.UID,
		"revision", rev,
	)

	// Create settings if they exist
	if committee.CommitteeSettings != nil {
		committee.CommitteeSettings.CommitteeUID = committee.CommitteeBase.UID
		settingsBytes, errMarshalSettings := json.Marshal(committee.CommitteeSettings)
		if errMarshalSettings != nil {
			rollback = true
			return errs.NewUnexpected("failed to marshal committee settings", errMarshalSettings)
		}
		rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeSettings].Create(ctx, committee.CommitteeBase.UID, settingsBytes)
		if errCreate != nil {
			rollback = true
			return errs.NewUnexpected("failed to create committee settings", errCreate)
		}
		keyMapping[constants.KVBucketNameCommitteeSettings] = append(keyMapping[constants.KVBucketNameCommitteeSettings], committee.CommitteeBase.UID)

		slog.DebugContext(ctx, "created committee settings in NATS storage",
			"committee_uid", committee.CommitteeBase.UID,
			"revision", rev,
		)
	}

	return nil
}

func (s *storage) GetBase(ctx context.Context, uid string) (*model.Committee, error) {
	return nil, nil
}

func (s *storage) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, error) {
	return nil, nil
}

func (s *storage) ByNameProject(ctx context.Context, nameProjectKey string) (*model.Committee, error) {
	return nil, nil
}

func (s *storage) BySSOGroupName(ctx context.Context, name string) (*model.Committee, error) {
	return nil, nil
}

func (s *storage) UpdateBase(ctx context.Context, committee *model.Committee) error {
	return nil
}

func (s *storage) UpdateSetting(ctx context.Context, committee *model.CommitteeSettings) error {
	return nil
}

func (s *storage) Delete(ctx context.Context, uid string) error {
	return nil
}

func NewStorage(client *NATSClient) port.CommitteeReaderWriter {
	return &storage{
		client: client,
	}
}
