// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// reverseSource is the PostgreSQL store as the copy back to KV reads it: every table streamed. *postgres.Store
// implements it.
type reverseSource = verifySource

var _ reverseSource = (*postgres.Store)(nil)

// reverseSpec is what the copy back needs to know about an entity that belongs to a committee: how to stream it from
// PostgreSQL and how to look it up, create it and replace it in KV. T is the record, a pointer to the model.
type reverseSpec[T any] struct {
	each func(ctx context.Context, fn func(record T, revision uint64) error) error
	// uid names the record in the report; committeeUID is the committee it belongs to.
	uid, committeeUID func(T) string
	// lookup returns what KV holds for the record and the revision it has there: errs.NotFound when it holds nothing,
	// errs.Conflict when the record that stands in its way is another one.
	lookup func(ctx context.Context, record T) (T, uint64, error)
	create func(ctx context.Context, record T) error
	// update replaces the record, guarded by the revision lookup returned.
	update func(ctx context.Context, record T, kvRevision uint64) error
	// normalize, when set, clears the fields KV sets itself, so that a record is compared by what it says.
	normalize func(T) T
}

// sameRecord reports whether two records say the same, whatever revision each store holds them at.
func (s reverseSpec[T]) sameRecord(a, b T) (bool, error) {
	if s.normalize != nil {
		a, b = s.normalize(a), s.normalize(b)
	}
	return sameContent(a, b)
}

// copyBack copies every PostgreSQL record of an entity whose committee exists in PostgreSQL, or every record with
// IncludeOrphans. Records PostgreSQL holds only for a committee it no longer has are reported with their uids and not
// copied.
func copyBack[T any](ctx context.Context, spec reverseSpec[T], opts backfillOptions, t *tracker, committees map[string]struct{}) error {
	return stream(ctx, opts.Workers, opts.BatchSize,
		func(emit func(childRecord[T]) error) error {
			return spec.each(ctx, func(record T, revision uint64) error {
				return emit(childRecord[T]{record: record, revision: revision})
			})
		},
		func(r childRecord[T]) { copyBackRecord(ctx, spec, opts, t, committees, r) })
}

func copyBackRecord[T any](ctx context.Context, spec reverseSpec[T], opts backfillOptions, t *tracker, committees map[string]struct{}, r childRecord[T]) {
	uid := spec.uid(r.record)
	t.scanned(ctx, r.revision)
	if _, ok := committees[spec.committeeUID(r.record)]; !ok && !opts.IncludeOrphans {
		t.orphan(ctx, uid)
		return
	}

	held, kvRevision, err := spec.lookup(ctx, r.record)
	switch {
	case isNotFound(err):
		if opts.DryRun {
			t.written()
			return
		}
		if err := spec.create(ctx, r.record); err != nil {
			t.problem(ctx, uid, writeKind(err), err.Error())
			return
		}
		t.written()
	case err != nil:
		kind := writeKind(err)
		if kind == "write" {
			kind = "read"
		}
		t.problem(ctx, uid, kind, err.Error())
	default:
		same, err := spec.sameRecord(r.record, held)
		switch {
		case err != nil:
			t.problem(ctx, uid, "read", err.Error())
		case same:
			t.unchanged()
		case opts.DryRun:
			t.written()
		default:
			if err := spec.update(ctx, r.record, kvRevision); err != nil {
				t.problem(ctx, uid, writeKind(err), err.Error())
				return
			}
			t.written()
		}
	}
}

