// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

var (
	_ port.GroupWeeklyBriefReader = (*Store)(nil)
	_ port.GroupWeeklyBriefWriter = (*Store)(nil)
)

// briefColumns and throttleColumns are the columns the store writes, in the order briefValues and
// throttleValues return them. The revision is not listed: it comes from the sequence.
var (
	briefColumns = []string{
		"uid", "committee_uid", "window_start", "window_end", "state", "error_reason", "brief_text", "source_refs",
		"prompt_version", "model", "regeneration_count", "private_source_present", "last_edited_at", "last_edited_by",
		"created_at", "updated_at",
	}
	throttleColumns = []string{
		"committee_uid", "window_start", "window_end", "generates_used", "regenerations_used", "window_resets_at",
	}
)

// The KV buckets key a brief and its throttle by <committee_uid>.<yyyymmdd of the window start, in
// UTC>. The schema has a unique index on the same expression, so a window is found by committee and
// the UTC date of window_start, passed as text and cast.
const windowDate = "(window_start AT TIME ZONE 'UTC')::date = $2::date"

var (
	selectBrief = "SELECT " + columnList(briefColumns) + ", revision FROM group_weekly_briefs"
	// A put without a revision replaces the brief with the same uid, as a KV Put does.
	upsertBrief = insertStatement("group_weekly_briefs", briefColumns) +
		" ON CONFLICT (uid) DO UPDATE SET " + fromExcluded(append(briefColumns[1:len(briefColumns):len(briefColumns)], "revision")) + " RETURNING revision"
	updateBrief = "UPDATE group_weekly_briefs SET " + updateAssignments(briefColumns[1:], 3) +
		" WHERE uid = $1 AND revision = $2 RETURNING revision"

	selectThrottle = "SELECT " + columnList(throttleColumns) + ", revision FROM group_weekly_brief_throttle" +
		" WHERE committee_uid = $1 AND " + windowDate
	insertThrottle = insertStatement("group_weekly_brief_throttle", throttleColumns) + " RETURNING revision"
	// $1 is the committee, $2 the window date and $3 the revision; the new values start at $4.
	updateThrottle = "UPDATE group_weekly_brief_throttle SET " + updateAssignments(throttleColumns[1:], 4) +
		" WHERE committee_uid = $1 AND " + windowDate + " AND revision = $3 RETURNING revision"
)

// fromExcluded returns "col = EXCLUDED.col" for each column, for an INSERT ... ON CONFLICT DO UPDATE.
func fromExcluded(columns []string) string {
	assigned := make([]string, len(columns))
	for i, column := range columns {
		assigned[i] = column + " = EXCLUDED." + column
	}
	return strings.Join(assigned, ", ")
}

// windowDay is the KV key's date: the UTC day of the window start, as yyyy-mm-dd for the SQL cast.
func windowDay(windowStart time.Time) string {
	return windowStart.UTC().Format("2006-01-02")
}

// briefIndexKey is the KV key of a brief's index and throttle entries, <committee_uid>.<yyyymmdd>,
// with the characters KV keys cannot hold replaced by dots.
func briefIndexKey(committeeUID, windowYYYYMMDD string) string {
	return strings.NewReplacer("/", ".", ":", ".", " ", ".").Replace(committeeUID + "." + windowYYYYMMDD)
}

func briefValues(b *model.GroupWeeklyBrief) ([]any, error) {
	sourceRefs, err := jsonArray(b.SourceRefs)
	if err != nil {
		return nil, err
	}
	var lastEditedAt any // NULL when the brief was never edited
	if b.LastEditedAt != nil {
		lastEditedAt = *b.LastEditedAt
	}
	return []any{
		b.UID, b.CommitteeUID, b.WindowStart, b.WindowEnd, string(b.State), b.ErrorReason, b.BriefText, sourceRefs,
		b.PromptVersion, b.Model, b.RegenerationCount, b.PrivateSourcePresent, lastEditedAt, b.LastEditedBy,
		b.CreatedAt, b.UpdatedAt,
	}, nil
}

