// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package backfill

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// copyCommittees copies every committee with its settings. A committee that does not exist yet is created
// with both revisions; one that exists is updated when KV is ahead, left alone when the revisions match,
// and reported when PostgreSQL is ahead, which a migration cannot reconcile.
func copyCommittees(ctx context.Context, src port.KVExportReader, sink Sink, opts Options, committees, settings *tracker) error {
	return stream(ctx, opts.Workers, opts.BatchSize,
		func(emit func(port.CommitteeExport) error) error { return src.EachCommitteeExport(ctx, emit) },
		func(e port.CommitteeExport) { copyCommittee(ctx, sink, opts, committees, settings, e) })
}

func copyCommittee(ctx context.Context, sink Sink, opts Options, committees, settings *tracker, e port.CommitteeExport) {
	uid := e.Base.UID
	committees.scanned(ctx, e.BaseRevision)
	if e.Settings != nil {
		settings.scanned(ctx, e.SettingsRevision)
	}

	_, pgRevision, err := sink.GetBase(ctx, uid)
	switch {
	case isNotFound(err):
		createCommittee(ctx, sink, opts, committees, settings, e)
		return
	case err != nil:
		committees.problem(ctx, uid, "read", err.Error())
		return
	}

	committee := &model.Committee{CommitteeBase: *e.Base}
	switch {
	case e.BaseRevision == pgRevision:
		committees.unchanged()
	case e.BaseRevision < pgRevision:
		committees.problem(ctx, uid, "ahead", "PostgreSQL has a newer revision than KV")
	case opts.DryRun:
		committees.written()
	default:
		writeCtx, err := postgres.WithRevision(ctx, e.BaseRevision)
		if err == nil {
			err = sink.UpdateBase(writeCtx, committee, pgRevision)
		}
		if err != nil {
			committees.problem(ctx, uid, writeKind(err), err.Error())
		} else {
			committees.written()
		}
	}

	if e.Settings != nil {
		copySettings(ctx, sink, opts, settings, e)
	}
}

// createCommittee creates the committee and its settings, if it has any, in one transaction.
func createCommittee(ctx context.Context, sink Sink, opts Options, committees, settings *tracker, e port.CommitteeExport) {
	uid := e.Base.UID
	if opts.DryRun {
		committees.written()
		if e.Settings != nil {
			settings.written()
		}
		return
	}
	writeCtx, err := postgres.WithRevision(ctx, e.BaseRevision)
	if err == nil && e.Settings != nil {
		writeCtx, err = postgres.WithSettingsRevision(writeCtx, e.SettingsRevision)
	}
	if err == nil {
		err = sink.Create(writeCtx, &model.Committee{CommitteeBase: *e.Base, CommitteeSettings: e.Settings})
	}
	if err != nil {
		committees.problem(ctx, uid, writeKind(err), err.Error())
		return
	}
	committees.written()
	if e.Settings != nil {
		settings.written()
	}
}

// copySettings handles the settings of a committee that already exists in PostgreSQL.
func copySettings(ctx context.Context, sink Sink, opts Options, settings *tracker, e port.CommitteeExport) {
	uid := e.Base.UID
	_, pgRevision, err := sink.GetSettings(ctx, uid)
	switch {
	case isNotFound(err):
		// The store creates settings only along with their committee.
		settings.problem(ctx, uid, "missing", "the committee exists in PostgreSQL without settings, which can only be created with a committee")
	case err != nil:
		settings.problem(ctx, uid, "read", err.Error())
	case e.SettingsRevision == pgRevision:
		settings.unchanged()
	case e.SettingsRevision < pgRevision:
		settings.problem(ctx, uid, "ahead", "PostgreSQL has a newer revision than KV")
	case opts.DryRun:
		settings.written()
	default:
		writeCtx, err := postgres.WithRevision(ctx, e.SettingsRevision)
		if err == nil {
			err = sink.UpdateSetting(writeCtx, e.Settings, pgRevision)
		}
		if err != nil {
			settings.problem(ctx, uid, writeKind(err), err.Error())
		} else {
			settings.written()
		}
	}
}
