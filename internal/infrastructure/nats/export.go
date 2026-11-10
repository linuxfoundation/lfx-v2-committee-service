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

	"github.com/nats-io/nats.go/jetstream"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

var _ port.KVExportReader = (*storage)(nil)

// EachCommitteeExport streams every committee with its settings and the revision of each, one committee
// at a time. A committee that cannot be read is skipped and counted, and the scan ends with an error that
// says how many; a committee with no settings record comes back with nil settings.
func (s *storage) EachCommitteeExport(ctx context.Context, fn func(port.CommitteeExport) error) error {
	uids, err := s.ListAllUIDs(ctx)
	if err != nil {
		return err
	}
	failedReads := 0
	for _, uid := range uids {
		if err := ctx.Err(); err != nil {
			return errs.NewUnexpected("context cancelled while streaming all committees", err)
		}
		export := port.CommitteeExport{Base: &model.CommitteeBase{}}
		var errGet error
		if export.BaseRevision, errGet = s.get(ctx, constants.KVBucketNameCommittees, uid, export.Base, false); errGet != nil {
			// Deleted between listing and reading is not a failure: the committee is gone.
			if !errors.Is(errGet, jetstream.ErrKeyNotFound) {
				slog.WarnContext(ctx, "failed to get committee while streaming all", "committee_uid", uid, "error", errGet)
				failedReads++
			}
			continue
		}
		settings := &model.CommitteeSettings{}
		switch revision, errSettings := s.get(ctx, constants.KVBucketNameCommitteeSettings, uid, settings, false); {
		case errSettings == nil:
			// The settings are keyed by the committee uid; the store writes and updates them by the uid in
			// the record, so a record whose own uid is empty or different must not change what they are keyed by.
			settings.UID = uid
			export.Settings, export.SettingsRevision = settings, revision
		case !errors.Is(errSettings, jetstream.ErrKeyNotFound):
			slog.WarnContext(ctx, "failed to get committee settings while streaming all", "committee_uid", uid, "error", errSettings)
			failedReads++
			continue
		}
		if err := fn(export); err != nil {
			return err
		}
	}
	// A cancelled context can end the key listing with no error, which must not look like a complete scan.
	if err := ctx.Err(); err != nil {
		return errs.NewUnexpected("context cancelled while streaming all committees", err)
	}
	if failedReads > 0 {
		return errs.NewUnexpected("failed to read one or more committees while streaming all", fmt.Errorf("failed_reads=%d", failedReads))
	}
	return nil
}

// EachSettingsExport streams every settings record with the revision it was read at, including those of a committee
// that no longer exists. The settings are keyed by the committee uid, so the key is the uid whatever the record holds.
func (s *storage) EachSettingsExport(ctx context.Context, fn func(*model.CommitteeSettings, uint64) error) error {
	return eachKeyedRecord(ctx, s.client.kvStore[constants.KVBucketNameCommitteeSettings], "committee settings", nil,
		func(key string, settings *model.CommitteeSettings, revision uint64) error {
			settings.UID = key
			return fn(settings, revision)
		})
}

// EachMemberExport streams every committee member with the revision it was read at.
func (s *storage) EachMemberExport(ctx context.Context, fn func(*model.CommitteeMember, uint64) error) error {
	return s.eachMemberWithRevision(ctx, fn, true)
}

// EachInviteExport streams every committee invite with the revision it was read at.
func (s *storage) EachInviteExport(ctx context.Context, fn func(*model.CommitteeInvite, uint64) error) error {
	return eachRecord(ctx, s.client.kvStore[constants.KVBucketNameCommitteeInvites], "invites", nil, fn)
}

// EachApplicationExport streams every committee application with the revision it was read at.
func (s *storage) EachApplicationExport(ctx context.Context, fn func(*model.CommitteeApplication, uint64) error) error {
	return eachRecord(ctx, s.client.kvStore[constants.KVBucketNameCommitteeApplications], "applications", nil, fn)
}

// EachLinkExport streams every committee link with the revision it was read at.
func (s *storage) EachLinkExport(ctx context.Context, fn func(*model.CommitteeLink, uint64) error) error {
	return eachRecord(ctx, s.client.kvStore[constants.KVBucketNameCommitteeLinks], "links", nil, fn)
}

// EachFolderExport streams every committee link folder with the revision it was read at.
func (s *storage) EachFolderExport(ctx context.Context, fn func(*model.CommitteeLinkFolder, uint64) error) error {
	return eachRecord(ctx, s.client.kvStore[constants.KVBucketNameCommitteeFolders], "folders", nil, fn)
}

// EachDocumentExport streams the metadata of every committee document with the revision it was read at.
func (s *storage) EachDocumentExport(ctx context.Context, fn func(*model.CommitteeDocument, uint64) error) error {
	return eachRecord(ctx, s.client.kvStore[constants.KVBucketNameCommitteeDocuments], "documents", nil, fn)
}