// runReverse copies the committee data from PostgreSQL into the KV store, the way back from dual-pg or postgres mode.
// PostgreSQL decides: a record KV lacks is created, one KV holds with other content is replaced, and one with the
// same content is left alone, whatever revision each store gives it; KV assigns revisions of its own. It never
// deletes, so what only KV holds stays, and it can be run again. It returns one result per entity, with the settings of
// the committees as a line of their own. The error is non-nil when anything could not be copied or read, so the
// command can exit non-zero; the results are returned either way.
func runReverse(ctx context.Context, src reverseSource, sink port.KVRepairStore, opts backfillOptions) ([]backfillResult, error) {
	if len(opts.Entities) == 0 {
		opts.Entities = allEntities
	}
	if err := validateEntities(opts.Entities); err != nil {
		return nil, err
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 1000
	}
	newTracker := func(entity string) *tracker {
		return &tracker{res: backfillResult{Entity: entity}, progressEvery: opts.ProgressEvery, orphanMessage: "its committee does not exist in PostgreSQL"}
	}

	var existing map[string]struct{}
	var listErr error
	committeesInPostgreSQL := func() (map[string]struct{}, error) {
		if existing == nil && listErr == nil {
			existing, listErr = postgresCommitteeSet(ctx, src)
		}
		return existing, listErr
	}

	var trackers []*tracker
	if slices.Contains(opts.Entities, entityCommittees) {
		committees, settings := newTracker(entityCommittees), newTracker(resultSettings)
		trackers = append(trackers, committees, settings)
		scanError(ctx, committees, copyCommitteesBack(ctx, src, sink, opts, committees, settings))
	}
	for _, child := range reverseCopies(src, sink) {
		if !slices.Contains(opts.Entities, child.entity) {
			continue
		}
		t := newTracker(child.entity)
		trackers = append(trackers, t)
		set, err := committeesInPostgreSQL()
		if err != nil {
			scanError(ctx, t, err)
			continue
		}
		scanError(ctx, t, child.copy(ctx, opts, t, set))
	}

	results := make([]backfillResult, 0, len(trackers))
	total := int64(0)
	for _, t := range trackers {
		results = append(results, t.res)
		total += t.res.Errors
		slog.InfoContext(ctx, "copy back summary", "entity", t.res.Entity, "scanned", t.res.Scanned, "written", t.res.Written,
			"unchanged", t.res.Unchanged, "orphans", t.res.Orphans, "errors", t.res.Errors, "dry_run", opts.DryRun)
	}
	if total > 0 {
		return results, fmt.Errorf("%d records could not be copied or read", total)
	}
	return results, nil
}

// copyCommitteesBack copies every committee with its settings. One KV does not hold is created with its settings;
// one it holds is replaced when the content differs. KV creates settings only along with their committee, so settings
// missing next to a committee that exists are reported. The settings are read before the committees are streamed and held,
// one record each, because a worker that read them from PostgreSQL while the committees stream would wait for a pool
// connection the scan holds, and a pool of one connection would never finish.
func copyCommitteesBack(ctx context.Context, src reverseSource, sink port.KVRepairStore, opts backfillOptions, committees, settings *tracker) error {
	held := map[string]childRecord[*model.CommitteeSettings]{}
	if err := src.EachSettingsExport(ctx, func(s *model.CommitteeSettings, revision uint64) error {
		held[s.UID] = childRecord[*model.CommitteeSettings]{record: s, revision: revision}
		return nil
	}); err != nil {
		return fmt.Errorf("reading the settings from PostgreSQL: %w", err)
	}
	return stream(ctx, opts.Workers, opts.BatchSize,
		func(emit func(childRecord[*model.CommitteeBase]) error) error {
			return src.EachCommitteeExport(ctx, func(base *model.CommitteeBase, revision uint64) error {
				return emit(childRecord[*model.CommitteeBase]{record: base, revision: revision})
			})
		},
		func(r childRecord[*model.CommitteeBase]) {
			copyCommitteeBack(ctx, sink, opts, committees, settings, held[r.record.UID], r)
		})
}