func scanBrief(row pgx.Row) (*model.GroupWeeklyBrief, error) {
	b := &model.GroupWeeklyBrief{}
	var state string
	var sourceRefs []byte
	var lastEditedAt *time.Time
	var revision int64
	if err := row.Scan(
		&b.UID, &b.CommitteeUID, &b.WindowStart, &b.WindowEnd, &state, &b.ErrorReason, &b.BriefText, &sourceRefs,
		&b.PromptVersion, &b.Model, &b.RegenerationCount, &b.PrivateSourcePresent, &lastEditedAt, &b.LastEditedBy,
		&b.CreatedAt, &b.UpdatedAt, &revision,
	); err != nil {
		return nil, err
	}
	b.State = model.GroupWeeklyBriefState(state)
	b.WindowStart, b.WindowEnd = b.WindowStart.UTC(), b.WindowEnd.UTC()
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	if lastEditedAt != nil {
		edited := lastEditedAt.UTC()
		b.LastEditedAt = &edited
	}
	var err error
	if b.SourceRefs, err = decodeArray[model.SourceRef](sourceRefs); err != nil {
		return nil, err
	}
	b.Revision = uint64(revision)
	return b, nil
}

func throttleValues(t *model.GroupWeeklyBriefThrottle) []any {
	return []any{t.CommitteeUID, t.WindowStart, t.WindowEnd, t.GeneratesUsed, t.RegenerationsUsed, t.WindowResetsAt}
}

func scanThrottle(row pgx.Row) (*model.GroupWeeklyBriefThrottle, error) {
	t := &model.GroupWeeklyBriefThrottle{}
	var revision int64
	if err := row.Scan(&t.CommitteeUID, &t.WindowStart, &t.WindowEnd, &t.GeneratesUsed, &t.RegenerationsUsed, &t.WindowResetsAt, &revision); err != nil {
		return nil, err
	}
	t.WindowStart, t.WindowEnd, t.WindowResetsAt = t.WindowStart.UTC(), t.WindowEnd.UTC(), t.WindowResetsAt.UTC()
	t.Revision = uint64(revision)
	return t, nil
}