// EachBriefExport streams every weekly brief: first those the window index names, then the rest. A brief
// the index does not name is unreachable through the API; it is still exported, because the copy decides what
// to do with it, but after the served ones so that it cannot take a window from the brief that was served.
func (s *storage) EachBriefExport(ctx context.Context, fn func(*model.GroupWeeklyBrief, uint64) error) error {
	briefs, ok, err := s.optionalBucket(ctx, constants.KVBucketNameGroupWeeklyBriefs)
	if err != nil || !ok {
		return err
	}
	served := map[string]struct{}{}
	if idx, ok, err := s.optionalBucket(ctx, constants.KVBucketNameGroupWeeklyBriefUIDIndex); err != nil {
		return err
	} else if ok {
		keys, err := idx.Keys(ctx)
		if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
			return errs.NewUnexpected("failed to list weekly-brief uid index keys", err)
		}
		for _, key := range keys {
			entry, err := idx.Get(ctx, key)
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				continue
			}
			if err != nil {
				return errs.NewUnexpected("failed to read weekly-brief uid index", err)
			}
			served[sanitizeKVKey(string(entry.Value()))] = struct{}{}
		}
	}
	each := func(want bool) error {
		keep := func(key string) bool { _, isServed := served[key]; return isServed == want }
		return eachRecord(ctx, briefs, "weekly briefs", keep, func(brief *model.GroupWeeklyBrief, revision uint64) error {
			brief.Revision = revision
			return fn(brief, revision)
		})
	}
	// An error in the first pass ends the export: a callback error is the caller's request to stop, and a served
	// brief that could not be read must not leave its window to the unserved brief that shares it, which a copy
	// would then keep. The run is repeated once the record is readable.
	if err := each(true); err != nil {
		return err
	}
	return each(false)
}

// EachThrottleExport streams every weekly-brief throttle entry with the revision it was read at.
func (s *storage) EachThrottleExport(ctx context.Context, fn func(*model.GroupWeeklyBriefThrottle, uint64) error) error {
	throttles, ok, err := s.optionalBucket(ctx, constants.KVBucketNameGroupWeeklyBriefThrottle)
	if err != nil || !ok {
		return err
	}
	return eachRecord(ctx, throttles, "weekly-brief throttles", nil, func(throttle *model.GroupWeeklyBriefThrottle, revision uint64) error {
		throttle.Revision = revision
		return fn(throttle, revision)
	})
}

// optionalBucket binds a bucket that a deployment may not have created: the weekly-brief buckets are made
// when the feature is first used. A bucket that is not there is empty, which is logged; any other failure
// to bind it is an error, so an outage is never mistaken for an empty bucket.
func (s *storage) optionalBucket(ctx context.Context, name string) (jetstream.KeyValue, bool, error) {
	kv, err := s.client.GetOrBindKVStore(ctx, name)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		slog.WarnContext(ctx, "KV bucket does not exist, treating it as empty", "bucket", name)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return kv, true, nil
}

// eachRecord streams the records of a bucket as T with the revision each was read at. Secondary-index keys
// ("lookup/") are skipped, and so are keys keep rejects when it is not nil. A record that cannot be read or
// decoded is logged and skipped and the scan ends with an aggregated error that says how many; a record
// deleted between listing and reading is gone, not a failed read. The first error fn returns stops the scan.
func eachRecord[T any](ctx context.Context, kv jetstream.KeyValue, what string, keep func(key string) bool, fn func(*T, uint64) error) error {
	return eachKeyedRecord(ctx, kv, what, keep, func(_ string, record *T, revision uint64) error { return fn(record, revision) })
}

// eachKeyedRecord is eachRecord for a caller that needs the key the record is stored under.
func eachKeyedRecord[T any](ctx context.Context, kv jetstream.KeyValue, what string, keep func(key string) bool, fn func(key string, record *T, revision uint64) error) error {
	keys, err := kv.ListKeys(ctx)
	if err != nil {
		return errs.NewUnexpected(fmt.Sprintf("failed to list keys of the %s bucket", what), err)
	}
	defer func() { _ = keys.Stop() }()

	failedReads := 0
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") || (keep != nil && !keep(key)) {
			continue
		}
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		record := new(T)
		if err == nil {
			err = json.Unmarshal(entry.Value(), record)
		}
		if err != nil {
			slog.WarnContext(ctx, "failed to read a record while streaming all", "what", what, "key", key, "error", err)
			failedReads++
			continue
		}
		if err := fn(key, record, entry.Revision()); err != nil {
			return err
		}
	}
	// A cancelled context can end the key listing with no error, which must not look like a complete scan.
	if err := ctx.Err(); err != nil {
		return errs.NewUnexpected(fmt.Sprintf("context cancelled while streaming all %s", what), err)
	}
	if failedReads > 0 {
		return errs.NewUnexpected(fmt.Sprintf("failed to read one or more of the %s while streaming all", what), fmt.Errorf("failed_reads=%d", failedReads))
	}
	return nil
}