// copyCommitteeBack copies one committee; held is its settings in PostgreSQL, the zero value when it has none.
func copyCommitteeBack(ctx context.Context, sink port.KVRepairStore, opts backfillOptions, committees, settings *tracker, held childRecord[*model.CommitteeSettings], r childRecord[*model.CommitteeBase]) {
	uid := r.record.UID
	committees.scanned(ctx, r.revision)
	pgSettings := held.record
	if pgSettings != nil {
		settings.scanned(ctx, held.revision)
	}

	inKV, kvRevision, err := sink.GetBase(ctx, uid)
	switch {
	case isNotFound(err):
		createCommitteeBack(ctx, sink, opts, committees, settings, r.record, pgSettings)
		return
	case err != nil:
		committees.problem(ctx, uid, "read", err.Error())
		skipSettingsBack(ctx, settings, pgSettings)
		return
	}
	same, err := sameContent(r.record, inKV)
	switch {
	case err != nil:
		committees.problem(ctx, uid, "read", err.Error())
	case same:
		committees.unchanged()
	case opts.DryRun:
		committees.written()
	default:
		if err := sink.UpdateBase(ctx, &model.Committee{CommitteeBase: *r.record}, kvRevision); err != nil {
			committees.problem(ctx, uid, writeKind(err), err.Error())
		} else {
			committees.written()
		}
	}
	if pgSettings != nil {
		copySettingsBack(ctx, sink, opts, settings, pgSettings)
	}
}

// createCommitteeBack creates the committee and its settings, if it has any, in KV.
func createCommitteeBack(ctx context.Context, sink port.KVRepairStore, opts backfillOptions, committees, settings *tracker, base *model.CommitteeBase, pgSettings *model.CommitteeSettings) {
	uid := base.UID
	if opts.DryRun {
		committees.written()
		if pgSettings != nil {
			settings.written()
		}
		return
	}
	if err := sink.Create(ctx, &model.Committee{CommitteeBase: *base, CommitteeSettings: pgSettings}); err != nil {
		committees.problem(ctx, uid, writeKind(err), err.Error())
		skipSettingsBack(ctx, settings, pgSettings)
		return
	}
	committees.written()
	if pgSettings != nil {
		settings.written()
	}
}

// skipSettingsBack counts the settings of a committee that could not be copied, as skipSettings does.
func skipSettingsBack(ctx context.Context, settings *tracker, pgSettings *model.CommitteeSettings) {
	if pgSettings != nil {
		settings.problem(ctx, pgSettings.UID, "skipped", "its committee could not be copied")
	}
}

// copySettingsBack handles the settings of a committee that already exists in KV.
func copySettingsBack(ctx context.Context, sink port.KVRepairStore, opts backfillOptions, settings *tracker, pgSettings *model.CommitteeSettings) {
	uid := pgSettings.UID
	held, kvRevision, err := sink.GetSettings(ctx, uid)
	switch {
	case isNotFound(err):
		settings.problem(ctx, uid, "missing", "the committee exists in KV without settings, which can only be created with a committee")
		return
	case err != nil:
		settings.problem(ctx, uid, "read", err.Error())
		return
	}
	same, err := sameContent(pgSettings, held)
	switch {
	case err != nil:
		settings.problem(ctx, uid, "read", err.Error())
	case same:
		settings.unchanged()
	case opts.DryRun:
		settings.written()
	default:
		if err := sink.UpdateSetting(ctx, pgSettings, kvRevision); err != nil {
			settings.problem(ctx, uid, writeKind(err), err.Error())
		} else {
			settings.written()
		}
	}
}

// sameContent reports whether two records say the same, apart from their revisions.
func sameContent(a, b any) (bool, error) {
	da, err := digestOf(a)
	if err != nil {
		return false, err
	}
	db, err := digestOf(b)
	if err != nil {
		return false, err
	}
	return da == db, nil
}

// reverseCopy copies one entity that belongs to a committee.
type reverseCopy struct {
	entity string
	copy   func(ctx context.Context, opts backfillOptions, t *tracker, committees map[string]struct{}) error
}

func reverseOf[T any](entity string, spec reverseSpec[T]) reverseCopy {
	return reverseCopy{entity, func(ctx context.Context, opts backfillOptions, t *tracker, c map[string]struct{}) error {
		return copyBack(ctx, spec, opts, t, c)
	}}
}

