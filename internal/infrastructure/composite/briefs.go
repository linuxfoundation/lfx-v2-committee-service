// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
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
// PostgreSQL-primary mode needs that reconciled first: which brief owns the window has to be decided and repaired by hand.
// storage verify does not flag the one PostgreSQL kept as extra, because the export includes briefs the window index no
// longer names and matches them by uid; it reports only the brief KV serves as missing. In dual-pg mode PostgreSQL is the
// primary store and each put is copied to KV, which stamps the update time of a brief itself: the brief in KV carries the
// time of its copy, and briefs are compared without it.
type Briefs struct {
	BriefBackend
	core
	// src is the primary store the copy reads and dst the secondary store it writes; see Store.
	src, dst BriefBackend
}

var _ BriefBackend = (*Briefs)(nil)

// NewBriefs returns the composite of the weekly briefs and throttle entries for the mode; see New.
func NewBriefs(mode Mode, kv, pg BriefBackend, opts ...Option) (*Briefs, error) {
	primary, err := selectPrimary(mode, kv, pg)
	if err != nil {
		return nil, err
	}
	b := &Briefs{BriefBackend: primary, core: newCore(mode, opts), src: kv, dst: pg}
	if mode == ModeDualPG {
		b.src, b.dst = pg, kv
	}
	return b, nil
}

// PutGroupWeeklyBrief writes the brief and returns it with its new revision. In dual mode the brief KV serves for the
// window is copied to PostgreSQL, which holds one brief per committee and window: a second brief for a window KV let
// through is refused there and counted.
func (b *Briefs) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	stored, err := b.BriefBackend.PutGroupWeeklyBrief(ctx, brief)
	if err != nil || stored == nil {
		return stored, err
	}
	if b.mode.HasSecondary() {
		committeeUID, window := stored.CommitteeUID, *stored
		b.replicate(ctx, "put_weekly_brief", stored.UID, func(ctx context.Context) error { return b.copyBrief(ctx, committeeUID, window) })
	}
	return stored, nil
}

// copyBrief writes the brief the primary store serves for the window now to the secondary store, with the revision the primary
// store gave it where the secondary store can take one. It reads the primary store again, like copyUpdated, and does not copy
// the value a put returned: when two puts of a brief overlap, each copy then writes the newest state, and the one that comes
// second finds it already there and writes nothing, where copying what each put returned would leave the secondary store on
// the older one, or count a refused copy that is not a failure. A write replaces the brief with the same uid, so a copy that
// was missed is made up for by the next one; nothing is written when PostgreSQL already holds this revision or a newer one of
// the brief.
//
// In dual-pg mode KV is the secondary store, which refuses nothing but a stale guard, so as in copyUpdated it is read before
// the primary store and the write is guarded by the revision read then, and a refused write is made once more from the
// start. A window that has no brief in KV is put without a guard, which an overlapping copy of an older brief could
// overwrite, so after such a put the two stores are compared again and the copy is made once more if they differ.
func (b *Briefs) copyBrief(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) error {
	attempts := 1
	if !b.secondaryIsPostgres() {
		attempts = 2
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		var settled bool
		if settled, err = b.copyBriefOnce(ctx, committeeUID, window); settled {
			return err
		}
	}
	if err == nil {
		err = errors.New("the copy of the weekly brief was overtaken by another copy and left KV behind")
	}
	return err
}

