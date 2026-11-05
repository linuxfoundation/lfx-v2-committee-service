// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// The Each...Export methods stream every row of a table with its revision, for the tooling that compares the
// PostgreSQL data with KV (committee-cli storage verify). They only read, hold no more than a row at a time, and
// stop at the first error fn returns. fn runs while the query is open, so a call back into the store from fn uses
// a second connection from the pool. A row that cannot be read ends the stream with an error.

// selectAllThrottles is selectThrottle without its committee and window.
const selectAllThrottles = "SELECT " + "committee_uid, window_start, window_end, generates_used, regenerations_used, window_resets_at" + ", revision FROM group_weekly_brief_throttle"

// eachRow streams the rows of query, which selects what scan reads, to fn.
func eachRow[T any](ctx context.Context, s *Store, what, query string, scan func(pgx.Row) (T, uint64, error), fn func(T, uint64) error) error {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return unexpected("failed to stream "+what, err)
	}
	defer rows.Close()
	for rows.Next() {
		record, revision, err := scan(rows)
		if err != nil {
			return unexpected("failed to read a row while streaming "+what, err)
		}
		if err := fn(record, revision); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return unexpected("failed to stream "+what, err)
	}
	return nil
}

// EachCommitteeExport streams every committee, without its settings.
func (s *Store) EachCommitteeExport(ctx context.Context, fn func(*model.CommitteeBase, uint64) error) error {
	return eachRow(ctx, s, "committees", selectCommittee, scanCommittee, fn)
}

// EachSettingsExport streams the settings of every committee.
func (s *Store) EachSettingsExport(ctx context.Context, fn func(*model.CommitteeSettings, uint64) error) error {
	return eachRow(ctx, s, "committee settings", selectSettings, scanSettings, fn)
}

// EachMemberExport streams every committee member.
func (s *Store) EachMemberExport(ctx context.Context, fn func(*model.CommitteeMember, uint64) error) error {
	return eachRow(ctx, s, "committee members", selectMember, scanMember, fn)
}

// EachInviteExport streams every committee invite.
func (s *Store) EachInviteExport(ctx context.Context, fn func(*model.CommitteeInvite, uint64) error) error {
	return eachRow(ctx, s, "committee invites", selectInvite, scanInvite, fn)
}

// EachApplicationExport streams every committee application.
func (s *Store) EachApplicationExport(ctx context.Context, fn func(*model.CommitteeApplication, uint64) error) error {
	return eachRow(ctx, s, "committee applications", selectApplication, scanApplication, fn)
}

// EachLinkExport streams every committee link.
func (s *Store) EachLinkExport(ctx context.Context, fn func(*model.CommitteeLink, uint64) error) error {
	return eachRow(ctx, s, "committee links", selectLink, scanLink, fn)
}

// EachFolderExport streams every committee link folder.
func (s *Store) EachFolderExport(ctx context.Context, fn func(*model.CommitteeLinkFolder, uint64) error) error {
	return eachRow(ctx, s, "committee link folders", selectFolder, scanFolder, fn)
}

// EachDocumentExport streams the metadata of every committee document.
func (s *Store) EachDocumentExport(ctx context.Context, fn func(*model.CommitteeDocument, uint64) error) error {
	return eachRow(ctx, s, "committee documents", selectDocument, scanDocument, fn)
}

// EachBriefExport streams every weekly brief, with Revision set.
func (s *Store) EachBriefExport(ctx context.Context, fn func(*model.GroupWeeklyBrief, uint64) error) error {
	return eachRow(ctx, s, "weekly briefs", selectBrief, func(row pgx.Row) (*model.GroupWeeklyBrief, uint64, error) {
		brief, err := scanBrief(row)
		if err != nil {
			return nil, 0, err
		}
		return brief, brief.Revision, nil
	}, fn)
}

// EachThrottleExport streams every weekly-brief throttle entry, with Revision set.
func (s *Store) EachThrottleExport(ctx context.Context, fn func(*model.GroupWeeklyBriefThrottle, uint64) error) error {
	return eachRow(ctx, s, "weekly-brief throttles", selectAllThrottles, func(row pgx.Row) (*model.GroupWeeklyBriefThrottle, uint64, error) {
		throttle, err := scanThrottle(row)
		if err != nil {
			return nil, 0, err
		}
		return throttle, throttle.Revision, nil
	}, fn)
}