// reverseCopies lists the entities that belong to a committee, in the order a run copies them.
func reverseCopies(src reverseSource, sink port.KVRepairStore) []reverseCopy {
	return []reverseCopy{
		reverseOf(entityMembers, memberBackSpec(src, sink)),
		reverseOf(entityInvites, inviteBackSpec(src, sink)),
		reverseOf(entityApplications, applicationBackSpec(src, sink)),
		reverseOf(entityLinks, linkBackSpec(src, sink)),
		reverseOf(entityFolders, folderBackSpec(src, sink)),
		reverseOf(entityDocuments, documentBackSpec(src, sink)),
		reverseOf(entityBriefs, briefBackSpec(src, sink)),
		reverseOf(entityThrottles, throttleBackSpec(src, sink)),
	}
}

func memberBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.CommitteeMember] {
	return reverseSpec[*model.CommitteeMember]{
		each:         src.EachMemberExport,
		uid:          func(m *model.CommitteeMember) string { return m.UID },
		committeeUID: func(m *model.CommitteeMember) string { return m.CommitteeUID },
		lookup: func(ctx context.Context, m *model.CommitteeMember) (*model.CommitteeMember, uint64, error) {
			return sink.GetMember(ctx, m.UID)
		},
		create: sink.CreateMember,
		update: func(ctx context.Context, m *model.CommitteeMember, kvRevision uint64) error {
			_, err := sink.UpdateMember(ctx, m, kvRevision)
			return err
		},
	}
}

func inviteBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.CommitteeInvite] {
	return reverseSpec[*model.CommitteeInvite]{
		each:         src.EachInviteExport,
		uid:          func(i *model.CommitteeInvite) string { return i.UID },
		committeeUID: func(i *model.CommitteeInvite) string { return i.CommitteeUID },
		lookup: func(ctx context.Context, i *model.CommitteeInvite) (*model.CommitteeInvite, uint64, error) {
			return sink.GetInvite(ctx, i.UID)
		},
		create: sink.CreateInvite,
		update: sink.UpdateInvite,
	}
}

func applicationBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.CommitteeApplication] {
	return reverseSpec[*model.CommitteeApplication]{
		each:         src.EachApplicationExport,
		uid:          func(a *model.CommitteeApplication) string { return a.UID },
		committeeUID: func(a *model.CommitteeApplication) string { return a.CommitteeUID },
		lookup: func(ctx context.Context, a *model.CommitteeApplication) (*model.CommitteeApplication, uint64, error) {
			return sink.GetApplication(ctx, a.UID)
		},
		create: sink.CreateApplication,
		update: sink.UpdateApplication,
	}
}

func linkBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.CommitteeLink] {
	return reverseSpec[*model.CommitteeLink]{
		each:         src.EachLinkExport,
		uid:          func(l *model.CommitteeLink) string { return l.UID },
		committeeUID: func(l *model.CommitteeLink) string { return l.CommitteeUID },
		lookup: func(ctx context.Context, l *model.CommitteeLink) (*model.CommitteeLink, uint64, error) {
			return sink.GetLink(ctx, l.CommitteeUID, l.UID)
		},
		create: sink.CreateLink,
		update: sink.UpdateLink,
	}
}

func folderBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.CommitteeLinkFolder] {
	return reverseSpec[*model.CommitteeLinkFolder]{
		each:         src.EachFolderExport,
		uid:          func(f *model.CommitteeLinkFolder) string { return f.UID },
		committeeUID: func(f *model.CommitteeLinkFolder) string { return f.CommitteeUID },
		lookup: func(ctx context.Context, f *model.CommitteeLinkFolder) (*model.CommitteeLinkFolder, uint64, error) {
			return sink.GetLinkFolder(ctx, f.CommitteeUID, f.UID)
		},
		create: sink.CreateLinkFolder,
		update: sink.UpdateLinkFolder,
	}
}