// copyBriefOnce makes one copy. settled is false only when an overlapping copy got in the way of KV's write.
func (b *Briefs) copyBriefOnce(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) (settled bool, err error) {
	var existing *model.GroupWeeklyBrief
	if !b.secondaryIsPostgres() {
		if existing, _, err = b.dst.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window); err != nil {
			return true, err // before the primary store, see copyBrief
		}
	}
	served, _, err := b.src.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if err != nil {
		return true, err
	}
	if served == nil {
		return true, nil // there is no brief for the window, so there is nothing to copy
	}
	if b.secondaryIsPostgres() {
		if existing, _, err = b.dst.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window); err != nil {
			return true, err
		}
	}
	copied := *served
	if b.secondaryIsPostgres() {
		if existing != nil && existing.UID == served.UID && existing.Revision >= served.Revision {
			return true, nil
		}
		copied.Revision = 0 // a put without a revision replaces the brief with the same uid, as a write of KV does
	} else {
		// KV has revisions of its own: the brief with the same uid is replaced under the revision KV has, nothing is written
		// when it already holds the same content, and a window without one, or with another brief, is put as a new one.
		copied.Revision = 0
		if existing != nil && existing.UID == served.UID {
			if same, err := sameBriefContent(served, existing); err != nil || same {
				return true, err
			}
			copied.Revision = existing.Revision
		}
	}
	wctx, err := b.carry(ctx, served.Revision)
	if err != nil {
		return true, err
	}
	guarded := copied.Revision != 0 // the put sets the revision on what it is given
	_, err = b.dst.PutGroupWeeklyBrief(wctx, &copied)
	if refusedAsStale(err) {
		// Another copy wrote between the read above and this write. That is not a failure if the window now holds this
		// brief at this revision or a newer one (in dual-pg mode, with the same content).
		now, _, getErr := b.dst.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
		if getErr == nil && now != nil && now.UID == served.UID {
			if b.secondaryIsPostgres() && now.Revision >= served.Revision {
				return true, nil
			}
			if same, sameErr := sameBriefContent(served, now); !b.secondaryIsPostgres() && sameErr == nil && same {
				return true, nil
			}
		}
		return b.secondaryIsPostgres(), err
	}
	if err != nil || b.secondaryIsPostgres() || guarded {
		return true, err
	}
	// KV was put without a guard: see whether an overlapping copy of an older brief overwrote a newer one.
	latest, _, srcErr := b.src.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	held, _, dstErr := b.dst.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if srcErr != nil || dstErr != nil {
		return true, errors.Join(srcErr, dstErr)
	}
	if latest == nil || held == nil {
		return latest == nil, nil
	}
	same, err := sameBriefContent(latest, held)
	return err != nil || same, err
}

// sameBriefContent reports whether two briefs have the same content apart from the update time. KV stamps the update time
// of every put of a brief with the time of the write and cannot be given one, so the brief KV holds as the secondary store
// of dual-pg mode carries the time of the copy, and the two stores' update times never agree.
func sameBriefContent(a, b *model.GroupWeeklyBrief) (bool, error) {
	x, y := *a, *b
	x.UpdatedAt, y.UpdatedAt = time.Time{}, time.Time{}
	return sameContent(&x, &y)
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
	if b.mode.HasSecondary() {
		committeeUID, windowStart := stored.CommitteeUID, stored.WindowStart
		b.replicate(ctx, "put_weekly_brief_throttle", committeeUID+"."+model.WindowDateKey(windowStart),
			func(ctx context.Context) error { return b.copyThrottle(ctx, committeeUID, windowStart) })
	}
	return stored, nil
}

// copyThrottle writes the throttle entry the primary store holds now to the secondary store, with the revision it gave where
// the secondary store can take one (see copyBrief), guarded by the revision
// PostgreSQL has now. It reads KV again, like copyUpdated: when two puts overlap each copy writes the newest entry, and a
// copy that finds PostgreSQL already there writes nothing, where copying what each put returned would leave the older one
// whenever the first copy wins the swap. It creates the entry when PostgreSQL has none. When an overlapping copy wins the
// write first, this one is settled if PostgreSQL then holds this revision or a newer one; if it holds an older one (the
// other copy had read an older entry) the copy is made once more from the start, so the newest entry is not left behind.
func (b *Briefs) copyThrottle(ctx context.Context, committeeUID string, windowStart time.Time) error {
	for attempt := 0; ; attempt++ {
		settled, err := b.copyThrottleOnce(ctx, committeeUID, windowStart)
		if settled || attempt > 0 {
			return err
		}
	}
}