// GetGroupWeeklyBriefForWindow returns the committee's brief for the UTC day of window.WindowStart
// and the JSON of the throttle entry for the same window (nil when there is none). A miss is not
// an error. Like KV, a window with no brief returns no throttle either, and a failed throttle lookup is
// logged and leaves the throttle out rather than failing the read.
func (s *Store) GetGroupWeeklyBriefForWindow(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	brief, err := scanBrief(s.pool.QueryRow(ctx, selectBrief+" WHERE committee_uid = $1 AND "+windowDate, committeeUID, windowDay(window.WindowStart)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, unexpected("failed to read weekly brief", err)
	}
	// The throttle is advisory metadata for GET /current: as in KV, a failed lookup does not fail the brief read.
	throttle, err := s.GetGroupWeeklyBriefThrottle(ctx, committeeUID, window.WindowStart)
	if err != nil {
		slog.WarnContext(ctx, "failed to read weekly-brief throttle entry", "committee_uid", committeeUID, "error", err)
		return brief, nil, nil
	}
	if throttle == nil {
		return brief, nil, nil
	}
	throttleJSON, err := json.Marshal(throttle)
	if err != nil {
		return nil, nil, unexpected("failed to encode weekly-brief throttle entry", err)
	}
	return brief, throttleJSON, nil
}

// ListGroupWeeklyBriefIndexKeys returns the KV index key of every brief, <committee_uid>.<yyyymmdd>.
func (s *Store) ListGroupWeeklyBriefIndexKeys(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT committee_uid, to_char((window_start AT TIME ZONE 'UTC')::date, 'YYYYMMDD') FROM group_weekly_briefs ORDER BY 1, 2")
	if err != nil {
		return nil, unexpected("failed to list weekly-brief index keys", err)
	}
	keys, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var committeeUID, day string
		if err := row.Scan(&committeeUID, &day); err != nil {
			return "", err
		}
		return briefIndexKey(committeeUID, day), nil
	})
	if err != nil {
		return nil, unexpected("failed to list weekly-brief index keys", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	return keys, nil
}

// briefWindowConflict is what KV reports for a lost compare-and-swap, a service-unavailable error
// the caller answers by re-reading and retrying.
func briefWindowConflict(message string, err error) error {
	return errs.NewServiceUnavailable(message, err)
}

// PutGroupWeeklyBrief writes the brief and returns it with its new revision. Like KV it fills in a
// missing UID and creation time, sets the update time, and writes unconditionally when the revision is
// 0 (replacing a brief with the same uid) and only when the revision is current otherwise; a lost
// compare-and-swap is a service-unavailable error to retry. Unlike KV, which lets a second brief with a
// different uid take over a window's index entry, a window holds one brief: a second one is the same
// retryable error, and the caller finds the first by reading the window again.
func (s *Store) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	if brief == nil {
		return nil, errs.NewValidation("brief is required")
	}
	if brief.CommitteeUID == "" {
		return nil, errs.NewValidation("committee_uid is required")
	}
	if brief.WindowStart.IsZero() {
		return nil, errs.NewValidation("window_start is required")
	}
	if brief.UID == "" {
		brief.UID = uuid.NewString()
	}
	// The column keeps microseconds, so stamp that precision: the brief returned is then what a read returns.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if brief.CreatedAt.IsZero() {
		brief.CreatedAt = now
	}
	brief.UpdatedAt = now

	values, err := briefValues(brief)
	if err != nil {
		return nil, unexpected("failed to encode weekly brief", err)
	}
	query, args := upsertBrief, withRevisionArg(ctx, primaryRevision, values)
	if brief.Revision > 0 {
		query, args = updateBrief, withRevisionArg(ctx, primaryRevision, append([]any{values[0], revisionArg(brief.Revision)}, values[1:]...))
	}
	var revision int64
	err = s.pool.QueryRow(ctx, query, args...).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, briefWindowConflict("weekly brief CAS conflict — retry", nil)
	}
	if _, ok := uniqueViolation(err); ok {
		return nil, briefWindowConflict("weekly brief window already has a brief — retry", err)
	}
	if err != nil {
		return nil, unexpected("failed to write weekly brief", err)
	}
	brief.Revision = uint64(revision)
	return brief, nil
}

// GetGroupWeeklyBriefThrottle returns the throttle entry of the committee's window, or nil when there is none.
func (s *Store) GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	throttle, err := scanThrottle(s.pool.QueryRow(ctx, selectThrottle, committeeUID, windowDay(windowStart)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unexpected("failed to read weekly-brief throttle entry", err)
	}
	return throttle, nil
}

// PutGroupWeeklyBriefThrottle creates the entry when its revision is 0 and replaces it when the
// revision is current, and returns it with its new revision. Callers racing on the same revision, or
// creating the same window, lose with the service-unavailable error KV reports, and retry by reading again.
func (s *Store) PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	if throttle == nil {
		return nil, errs.NewValidation("throttle is required")
	}
	if throttle.CommitteeUID == "" {
		return nil, errs.NewValidation("committee_uid is required")
	}
	if throttle.WindowStart.IsZero() {
		return nil, errs.NewValidation("window_start is required")
	}
	values := throttleValues(throttle)
	query, args := insertThrottle, withRevisionArg(ctx, primaryRevision, values)
	if throttle.Revision > 0 {
		query = updateThrottle
		args = withRevisionArg(ctx, primaryRevision, append([]any{throttle.CommitteeUID, windowDay(throttle.WindowStart), revisionArg(throttle.Revision)}, values[1:]...))
	}
	var revision int64
	err := s.pool.QueryRow(ctx, query, args...).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, briefWindowConflict("weekly-brief throttle CAS conflict — retry", nil)
	}
	if _, ok := uniqueViolation(err); ok {
		return nil, briefWindowConflict("weekly-brief throttle CAS conflict — retry", err)
	}
	if err != nil {
		return nil, unexpected("failed to write weekly-brief throttle entry", err)
	}
	throttle.Revision = uint64(revision)
	return throttle, nil
}
