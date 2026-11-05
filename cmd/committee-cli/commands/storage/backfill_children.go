// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// childSpec is what the backfill needs to know about an entity that belongs to a committee: how to read it from
// KV, how to tell its uid and committee, and how to look it up, create it and update it in PostgreSQL. T is the
// record, a pointer to the model.
type childSpec[T any] struct {
	// each streams the KV records with the revision each was read at.
	each func(ctx context.Context, fn func(record T, revision uint64) error) error
	// uid names the record in the report; committeeUID is the committee it belongs to.
	uid, committeeUID func(T) string
	// revision returns the revision the record has in PostgreSQL: errs.NotFound when it has no row, errs.Conflict
	// when the row that stands in its way is another record.
	revision func(ctx context.Context, record T) (uint64, error)
	create   func(ctx context.Context, record T) error
	// update replaces the row, guarded by the revision revision returned.
	update func(ctx context.Context, record T, pgRevision uint64) error
}

// childRecord is a KV record with the revision it has in KV.
type childRecord[T any] struct {
	record   T
	revision uint64
}

// copyChildren copies every record of an entity whose committee exists in KV, or every record with
// IncludeOrphans. KV never removed a deleted committee's children, so a run usually finds some orphans; they are
// reported with their uids and not copied.
func copyChildren[T any](ctx context.Context, spec childSpec[T], opts backfillOptions, t *tracker, committees map[string]struct{}) error {
	return stream(ctx, opts.Workers, opts.BatchSize,
		func(emit func(childRecord[T]) error) error {
			return spec.each(ctx, func(record T, revision uint64) error {
				return emit(childRecord[T]{record: record, revision: revision})
			})
		},
		func(r childRecord[T]) { copyChild(ctx, spec, opts, t, committees, r) })
}

func copyChild[T any](ctx context.Context, spec childSpec[T], opts backfillOptions, t *tracker, committees map[string]struct{}, r childRecord[T]) {
	uid := spec.uid(r.record)
	t.scanned(ctx, r.revision)
	if _, ok := committees[spec.committeeUID(r.record)]; !ok && !opts.IncludeOrphans {
		t.orphan(ctx, uid)
		return
	}

	pgRevision, err := spec.revision(ctx, r.record)
	switch {
	case isNotFound(err):
		if opts.DryRun {
			t.written()
			return
		}
		writeCtx, err := postgres.WithRevision(ctx, r.revision)
		if err == nil {
			err = spec.create(writeCtx, r.record)
		}
		if err != nil {
			t.problem(ctx, uid, writeKind(err), err.Error())
			return
		}
		t.written()
	case err != nil:
		// A row that stands in the way is a constraint, as when the write itself is refused.
		kind := writeKind(err)
		if kind == "write" {
			kind = "read"
		}
		t.problem(ctx, uid, kind, err.Error())
	case r.revision == pgRevision:
		t.unchanged()
	case r.revision < pgRevision:
		t.problem(ctx, uid, "ahead", "PostgreSQL has a newer revision than KV")
	case opts.DryRun:
		t.written()
	default:
		writeCtx, err := postgres.WithRevision(ctx, r.revision)
		if err == nil {
			err = spec.update(writeCtx, r.record, pgRevision)
		}
		if err != nil {
			t.problem(ctx, uid, writeKind(err), err.Error())
			return
		}
		t.written()
	}
}

func memberSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.CommitteeMember] {
	return childSpec[*model.CommitteeMember]{
		each:         src.EachMemberExport,
		uid:          func(m *model.CommitteeMember) string { return m.UID },
		committeeUID: func(m *model.CommitteeMember) string { return m.CommitteeUID },
		revision: func(ctx context.Context, m *model.CommitteeMember) (uint64, error) {
			_, revision, err := sink.GetMember(ctx, m.UID)
			return revision, err
		},
		create: sink.CreateMember,
		update: func(ctx context.Context, m *model.CommitteeMember, pgRevision uint64) error {
			_, err := sink.UpdateMember(ctx, m, pgRevision)
			return err
		},
	}
}

func inviteSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.CommitteeInvite] {
	return childSpec[*model.CommitteeInvite]{
		each:         src.EachInviteExport,
		uid:          func(i *model.CommitteeInvite) string { return i.UID },
		committeeUID: func(i *model.CommitteeInvite) string { return i.CommitteeUID },
		revision: func(ctx context.Context, i *model.CommitteeInvite) (uint64, error) {
			_, revision, err := sink.GetInvite(ctx, i.UID)
			return revision, err
		},
		create: sink.CreateInvite,
		update: sink.UpdateInvite,
	}
}

func applicationSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.CommitteeApplication] {
	return childSpec[*model.CommitteeApplication]{
		each:         src.EachApplicationExport,
		uid:          func(a *model.CommitteeApplication) string { return a.UID },
		committeeUID: func(a *model.CommitteeApplication) string { return a.CommitteeUID },
		revision: func(ctx context.Context, a *model.CommitteeApplication) (uint64, error) {
			_, revision, err := sink.GetApplication(ctx, a.UID)
			return revision, err
		},
		create: sink.CreateApplication,
		update: sink.UpdateApplication,
	}
}

func linkSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.CommitteeLink] {
	return childSpec[*model.CommitteeLink]{
		each:         src.EachLinkExport,
		uid:          func(l *model.CommitteeLink) string { return l.UID },
		committeeUID: func(l *model.CommitteeLink) string { return l.CommitteeUID },
		revision: func(ctx context.Context, l *model.CommitteeLink) (uint64, error) {
			_, revision, err := sink.GetLink(ctx, l.CommitteeUID, l.UID)
			return revision, err
		},
		create: sink.CreateLink,
		update: sink.UpdateLink,
	}
}

func folderSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.CommitteeLinkFolder] {
	return childSpec[*model.CommitteeLinkFolder]{
		each:         src.EachFolderExport,
		uid:          func(f *model.CommitteeLinkFolder) string { return f.UID },
		committeeUID: func(f *model.CommitteeLinkFolder) string { return f.CommitteeUID },
		revision: func(ctx context.Context, f *model.CommitteeLinkFolder) (uint64, error) {
			_, revision, err := sink.GetLinkFolder(ctx, f.CommitteeUID, f.UID)
			return revision, err
		},
		create: sink.CreateLinkFolder,
		update: sink.UpdateLinkFolder,
	}
}

// documentSpec copies the metadata of the documents. The files stay in the Object Store, which the move to
// PostgreSQL does not touch.
func documentSpec(src port.KVExportReader, sink backfillSink) childSpec[*model.CommitteeDocument] {
	return childSpec[*model.CommitteeDocument]{
		each:         src.EachDocumentExport,
		uid:          func(d *model.CommitteeDocument) string { return d.UID },
		committeeUID: func(d *model.CommitteeDocument) string { return d.CommitteeUID },
		revision: func(ctx context.Context, d *model.CommitteeDocument) (uint64, error) {
			_, revision, err := sink.GetDocumentMetadata(ctx, d.CommitteeUID, d.UID)
			return revision, err
		},
		create: sink.CreateDocumentMetadata,
		update: sink.UpdateDocumentMetadata,
	}
}