// copyThrottleOnce makes one copy. settled is false only when a write was refused because an overlapping copy got there
// first and PostgreSQL still holds an older revision than the one KV holds, so that another attempt can succeed.
func (b *Briefs) copyThrottleOnce(ctx context.Context, committeeUID string, windowStart time.Time) (settled bool, err error) {
	var current *model.GroupWeeklyBriefThrottle
	if !b.secondaryIsPostgres() {
		if current, err = b.dst.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart); err != nil {
			return true, err // before the primary store, see copyBrief
		}
	}
	served, err := b.src.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if err != nil {
		return true, err
	}
	if served == nil {
		return true, nil
	}
	if b.secondaryIsPostgres() {
		if current, err = b.dst.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart); err != nil {
			return true, err
		}
	}
	revision := served.Revision
	copied := *served
	switch {
	case current == nil:
		copied.Revision = 0 // a create
	case b.secondaryIsPostgres() && current.Revision >= revision:
		return true, nil
	case !b.secondaryIsPostgres():
		if same, err := sameContent(served, current); err != nil || same {
			return true, err
		}
		copied.Revision = current.Revision // the guard, KV's own
	default:
		copied.Revision = current.Revision // the guard
	}
	wctx, err := b.carry(ctx, revision)
	if err != nil {
		return true, err
	}
	_, err = b.dst.PutGroupWeeklyBriefThrottle(wctx, &copied)
	if !refusedAsStale(err) {
		return true, err
	}
	now, getErr := b.dst.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if getErr == nil && now != nil {
		if b.secondaryIsPostgres() && now.Revision >= revision {
			return true, nil
		}
		if same, sameErr := sameContent(served, now); !b.secondaryIsPostgres() && sameErr == nil && same {
			return true, nil
		}
	}
	return false, err
}

// GetGroupWeeklyBriefForWindow returns the committee's brief for the window and the throttle JSON of the same window. In
// dual mode a sampled fraction is also made against PostgreSQL and the brief is compared, by content and revision; the
// throttle entry has a read of its own.
func (b *Briefs) GetGroupWeeklyBriefForWindow(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	brief, throttle, err := b.BriefBackend.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if err == nil && b.sampled() {
		b.compareValue(ctx, "get_weekly_brief", func(ctx context.Context) (bool, error) {
			other, _, otherErr := b.dst.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
			if otherErr != nil {
				return false, otherErr
			}
			return b.agreeBrief(brief, other)
		})
	}
	return brief, throttle, err
}

// GetGroupWeeklyBriefThrottle returns the throttle entry of the committee's window, or nil.
func (b *Briefs) GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	throttle, err := b.BriefBackend.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if err == nil && b.sampled() {
		b.compareValue(ctx, "get_weekly_brief_throttle", func(ctx context.Context) (bool, error) {
			other, otherErr := b.dst.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
			if otherErr != nil {
				return false, otherErr
			}
			return b.agreeThrottle(throttle, other)
		})
	}
	return throttle, err
}

// agreeBrief compares the brief the primary store returned with the secondary store's: by content and revision in dual mode,
// and in dual-pg mode by content alone, where the two stores have revisions of their own, and without the update time.
func (b *Briefs) agreeBrief(a, other *model.GroupWeeklyBrief) (bool, error) {
	if !b.secondaryIsPostgres() && a != nil && other != nil {
		x, y := *a, *other
		x.Revision, y.Revision = 0, 0
		x.UpdatedAt, y.UpdatedAt = time.Time{}, time.Time{} // see sameBriefContent
		return sameBrief(&x, &y)
	}
	return sameBrief(a, other)
}

// agreeThrottle is agreeBrief for the throttle entry.
func (b *Briefs) agreeThrottle(a, other *model.GroupWeeklyBriefThrottle) (bool, error) {
	if !b.secondaryIsPostgres() && a != nil && other != nil {
		x, y := *a, *other
		x.Revision, y.Revision = 0, 0
		return sameThrottle(&x, &y)
	}
	return sameThrottle(a, other)
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
