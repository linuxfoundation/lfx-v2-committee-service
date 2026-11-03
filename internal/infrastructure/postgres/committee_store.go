// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

var (
	_ port.CommitteeBaseReader     = (*Store)(nil)
	_ port.CommitteeSettingsReader = (*Store)(nil)
	_ port.CommitteeBaseWriter     = (*Store)(nil)
	_ port.CommitteeSettingsWriter = (*Store)(nil)
)

// committeeColumns are the committees columns the store writes, in the order committeeValues
// returns them. uid comes first and the revision is not listed: it comes from the sequence.
var committeeColumns = []string{
	"uid", "project_uid", "project_name", "project_slug", "name", "category", "description",
	"website", "mailing_list", "chat_channel", "enable_voting", "sso_group_enabled", "sso_group_name",
	"requires_review", "public", "join_mode", "calendar_public", "display_name", "parent_uid",
	"repository", "scope", "deliverables", "key_dates", "external_sources", "charter",
	"total_members", "total_voting_repos", "has_mailing_list", "created_at", "updated_at",
}

// settingsColumns are the committee_settings columns the store writes, in settingsValues order.
var settingsColumns = []string{
	"committee_uid", "business_email_required", "show_meeting_attendees", "member_visibility",
	"chat_webhook_url", "has_chat_webhook", "last_reviewed_at", "last_reviewed_by", "writers",
	"auditors", "created_at", "updated_at",
}

