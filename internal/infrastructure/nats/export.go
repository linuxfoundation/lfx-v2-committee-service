// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
	if failedReads > 0 {
		return errs.NewUnexpected("failed to read one or more committees while streaming all", fmt.Errorf("failed_reads=%d", failedReads))
	}
	return nil
}

// EachMemberExport streams every committee member with the revision it was read at.
func (s *storage) EachMemberExport(ctx context.Context, fn func(*model.CommitteeMember, uint64) error) error {
	return s.eachMemberWithRevision(ctx, fn, true)
}
