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
	_ port.CommitteeApplicationReader = (*Store)(nil)
	_ port.CommitteeApplicationWriter = (*Store)(nil)
)

// applicationColumns are the committee_applications columns the store writes, in the order
// applicationValues returns them. uid comes first and the revision comes from the sequence.
var applicationColumns = []string{
	"uid", "committee_uid", "applicant_email", "applicant_username", "message", "status",
	"reviewer_notes", "organization", "created_at", "updated_at",
}

var (
	selectApplication = "SELECT " + columnList(applicationColumns) + ", revision FROM committee_applications"
	insertApplication = "INSERT INTO committee_applications (" + columnList(applicationColumns) + ") VALUES (" + placeholders(len(applicationColumns)) + ")"
	updateApplication = "UPDATE committee_applications SET " + assignments(applicationColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"
)

const applicationExists = "SELECT EXISTS (SELECT 1 FROM committee_applications WHERE uid = $1)"

// applicationValues returns the values for applicationColumns. The model has no update time, so the
// caller says what to store: the creation time on insert, the current time on update.
func applicationValues(a *model.CommitteeApplication, updatedAt time.Time) ([]any, error) {
	organization, err := jsonObject(a.Organization)
	if err != nil {
		return nil, err
	}
	return []any{
		a.UID, a.CommitteeUID, a.ApplicantEmail, a.ApplicantUsername, a.Message, a.Status,
		a.ReviewerNotes, organization, a.CreatedAt, updatedAt,
	}, nil
}

// scanApplication reads a row selected with selectApplication. updated_at is not read: the model has
// no field for it.
func scanApplication(row pgx.Row) (*model.CommitteeApplication, uint64, error) {
	a := &model.CommitteeApplication{}
	var organization []byte
	var updatedAt time.Time
	var revision int64
	if err := row.Scan(
		&a.UID, &a.CommitteeUID, &a.ApplicantEmail, &a.ApplicantUsername, &a.Message, &a.Status,
		&a.ReviewerNotes, &organization, &a.CreatedAt, &updatedAt, &revision,
	); err != nil {
		return nil, 0, err
	}
	a.CreatedAt = a.CreatedAt.UTC()
	var err error
	if a.Organization, err = decodeObject[model.CommitteeMemberOrganization](organization); err != nil {
		return nil, 0, fmt.Errorf("decode organization: %w", err)
	}
	return a, uint64(revision), nil
}

func collectApplication(row pgx.CollectableRow) (*model.CommitteeApplication, error) {
	a, _, err := scanApplication(row)
	return a, err
}

// applicationConflict translates a unique violation on committee_applications into the conflict the
// KV store reports for the same lookup key. It returns nil when err is not one.
func applicationConflict(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok {
		return nil
	}
	if constraint == "committee_applications_committee_email_key" {
		return errs.NewConflict("application from the same user already exists in the committee")
	}
	return errs.NewConflict("committee application already exists")
}

// CreateApplication persists a new application. The (committee, normalized applicant email) unique
// index decides a duplicate.
func (s *Store) CreateApplication(ctx context.Context, application *model.CommitteeApplication) error {
	if application == nil {
		return errs.NewValidation("committee application cannot be nil")
	}
	values, err := applicationValues(application, application.CreatedAt)
	if err != nil {
		return unexpected("failed to encode committee application", err)
	}
	if _, err := s.pool.Exec(ctx, insertApplication, values...); err != nil {
		if conflict := applicationConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to create committee application", err)
	}
	return nil
}

// UpdateApplication replaces the application when the revision is still current. A stale revision
// is a conflict and a missing application is not found.
func (s *Store) UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error {
	if application == nil {
		return errs.NewValidation("committee application cannot be nil")
	}
	values, err := applicationValues(application, time.Now())
	if err != nil {
		return unexpected("failed to encode committee application", err)
	}
	args := append([]any{values[0], int64(revision)}, values[1:]...)

	tag, err := s.pool.Exec(ctx, updateApplication, args...)
	if err != nil {
		if conflict := applicationConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to update committee application", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, applicationExists, "application UID", application.UID, "committee application not found", "committee application has been modified by another process")
	}
	return nil
}

// DeleteApplication removes the application. Like the KV store it is not an error when the
// application is already gone: the committee cascade may have removed it.
func (s *Store) DeleteApplication(ctx context.Context, application *model.CommitteeApplication) error {
	if application == nil {
		return errs.NewValidation("committee application cannot be nil")
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM committee_applications WHERE uid = $1", application.UID); err != nil {
		return unexpected("failed to delete committee application", err)
	}
	return nil
}

// GetApplication returns the application and its revision.
func (s *Store) GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error) {
	if uid == "" {
		return nil, 0, errs.NewValidation("committee application UID cannot be empty")
	}
	application, revision, err := scanApplication(s.pool.QueryRow(ctx, selectApplication+" WHERE uid = $1", uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("committee application not found", fmt.Errorf("application UID: %s", uid))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get committee application", err)
	}
	return application, revision, nil
}

func (s *Store) listApplications(ctx context.Context, where string, args ...any) ([]*model.CommitteeApplication, error) {
	rows, err := s.pool.Query(ctx, selectApplication+where+" ORDER BY uid", args...)
	if err != nil {
		return nil, unexpected("failed to list committee applications", err)
	}
	applications, err := pgx.CollectRows(rows, collectApplication)
	if err != nil {
		return nil, unexpected("failed to list committee applications", err)
	}
	return applications, nil
}

// ListApplications returns the committee's applications.
func (s *Store) ListApplications(ctx context.Context, committeeUID string) ([]*model.CommitteeApplication, error) {
	return s.listApplications(ctx, " WHERE committee_uid = $1", committeeUID)
}

// ListAllApplications returns every application.
func (s *Store) ListAllApplications(ctx context.Context) ([]*model.CommitteeApplication, error) {
	return s.listApplications(ctx, "")
}