var (
	selectCommittee = "SELECT " + columnList(committeeColumns) + ", revision FROM committees"
	insertCommittee = "INSERT INTO committees (" + columnList(committeeColumns) + ") VALUES (" + placeholders(len(committeeColumns)) + ")"
	// The first column (uid) identifies the row and is not assigned.
	updateCommittee = "UPDATE committees SET " + assignments(committeeColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"

	selectSettings = "SELECT " + columnList(settingsColumns) + ", revision FROM committee_settings"
	insertSettings = "INSERT INTO committee_settings (" + columnList(settingsColumns) + ") VALUES (" + placeholders(len(settingsColumns)) + ")"
	updateSettings = "UPDATE committee_settings SET " + assignments(settingsColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE committee_uid = $1 AND revision = $2"
)

// committeeValues returns the values for committeeColumns.
func committeeValues(c *model.CommitteeBase) ([]any, error) {
	scope, err := jsonArray(c.Scope)
	if err != nil {
		return nil, err
	}
	deliverables, err := jsonArray(c.Deliverables)
	if err != nil {
		return nil, err
	}
	keyDates, err := jsonArray(c.KeyDates)
	if err != nil {
		return nil, err
	}
	sources, err := jsonArray(c.ExternalSources)
	if err != nil {
		return nil, err
	}
	charter, err := jsonObject(c.Charter)
	if err != nil {
		return nil, err
	}
	return []any{
		c.UID, c.ProjectUID, c.ProjectName, c.ProjectSlug, c.Name, c.Category, c.Description,
		c.Website, c.MailingList, c.ChatChannel, c.EnableVoting, c.SSOGroupEnabled, c.SSOGroupName,
		c.RequiresReview, c.Public, c.JoinMode, c.Calendar.Public, c.DisplayName, c.ParentUID,
		c.Repository, scope, deliverables, keyDates, sources, charter,
		c.TotalMembers, c.TotalVotingRepos, c.HasMailingList, c.CreatedAt, c.UpdatedAt,
	}, nil
}

// scanCommittee reads a row selected with selectCommittee.
func scanCommittee(row pgx.Row) (*model.CommitteeBase, uint64, error) {
	c := &model.CommitteeBase{}
	var scope, deliverables, keyDates, sources, charter []byte
	var revision int64
	if err := row.Scan(
		&c.UID, &c.ProjectUID, &c.ProjectName, &c.ProjectSlug, &c.Name, &c.Category, &c.Description,
		&c.Website, &c.MailingList, &c.ChatChannel, &c.EnableVoting, &c.SSOGroupEnabled, &c.SSOGroupName,
		&c.RequiresReview, &c.Public, &c.JoinMode, &c.Calendar.Public, &c.DisplayName, &c.ParentUID,
		&c.Repository, &scope, &deliverables, &keyDates, &sources, &charter,
		&c.TotalMembers, &c.TotalVotingRepos, &c.HasMailingList, &c.CreatedAt, &c.UpdatedAt,
		&revision,
	); err != nil {
		return nil, 0, err
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	var err error
	if c.Scope, err = decodeArray[string](scope); err != nil {
		return nil, 0, fmt.Errorf("decode scope: %w", err)
	}
	if c.Deliverables, err = decodeArray[string](deliverables); err != nil {
		return nil, 0, fmt.Errorf("decode deliverables: %w", err)
	}
	if c.KeyDates, err = decodeArray[model.KeyDate](keyDates); err != nil {
		return nil, 0, fmt.Errorf("decode key_dates: %w", err)
	}
	if c.ExternalSources, err = decodeArray[model.ExternalSource](sources); err != nil {
		return nil, 0, fmt.Errorf("decode external_sources: %w", err)
	}
	if c.Charter, err = decodeObject[model.Charter](charter); err != nil {
		return nil, 0, fmt.Errorf("decode charter: %w", err)
	}
	return c, uint64(revision), nil
}

// settingsValues returns the values for settingsColumns.
func settingsValues(s *model.CommitteeSettings) ([]any, error) {
	writers, err := jsonList(s.Writers)
	if err != nil {
		return nil, err
	}
	auditors, err := jsonList(s.Auditors)
	if err != nil {
		return nil, err
	}
	return []any{
		s.UID, s.BusinessEmailRequired, s.ShowMeetingAttendees, s.MemberVisibility,
		s.ChatWebhookURL, s.HasChatWebhook, s.LastReviewedAt, s.LastReviewedBy, writers,
		auditors, s.CreatedAt, s.UpdatedAt,
	}, nil
}

// scanSettings reads a row selected with selectSettings.
func scanSettings(row pgx.Row) (*model.CommitteeSettings, uint64, error) {
	s := &model.CommitteeSettings{}
	var writers, auditors []byte
	var revision int64
	if err := row.Scan(
		&s.UID, &s.BusinessEmailRequired, &s.ShowMeetingAttendees, &s.MemberVisibility,
		&s.ChatWebhookURL, &s.HasChatWebhook, &s.LastReviewedAt, &s.LastReviewedBy, &writers,
		&auditors, &s.CreatedAt, &s.UpdatedAt,
		&revision,
	); err != nil {
		return nil, 0, err
	}
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	var err error
	if s.Writers, err = decodeList[model.CommitteeUser](writers); err != nil {
		return nil, 0, fmt.Errorf("decode writers: %w", err)
	}
	if s.Auditors, err = decodeList[model.CommitteeUser](auditors); err != nil {
		return nil, 0, fmt.Errorf("decode auditors: %w", err)
	}
	return s, uint64(revision), nil
}

// committeeConflict translates a unique violation on committees into the conflict the KV store
// reports for the same lookup key. It returns nil when err is not one of them.
func committeeConflict(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok {
		return nil
	}
	switch constraint {
	case "committees_project_name_key":
		return errs.NewConflict("committee with the same name for the project already exists")
	case "committees_sso_group_name_key":
		return errs.NewConflict("committee with the same SSO group name already exists", errs.ErrSSOGroupNameTaken)
	default:
		return errs.NewConflict("committee already exists")
	}
}

// Create persists the committee and its optional settings in one transaction, so a failed create
// leaves nothing behind.
func (s *Store) Create(ctx context.Context, committee *model.Committee) error {
	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}
	values, err := committeeValues(&committee.CommitteeBase)
	if err != nil {
		return unexpected("failed to encode committee", err)
	}
	var settings []any
	if committee.CommitteeSettings != nil {
		committee.CommitteeSettings.UID = committee.CommitteeBase.UID
		if settings, err = settingsValues(committee.CommitteeSettings); err != nil {
			return unexpected("failed to encode committee settings", err)
		}
	}

	err = s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, insertCommittee, values...); err != nil {
			if conflict := committeeConflict(err); conflict != nil {
				return conflict
			}
			return unexpected("failed to create committee", err)
		}
		if settings == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, insertSettings, settings...); err != nil {
			if _, ok := uniqueViolation(err); ok {
				return errs.NewConflict("committee settings already exist")
			}
			return unexpected("failed to create committee settings", err)
		}
		return nil
	})
	return err
}

