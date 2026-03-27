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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go/jetstream"
)

func (s *storage) CreateLink(ctx context.Context, link *model.CommitteeLink) error {
	if link == nil {
		return errs.NewValidation("link cannot be nil")
	}
	linkBytes, errMarshal := json.Marshal(link)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal link", errMarshal)
	}
	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeLinks].Create(ctx, link.UID, linkBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create link", errCreate)
	}
	slog.DebugContext(ctx, "created link in NATS storage", "link_uid", link.UID, "committee_uid", link.CommitteeUID, "revision", rev)
	return nil
}

func (s *storage) GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error) {
	link := &model.CommitteeLink{}
	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeLinks, linkUID, link, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("link not found", fmt.Errorf("link UID: %s", linkUID))
		}
		return nil, 0, errs.NewUnexpected("failed to get link", errGet)
	}
	if link.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("link not found", fmt.Errorf("link UID: %s does not belong to committee: %s", linkUID, committeeUID))
	}
	return link, rev, nil
}

func (s *storage) ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	slog.DebugContext(ctx, "listing committee links from NATS storage", "committee_uid", committeeUID)
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeLinks].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee links bucket", errKeys)
	}
	var links []*model.CommitteeLink
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		link := &model.CommitteeLink{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeLinks, key, link, false); err != nil {
			slog.WarnContext(ctx, "failed to get link during list, skipping", "key", key, "error", err)
			continue
		}
		if link.CommitteeUID == committeeUID {
			links = append(links, link)
		}
	}
	return links, nil
}

func (s *storage) DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64) error {
	link, _, errGet := s.GetLink(ctx, committeeUID, linkUID)
	if errGet != nil {
		return errGet
	}
	errPurge := s.client.kvStore[constants.KVBucketNameCommitteeLinks].Purge(ctx, link.UID)
	if errPurge != nil {
		return errs.NewUnexpected("failed to delete link", errPurge)
	}
	slog.DebugContext(ctx, "deleted link from NATS storage", "link_uid", linkUID, "committee_uid", committeeUID)
	return nil
}

func (s *storage) CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error {
	if folder == nil {
		return errs.NewValidation("folder cannot be nil")
	}
	folderBytes, errMarshal := json.Marshal(folder)
	if errMarshal != nil {
		return errs.NewUnexpected("failed to marshal folder", errMarshal)
	}
	rev, errCreate := s.client.kvStore[constants.KVBucketNameCommitteeFolders].Create(ctx, folder.UID, folderBytes)
	if errCreate != nil {
		return errs.NewUnexpected("failed to create folder", errCreate)
	}
	slog.DebugContext(ctx, "created folder in NATS storage", "folder_uid", folder.UID, "committee_uid", folder.CommitteeUID, "revision", rev)
	return nil
}

func (s *storage) UniqueLinkFolderName(ctx context.Context, folder *model.CommitteeLinkFolder) (string, error) {
	uniqueKey := fmt.Sprintf(constants.KVLookupFolderPrefix, folder.BuildIndexKey(ctx))
	_, errUnique := s.client.kvStore[constants.KVBucketNameCommitteeFolders].Create(ctx, uniqueKey, []byte(folder.UID))
	if errUnique != nil {
		if errors.Is(errUnique, jetstream.ErrKeyExists) {
			return uniqueKey, errs.NewConflict("folder with the same name already exists for this committee")
		}
		return uniqueKey, errs.NewUnexpected("failed to create unique key for folder name", errUnique)
	}
	return uniqueKey, nil
}

func (s *storage) GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error) {
	folder := &model.CommitteeLinkFolder{}
	rev, errGet := s.get(ctx, constants.KVBucketNameCommitteeFolders, folderUID, folder, false)
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			return nil, 0, errs.NewNotFound("folder not found", fmt.Errorf("folder UID: %s", folderUID))
		}
		return nil, 0, errs.NewUnexpected("failed to get folder", errGet)
	}
	if folder.CommitteeUID != committeeUID {
		return nil, 0, errs.NewNotFound("folder not found", fmt.Errorf("folder UID: %s does not belong to committee: %s", folderUID, committeeUID))
	}
	return folder, rev, nil
}

func (s *storage) ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	slog.DebugContext(ctx, "listing committee folders from NATS storage", "committee_uid", committeeUID)
	keys, errKeys := s.client.kvStore[constants.KVBucketNameCommitteeFolders].ListKeys(ctx)
	if errKeys != nil {
		return nil, errs.NewUnexpected("failed to list keys from committee folders bucket", errKeys)
	}
	var folders []*model.CommitteeLinkFolder
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") {
			continue
		}
		folder := &model.CommitteeLinkFolder{}
		if _, err := s.get(ctx, constants.KVBucketNameCommitteeFolders, key, folder, false); err != nil {
			slog.WarnContext(ctx, "failed to get folder during list, skipping", "key", key, "error", err)
			continue
		}
		if folder.CommitteeUID == committeeUID {
			folders = append(folders, folder)
		}
	}
	return folders, nil
}

func (s *storage) DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error {
	folder, _, errGet := s.GetLinkFolder(ctx, committeeUID, folderUID)
	if errGet != nil {
		return errGet
	}
	uniqueKey := fmt.Sprintf(constants.KVLookupFolderPrefix, folder.BuildIndexKey(ctx))
	_ = s.client.kvStore[constants.KVBucketNameCommitteeFolders].Purge(ctx, uniqueKey)
	errPurge := s.client.kvStore[constants.KVBucketNameCommitteeFolders].Purge(ctx, folder.UID)
	if errPurge != nil {
		return errs.NewUnexpected("failed to delete folder", errPurge)
	}
	slog.DebugContext(ctx, "deleted folder from NATS storage", "folder_uid", folderUID, "committee_uid", committeeUID)
	return nil
}