// documentBackSpec copies the metadata of the documents; the files stay in the Object Store, which the move to
// PostgreSQL did not touch.
func documentBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.CommitteeDocument] {
	return reverseSpec[*model.CommitteeDocument]{
		each:         src.EachDocumentExport,
		uid:          func(d *model.CommitteeDocument) string { return d.UID },
		committeeUID: func(d *model.CommitteeDocument) string { return d.CommitteeUID },
		lookup: func(ctx context.Context, d *model.CommitteeDocument) (*model.CommitteeDocument, uint64, error) {
			return sink.GetDocumentMetadata(ctx, d.CommitteeUID, d.UID)
		},
		create: sink.CreateDocumentMetadata,
		update: sink.UpdateDocumentMetadata,
	}
}

// briefBackSpec copies the weekly briefs. KV finds a brief by its committee and window, as readers do, and holds one
// brief per window, as PostgreSQL does, so the order they are copied in does not matter: a window that KV already holds
// for another brief, when the copy looks it up, is a constraint. KV stamps the update time of a brief
// itself on every put, so it is not compared.
func briefBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.GroupWeeklyBrief] {
	return reverseSpec[*model.GroupWeeklyBrief]{
		each:         src.EachBriefExport,
		uid:          func(b *model.GroupWeeklyBrief) string { return b.UID },
		committeeUID: func(b *model.GroupWeeklyBrief) string { return b.CommitteeUID },
		normalize:    briefContent,
		lookup: func(ctx context.Context, b *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, uint64, error) {
			existing, _, err := sink.GetGroupWeeklyBriefForWindow(ctx, b.CommitteeUID, *b)
			switch {
			case err != nil:
				return nil, 0, err
			case existing == nil:
				return nil, 0, errs.NewNotFound("weekly brief not found")
			case existing.UID != b.UID:
				return nil, 0, errs.NewConflict(fmt.Sprintf("the window already has another brief, uid %s", existing.UID))
			}
			return existing, existing.Revision, nil
		},
		// KV has no guarded create for a brief: a put without a revision overwrites, and moves the window index. A brief
		// another writer stores between the lookup and the create is therefore replaced, which is why the copy needs no
		// writes in flight. A replace is guarded by the revision.
		create: func(ctx context.Context, b *model.GroupWeeklyBrief) error {
			b.Revision = 0
			_, err := sink.PutGroupWeeklyBrief(ctx, b)
			return err
		},
		update: func(ctx context.Context, b *model.GroupWeeklyBrief, kvRevision uint64) error {
			b.Revision = kvRevision
			_, err := sink.PutGroupWeeklyBrief(ctx, b)
			return lostWrite(err, briefHeld)
		},
	}
}

// throttleBackSpec copies the weekly-brief throttle entries, which both stores key by committee and window.
func throttleBackSpec(src reverseSource, sink port.KVRepairStore) reverseSpec[*model.GroupWeeklyBriefThrottle] {
	const held = "the weekly-brief throttle entry was written by another writer during the run"
	return reverseSpec[*model.GroupWeeklyBriefThrottle]{
		each: src.EachThrottleExport,
		uid: func(t *model.GroupWeeklyBriefThrottle) string {
			return t.CommitteeUID + "." + model.WindowDateKey(t.WindowStart)
		},
		committeeUID: func(t *model.GroupWeeklyBriefThrottle) string { return t.CommitteeUID },
		lookup: func(ctx context.Context, t *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, uint64, error) {
			existing, err := sink.GetGroupWeeklyBriefThrottle(ctx, t.CommitteeUID, t.WindowStart)
			switch {
			case err != nil:
				return nil, 0, err
			case existing == nil:
				return nil, 0, errs.NewNotFound("weekly-brief throttle entry not found")
			}
			return existing, existing.Revision, nil
		},
		create: func(ctx context.Context, t *model.GroupWeeklyBriefThrottle) error {
			t.Revision = 0
			_, err := sink.PutGroupWeeklyBriefThrottle(ctx, t)
			return lostWrite(err, held)
		},
		update: func(ctx context.Context, t *model.GroupWeeklyBriefThrottle, kvRevision uint64) error {
			t.Revision = kvRevision
			_, err := sink.PutGroupWeeklyBriefThrottle(ctx, t)
			return lostWrite(err, held)
		},
	}
}