// GetBase returns the committee and its revision.
func (s *Store) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	if uid == "" {
		return nil, 0, errs.NewValidation("committee UID cannot be empty")
	}
	base, revision, err := scanCommittee(s.pool.QueryRow(ctx, selectCommittee+" WHERE uid = $1", uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get committee", err)
	}
	return base, revision, nil
}

// ListAllUIDs returns the UID of every committee.
func (s *Store) ListAllUIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT uid FROM committees ORDER BY uid")
	if err != nil {
		return nil, unexpected("failed to list committees", err)
	}
	uids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, unexpected("failed to list committees", err)
	}
	return uids, nil
}

// FindUIDByProjectAndName returns the UID of the committee with the name in the project.
func (s *Store) FindUIDByProjectAndName(ctx context.Context, projectUID, name string) (string, error) {
	var uid string
	err := s.pool.QueryRow(ctx, "SELECT uid FROM committees WHERE project_uid = $1 AND name = $2", projectUID, name).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errs.NewNotFound("committee not found for project and name")
	}
	if err != nil {
		return "", unexpected("failed to find committee by project and name", err)
	}
	return uid, nil
}

// GetSettings returns the committee's settings and their revision.
func (s *Store) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	if uid == "" {
		return nil, 0, errs.NewValidation("committee UID cannot be empty")
	}
	settings, revision, err := scanSettings(s.pool.QueryRow(ctx, selectSettings+" WHERE committee_uid = $1", uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("committee settings not found", fmt.Errorf("committee UID: %s", uid))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get committee settings", err)
	}
	return settings, revision, nil
}

// UpdateBase replaces the committee base when the revision is still current. A stale revision is a
// conflict; a missing committee is not found. A changed name or SSO group name is checked by the
// unique indexes, and the old name is free again as soon as the update commits.
func (s *Store) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}
	values, err := committeeValues(&committee.CommitteeBase)
	if err != nil {
		return unexpected("failed to encode committee", err)
	}
	// $1 is the uid and $2 the revision; the rest follow committeeColumns without the uid.
	args := append([]any{values[0], int64(revision)}, values[1:]...)

	tag, err := s.pool.Exec(ctx, updateCommittee, args...)
	if err != nil {
		if conflict := committeeConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to update committee base", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, committeeExists, "committee UID", committee.CommitteeBase.UID, "committee not found", "committee has been modified by another process")
	}
	return nil
}

// UpdateSetting replaces the committee's settings when the revision is still current.
func (s *Store) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	if settings == nil {
		return errs.NewValidation("committee settings cannot be nil")
	}
	values, err := settingsValues(settings)
	if err != nil {
		return unexpected("failed to encode committee settings", err)
	}
	args := append([]any{values[0], int64(revision)}, values[1:]...)

	tag, err := s.pool.Exec(ctx, updateSettings, args...)
	if err != nil {
		return unexpected("failed to update committee settings", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, settingsExist, "committee UID", settings.UID, "committee settings not found", "committee settings have been modified by another process")
	}
	return nil
}

// The existence checks behind missingOrStale, as constants so no caller can pass request data into
// the query text.
const (
	committeeExists = "SELECT EXISTS (SELECT 1 FROM committees WHERE uid = $1)"
	settingsExist   = "SELECT EXISTS (SELECT 1 FROM committee_settings WHERE committee_uid = $1)"
)

