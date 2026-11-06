// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// BriefBackend is what the composite needs of a store of the weekly briefs and their throttle entries.
type BriefBackend interface {
	port.GroupWeeklyBriefReader
	port.GroupWeeklyBriefWriter
}

// Briefs is the composite of the weekly briefs and throttle entries, built like Store: the primary store decides every
// result and is embedded, so every method not overridden goes to it unchanged; in dual mode each put is followed by a copy
// to PostgreSQL of what KV holds, with the revision KV gave it, and a sampled fraction of reads is compared. See Store for
// what that guarantees. A brief or a throttle entry is never deleted, so a copy has no delete to race with. PostgreSQL
// holds one brief per window: when KV lets a second brief take a window over, PostgreSQL keeps the first, and a cutover to a
// PostgreSQL-primary mode needs that repaired first (storage verify reports the brief KV serves as missing and the one
// PostgreSQL kept as extra).
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

// PutGroupWeeklyBrief writes the brief and returns it with its new revision. In dual mode the brief KV serves for the
// window is copied to PostgreSQL, which holds one brief per committee and window: a second brief for a window KV let
// through is refused there and counted.
func (b *Briefs) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	stored, err := b.BriefBackend.PutGroupWeeklyBrief(ctx, brief)
	if err != nil || stored == nil {
		return stored, err
	}
	if b.mode == ModeDual {
		committeeUID, window := stored.CommitteeUID, *stored
		b.replicate(ctx, "put_weekly_brief", stored.UID, func(ctx context.Context) error { return b.copyBrief(ctx, committeeUID, window) })
	}
	return stored, nil
}

// copyBrief writes the brief KV serves for the window now to PostgreSQL, with the revision KV gave it. It reads KV again,
// like copyUpdated, and does not copy the value a put returned: when two puts of a brief overlap, each copy then writes
// the newest state, and the one that comes second finds it already there and writes nothing, where copying what each put
// returned would leave PostgreSQL on the older one, or count a refused copy that is not a failure. A write replaces the
// brief with the same uid, so a copy that was missed is made up for by the next one; nothing is written when PostgreSQL
// already holds this revision or a newer one of the brief.
func (b *Briefs) copyBrief(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) error {
	served, _, err := b.kv.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if err != nil {
		return err
	}
	if served == nil {
		return nil // there is no brief for the window, so there is nothing to copy
	}
	existing, _, err := b.pg.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if err != nil {
		return err
	}
	if existing != nil && existing.UID == served.UID && existing.Revision >= served.Revision {
		return nil
	}
	wctx, err := postgres.WithRevision(ctx, served.Revision)
	if err != nil {
		return err
	}
	copied := *served
	copied.Revision = 0 // a put without a revision replaces the brief with the same uid, as a write of KV does
	_, err = b.pg.PutGroupWeeklyBrief(wctx, &copied)
	if refusedAsStale(err) {
		// Another copy wrote between the read above and this write; PostgreSQL then refuses a revision that is not above
		// its own. That is not a failure if the window now holds this brief at this revision or a newer one.
		existing, _, getErr := b.pg.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
		if getErr == nil && existing != nil && existing.UID == served.UID && existing.Revision >= served.Revision {
			return nil
		}
	}
	return err
}

// refusedAsStale reports whether PostgreSQL refused a write because its revision is not above the one stored, or because
// a compare-and-swap was lost (it answers that with a service-unavailable error to retry): what an overlapping copy of
// the same record causes. The caller decides from a read whether the record is where it should be.
func refusedAsStale(err error) bool {
	var validation errs.Validation
	var conflict errs.Conflict
	var lost errs.ServiceUnavailable
	return errors.As(err, &validation) || errors.As(err, &conflict) || errors.As(err, &lost)
}

// PutGroupWeeklyBriefThrottle writes or updates the throttle entry, guarded by the revision it carries. In dual mode the
// entry KV holds now is copied to PostgreSQL.
func (b *Briefs) PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	stored, err := b.BriefBackend.PutGroupWeeklyBriefThrottle(ctx, throttle)
	if err != nil || stored == nil {
		return stored, err
	}
	if b.mode == ModeDual {
		committeeUID, windowStart := stored.CommitteeUID, stored.WindowStart
		b.replicate(ctx, "put_weekly_brief_throttle", committeeUID+"."+model.WindowDateKey(windowStart),
			func(ctx context.Context) error { return b.copyThrottle(ctx, committeeUID, windowStart) })
	}
	return stored, nil
}

// copyThrottle writes the throttle entry KV holds now to PostgreSQL, with the revision KV gave it, guarded by the revision
// PostgreSQL has now. It reads KV again, like copyUpdated: when two puts overlap each copy writes the newest entry, and a
// copy that finds PostgreSQL already there writes nothing, where copying what each put returned would leave the older one
// whenever the first copy wins the swap. It creates the entry when PostgreSQL has none, and a swap or create an overlapping
// copy won is not a failure when PostgreSQL then holds this revision or a newer one.
func (b *Briefs) copyThrottle(ctx context.Context, committeeUID string, windowStart time.Time) error {
	served, err := b.kv.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if err != nil {
		return err
	}
	if served == nil {
		return nil
	}
	current, err := b.pg.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if err != nil {
		return err
	}
	revision := served.Revision
	copied := *served
	switch {
	case current == nil:
		copied.Revision = 0 // a create
	case current.Revision >= revision:
		return nil
	default:
		copied.Revision = current.Revision // the guard
	}
	wctx, err := postgres.WithRevision(ctx, revision)
	if err != nil {
		return err
	}
	_, err = b.pg.PutGroupWeeklyBriefThrottle(wctx, &copied)
	if refusedAsStale(err) {
		now, getErr := b.pg.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
		if getErr == nil && now != nil && now.Revision >= revision {
			return nil
		}
	}
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
