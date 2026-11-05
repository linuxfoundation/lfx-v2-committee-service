// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package backfill

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// memberRecord is a member with the revision it has in KV.
type memberRecord struct {
	member   *model.CommitteeMember
	revision uint64
}

// copyMembers copies every member whose committee exists in KV, or every member with IncludeOrphans.
// KV never removed a deleted committee's members, so a run usually finds some orphans; they are reported
// with their uids and not copied.
func copyMembers(ctx context.Context, src port.KVExportReader, sink Sink, opts Options, members *tracker) error {
	committeeUIDs, err := src.ListAllUIDs(ctx)
	if err != nil {
		return err
	}
	exists := make(map[string]struct{}, len(committeeUIDs))
	for _, uid := range committeeUIDs {
		exists[uid] = struct{}{}
	}
	return stream(ctx, opts.Workers, opts.BatchSize,
		func(emit func(memberRecord) error) error {
			return src.EachMemberExport(ctx, func(m *model.CommitteeMember, revision uint64) error {
				return emit(memberRecord{member: m, revision: revision})
			})
		},
		func(r memberRecord) { copyMember(ctx, sink, opts, members, exists, r) })
}

func copyMember(ctx context.Context, sink Sink, opts Options, members *tracker, committees map[string]struct{}, r memberRecord) {
	uid := r.member.UID
	members.scanned(ctx, r.revision)
	if _, ok := committees[r.member.CommitteeUID]; !ok && !opts.IncludeOrphans {
		members.orphan(ctx, uid)
		return
	}

	_, pgRevision, err := sink.GetMember(ctx, uid)
	switch {
	case isNotFound(err):
		if opts.DryRun {
			members.written()
			return
		}
		writeCtx, err := postgres.WithRevision(ctx, r.revision)
		if err == nil {
			err = sink.CreateMember(writeCtx, r.member)
		}
		if err != nil {
			members.problem(ctx, uid, writeKind(err), err.Error())
			return
		}
		members.written()
	case err != nil:
		members.problem(ctx, uid, "read", err.Error())
	case r.revision == pgRevision:
		members.unchanged()
	case r.revision < pgRevision:
		members.problem(ctx, uid, "ahead", "PostgreSQL has a newer revision than KV")
	case opts.DryRun:
		members.written()
	default:
		writeCtx, err := postgres.WithRevision(ctx, r.revision)
		if err == nil {
			_, err = sink.UpdateMember(writeCtx, r.member, pgRevision)
		}
		if err != nil {
			members.problem(ctx, uid, writeKind(err), err.Error())
			return
		}
		members.written()
	}
}