// missingOrStale explains why a revision-guarded write matched no row: the record is gone (not
// found) or its revision moved on (conflict). existsQuery is committeeExists, settingsExist or memberExists and uidLabel names the uid in the error.
func (s *Store) missingOrStale(ctx context.Context, existsQuery, uidLabel, uid, notFound, stale string) error {
	return s.classifyRejected(ctx, existsQuery, []any{uid}, uidLabel, uid, notFound, stale)
}

// classifyRejected is missingOrStale for an existence query with more than the uid, such as one
// that also scopes the record to its committee.
func (s *Store) classifyRejected(ctx context.Context, existsQuery string, args []any, uidLabel, uid, notFound, stale string) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, existsQuery, args...).Scan(&exists); err != nil {
		return unexpected("failed to check the record after a rejected write", err)
	}
	if !exists {
		return errs.NewNotFound(notFound, fmt.Errorf("%s: %s", uidLabel, uid))
	}
	return errs.NewConflict(stale)
}

// The counter updates change one column in one statement, so concurrent updates cannot lose each
// other, and write nothing when the column already has the value. The text is built once here from
// fixed column names.
var (
	updateHasMailingList = counterUpdate("has_mailing_list")
	updateTotalMembers   = counterUpdate("total_members")
)

func counterUpdate(column string) string {
	return fmt.Sprintf("UPDATE committees SET %[1]s = $2, updated_at = $3, revision = nextval('committee_revision_seq') "+
		"WHERE uid = $1 AND %[1]s IS DISTINCT FROM $2 RETURNING %[2]s, revision", column, columnList(committeeColumns))
}

// UpdateHasMailingList sets has_mailing_list. It returns (nil, false, nil) when the flag already has
// that value and nothing is written.
func (s *Store) UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error) {
	return s.updateCommitteeField(ctx, uid, updateHasMailingList, hasMailingList, "failed to update committee has_mailing_list flag")
}

// UpdateTotalMembers sets total_members; see UpdateHasMailingList.
func (s *Store) UpdateTotalMembers(ctx context.Context, uid string, totalMembers int) (*model.CommitteeBase, bool, error) {
	return s.updateCommitteeField(ctx, uid, updateTotalMembers, totalMembers, "failed to update committee total_members counter")
}

// updateCommitteeField runs one of the counter updates above.
func (s *Store) updateCommitteeField(ctx context.Context, uid, query string, value any, failure string) (*model.CommitteeBase, bool, error) {
	base, _, err := scanCommittee(s.pool.QueryRow(ctx, query, uid, value, time.Now()))
	if err == nil {
		return base, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, unexpected(failure, err)
	}
	// No row changed: the committee is missing or already has the value.
	var exists bool
	if errExists := s.pool.QueryRow(ctx, committeeExists, uid).Scan(&exists); errExists != nil {
		return nil, false, unexpected(failure, errExists)
	}
	if !exists {
		return nil, false, errs.NewNotFound("committee not found", fmt.Errorf("committee UID: %s", uid))
	}
	return nil, false, nil
}

// Delete removes the committee and its settings in one transaction when the revision is still
// current. Its name and SSO group name are free again once it commits.
func (s *Store) Delete(ctx context.Context, uid string, revision uint64) error {
	if uid == "" {
		return errs.NewValidation("committee UID cannot be empty")
	}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM committees WHERE uid = $1 AND revision = $2", uid, int64(revision))
		if err != nil {
			return unexpected("failed to delete committee base", err)
		}
		if tag.RowsAffected() == 0 {
			return errNoMatch
		}
		if _, err := tx.Exec(ctx, "DELETE FROM committee_settings WHERE committee_uid = $1", uid); err != nil {
			return unexpected("failed to delete committee settings", err)
		}
		return nil
	})
	if errors.Is(err, errNoMatch) {
		return s.missingOrStale(ctx, committeeExists, "committee UID", uid, "committee not found", "committee has been modified by another process")
	}
	return err
}

// errNoMatch is returned inside a transaction when a revision-guarded statement matched no row.
var errNoMatch = errors.New("no row matched")
