// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// BriefBackend is what the composite needs of a store of the weekly briefs and their throttle entries.
type BriefBackend interface {
	port.GroupWeeklyBriefReader
	port.GroupWeeklyBriefWriter
}

// Briefs is the composite of the weekly briefs and throttle entries, built like Store: the primary store decides every
// result and is embedded, so every method not overridden goes to it unchanged; in dual mode each put is copied to
// PostgreSQL from what KV returned for it, with the revision KV gave it, and a sampled fraction of reads is compared. See Store
// for what that guarantees. A brief or a throttle entry is never deleted, so a copy has no delete to race with.
type Briefs struct {
	BriefBackend
	core
	kv, pg BriefBackend
}

var _ BriefBackend = (*Briefs)(nil)

// NewBriefs returns the composite of the weekly briefs and throttle entries for the mode; see New.
func NewBriefs(mode Mode, kv, pg BriefBackend, opts ...Option) (*Briefs, error) {
	primary, err := selectPrimary(mode, kv, pg)
	if err != nil {
		return nil, err
	}
	return &Briefs{BriefBackend: primary, core: newCore(mode, opts), kv: kv, pg: pg}, nil
}

// PutGroupWeeklyBrief writes the brief and returns it with its new revision. In dual mode the brief KV returned is copied
// to PostgreSQL, which holds one brief per committee and window: a second brief for a window KV let through is refused
// there and counted.
func (b *Briefs) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	stored, err := b.BriefBackend.PutGroupWeeklyBrief(ctx, brief)
	if err != nil || stored == nil {
		return stored, err
	}
	if b.mode == ModeDual {
		copied := *stored
		b.replicate(ctx, "put_weekly_brief", copied.UID, func(ctx context.Context) error { return b.copyBrief(ctx, &copied) })
	}
	return stored, nil
}

// copyBrief writes the brief to PostgreSQL with the revision KV gave it. A write replaces the brief with the same uid, so a
// copy that was missed is made up for by the next one; nothing is written when PostgreSQL already holds this revision or a
// newer one of the brief.
func (b *Briefs) copyBrief(ctx context.Context, stored *model.GroupWeeklyBrief) error {
	existing, _, err := b.pg.GetGroupWeeklyBriefForWindow(ctx, stored.CommitteeUID, *stored)
	if err != nil {
		return err
	}
	if existing != nil && existing.UID == stored.UID && existing.Revision >= stored.Revision {
		return nil
	}
	wctx, err := postgres.WithRevision(ctx, stored.Revision)
	if err != nil {
		return err
	}
	stored.Revision = 0 // a put without a revision replaces the brief with the same uid, as a write of KV does
	_, err = b.pg.PutGroupWeeklyBrief(wctx, stored)
	return err
}

// PutGroupWeeklyBriefThrottle writes or updates the throttle entry, guarded by the revision it carries. In dual mode the
// entry KV returned is copied to PostgreSQL.
func (b *Briefs) PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	stored, err := b.BriefBackend.PutGroupWeeklyBriefThrottle(ctx, throttle)
	if err != nil || stored == nil {
		return stored, err
	}
	if b.mode == ModeDual {
		copied := *stored
		b.replicate(ctx, "put_weekly_brief_throttle", copied.CommitteeUID+"."+model.WindowDateKey(copied.WindowStart),
			func(ctx context.Context) error { return b.copyThrottle(ctx, &copied) })
	}
	return stored, nil
}

// copyThrottle writes the throttle entry to PostgreSQL with the revision KV gave it, guarded by the revision PostgreSQL has
// now, like copyUpdated: it creates the entry when PostgreSQL has none and writes nothing when it already holds this
// revision or a newer one.
func (b *Briefs) copyThrottle(ctx context.Context, stored *model.GroupWeeklyBriefThrottle) error {
	current, err := b.pg.GetGroupWeeklyBriefThrottle(ctx, stored.CommitteeUID, stored.WindowStart)
	if err != nil {
		return err
	}
	revision := stored.Revision
	switch {
	case current == nil:
		stored.Revision = 0 // a create
	case current.Revision >= revision:
		return nil
	default:
		stored.Revision = current.Revision // the guard
	}
	wctx, err := postgres.WithRevision(ctx, revision)
	if err != nil {
		return err
	}
	_, err = b.pg.PutGroupWeeklyBriefThrottle(wctx, stored)
	return err
}

// GetGroupWeeklyBriefForWindow returns the committee's brief for the window and the throttle JSON of the same window. In
// dual mode a sampled fraction is also made against PostgreSQL and the brief is compared, by content and revision; the
// throttle entry has a read of its own.
func (b *Briefs) GetGroupWeeklyBriefForWindow(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	brief, throttle, err := b.BriefBackend.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if err == nil && b.sampled() {
		b.compareValue(ctx, "get_weekly_brief", func(ctx context.Context) (bool, error) {
			other, _, otherErr := b.pg.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
			if otherErr != nil {
				return false, otherErr
			}
			return sameBrief(brief, other)
		})
	}
	return brief, throttle, err
}

// GetGroupWeeklyBriefThrottle returns the throttle entry of the committee's window, or nil.
func (b *Briefs) GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	throttle, err := b.BriefBackend.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if err == nil && b.sampled() {
		b.compareValue(ctx, "get_weekly_brief_throttle", func(ctx context.Context) (bool, error) {
			other, otherErr := b.pg.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
			if otherErr != nil {
				return false, otherErr
			}
			return sameThrottle(throttle, other)
		})
	}
	return throttle, err
}

// sameBrief compares two briefs, either of which may be missing, by content and revision.
func sameBrief(a, b *model.GroupWeeklyBrief) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	da, err := Digest(a)
	if err != nil {
		return false, err
	}
	db, err := Digest(b)
	if err != nil {
		return false, err
	}
	return da == db && a.Revision == b.Revision, nil
}

// sameThrottle compares two throttle entries, either of which may be missing, by content and revision.
func sameThrottle(a, b *model.GroupWeeklyBriefThrottle) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	da, err := Digest(a)
	if err != nil {
		return false, err
	}
	db, err := Digest(b)
	if err != nil {
		return false, err
	}
	return da == db && a.Revision == b.Revision, nil
}
