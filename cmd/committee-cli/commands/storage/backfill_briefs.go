// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// briefSpec copies the weekly briefs. A brief is found in PostgreSQL by its committee and window, as readers
// find it, and PostgreSQL holds one brief per window: a KV window that holds two (a race between first
// generations leaves the one the index does not serve in the bucket) has its second brief reported as a
// constraint, and the exporter sends the served brief first, so the one a reader sees is the one that is kept.
func briefSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.GroupWeeklyBrief] {
	return childSpec[*model.GroupWeeklyBrief]{
		each:         src.EachBriefExport,
		uid:          func(b *model.GroupWeeklyBrief) string { return b.UID },
		committeeUID: func(b *model.GroupWeeklyBrief) string { return b.CommitteeUID },
		revision: func(ctx context.Context, b *model.GroupWeeklyBrief) (uint64, error) {
			existing, _, err := sink.GetGroupWeeklyBriefForWindow(ctx, b.CommitteeUID, *b)
			switch {
			case err != nil:
				return 0, err
			case existing == nil:
				return 0, errs.NewNotFound("weekly brief not found")
			case existing.UID != b.UID:
				return 0, errs.NewConflict(fmt.Sprintf("the window already has another brief, uid %s", existing.UID))
			}
			return existing.Revision, nil
		},
		create: func(ctx context.Context, b *model.GroupWeeklyBrief) error {
			b.Revision = 0
			_, err := sink.PutGroupWeeklyBrief(ctx, b)
			return briefConflict(err)
		},
		update: func(ctx context.Context, b *model.GroupWeeklyBrief, pgRevision uint64) error {
			b.Revision = pgRevision
			_, err := sink.PutGroupWeeklyBrief(ctx, b)
			return briefConflict(err)
		},
	}
}

// throttleSpec copies the weekly-brief throttle entries, which KV and PostgreSQL both key by committee and window.
func throttleSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.GroupWeeklyBriefThrottle] {
	return childSpec[*model.GroupWeeklyBriefThrottle]{
		each: src.EachThrottleExport,
		uid: func(t *model.GroupWeeklyBriefThrottle) string {
			return t.CommitteeUID + "." + model.WindowDateKey(t.WindowStart)
		},
		committeeUID: func(t *model.GroupWeeklyBriefThrottle) string { return t.CommitteeUID },
		revision: func(ctx context.Context, t *model.GroupWeeklyBriefThrottle) (uint64, error) {
			existing, err := sink.GetGroupWeeklyBriefThrottle(ctx, t.CommitteeUID, t.WindowStart)
			switch {
			case err != nil:
				return 0, err
			case existing == nil:
				return 0, errs.NewNotFound("weekly-brief throttle entry not found")
			}
			return existing.Revision, nil
		},
		create: func(ctx context.Context, t *model.GroupWeeklyBriefThrottle) error {
			t.Revision = 0
			_, err := sink.PutGroupWeeklyBriefThrottle(ctx, t)
			return briefConflict(err)
		},
		update: func(ctx context.Context, t *model.GroupWeeklyBriefThrottle, pgRevision uint64) error {
			t.Revision = pgRevision
			_, err := sink.PutGroupWeeklyBriefThrottle(ctx, t)
			return briefConflict(err)
		},
	}
}

// briefConflict turns the retryable service-unavailable error the store gives for a window that is taken or a swap
// that was lost into a conflict: a copy does not retry, it reports, and a lost swap here means someone else wrote the row.
func briefConflict(err error) error {
	var unavailable errs.ServiceUnavailable
	if errors.As(err, &unavailable) {
		return errs.NewConflict("the weekly brief window is held by another record or was changed during the run")
	}
	return err
}
