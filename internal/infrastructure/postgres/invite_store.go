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
	_ port.CommitteeInviteReader = (*Store)(nil)
	_ port.CommitteeInviteWriter = (*Store)(nil)
)

// inviteColumns are the committee_invites columns the store writes, in the order inviteValues
// returns them. uid comes first and the revision is not listed: it comes from the sequence.
var inviteColumns = []string{
	"uid", "committee_uid", "committee_name", "organization_required", "invitee_email", "role",
	"organization", "status", "inviter", "expires_at", "created_at", "updated_at",
}

var (
	selectInvite = "SELECT " + columnList(inviteColumns) + ", revision FROM committee_invites"
	insertInvite = "INSERT INTO committee_invites (" + columnList(inviteColumns) + ") VALUES (" + placeholders(len(inviteColumns)) + ")"
	updateInvite = "UPDATE committee_invites SET " + assignments(inviteColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"
)

const inviteExists = "SELECT EXISTS (SELECT 1 FROM committee_invites WHERE uid = $1)"

// inviteValues returns the values for inviteColumns. The model has no update time, so the caller
// says what to store: the creation time on insert, the current time on update.
func inviteValues(i *model.CommitteeInvite, updatedAt time.Time) ([]any, error) {
	organization, err := jsonObject(i.Organization)
	if err != nil {
		return nil, err
	}
	inviter, err := jsonObject(i.Inviter)
	if err != nil {
		return nil, err
	}
	var expiresAt any // NULL when the invite has no expiry
	if !i.ExpiresAt.IsZero() {
		expiresAt = i.ExpiresAt
	}
	return []any{
		i.UID, i.CommitteeUID, i.CommitteeName, i.OrganizationRequired, i.InviteeEmail, i.Role,
		organization, i.Status, inviter, expiresAt, i.CreatedAt, updatedAt,
	}, nil
}

// scanInvite reads a row selected with selectInvite. updated_at is not read: the model has no field for it.
func scanInvite(row pgx.Row) (*model.CommitteeInvite, uint64, error) {
	i := &model.CommitteeInvite{}
	var organization, inviter []byte
	var expiresAt *time.Time
	var updatedAt time.Time
	var revision int64
	if err := row.Scan(
		&i.UID, &i.CommitteeUID, &i.CommitteeName, &i.OrganizationRequired, &i.InviteeEmail, &i.Role,
		&organization, &i.Status, &inviter, &expiresAt, &i.CreatedAt, &updatedAt, &revision,
	); err != nil {
		return nil, 0, err
	}
	i.CreatedAt = i.CreatedAt.UTC()
	if expiresAt != nil {
		i.ExpiresAt = expiresAt.UTC()
	}
	var err error
	if i.Organization, err = decodeObject[model.CommitteeMemberOrganization](organization); err != nil {
		return nil, 0, fmt.Errorf("decode organization: %w", err)
	}
	if i.Inviter, err = decodeObject[model.CommitteeUser](inviter); err != nil {
		return nil, 0, fmt.Errorf("decode inviter: %w", err)
	}
	return i, uint64(revision), nil
}

func collectInvite(row pgx.CollectableRow) (*model.CommitteeInvite, error) {
	i, _, err := scanInvite(row)
	return i, err
}

// inviteConflict translates a unique violation on committee_invites into the conflict the KV store
// reports for the same lookup key. It returns nil when err is not one.
func inviteConflict(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok {
		return nil
	}
	if constraint == "committee_invites_committee_email_key" {
		return errs.NewConflict("invite for the same email already exists in the committee")
	}
	return errs.NewConflict("committee invite already exists")
}

// CreateInvite persists a new invite. The (committee, normalized email) unique index decides a
// duplicate, whatever the invite's status.
func (s *Store) CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error {
	if invite == nil {
		return errs.NewValidation("committee invite cannot be nil")
	}
	if invite.UID == "" || invite.CommitteeUID == "" {
		return errs.NewValidation("committee invite UID and CommitteeUID must be non-empty")
	}
	values, err := inviteValues(invite, invite.CreatedAt)
	if err != nil {
		return unexpected("failed to encode committee invite", err)
	}
	if _, err := s.pool.Exec(ctx, insertInvite, values...); err != nil {
		if conflict := inviteConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to create committee invite", err)
	}
	return nil
}

// UpdateInvite replaces the invite when the revision is still current. A stale revision is a
// conflict and a missing invite is not found. Unlike KV, which keeps the lookup key of the email
// the invite was created with, a changed email is checked against the unique index.
func (s *Store) UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error {
	if invite == nil {
		return errs.NewValidation("committee invite cannot be nil")
	}
	values, err := inviteValues(invite, time.Now())
	if err != nil {
		return unexpected("failed to encode committee invite", err)
	}
	args := append([]any{values[0], int64(revision)}, values[1:]...)

	tag, err := s.pool.Exec(ctx, updateInvite, args...)
	if err != nil {
		if conflict := inviteConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to update committee invite", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, inviteExists, "invite UID", invite.UID, "committee invite not found", "committee invite has been modified by another process")
	}
	return nil
}

// GetInvite returns the invite and its revision.
func (s *Store) GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error) {
	if uid == "" {
		return nil, 0, errs.NewValidation("committee invite UID cannot be empty")
	}
	invite, revision, err := scanInvite(s.pool.QueryRow(ctx, selectInvite+" WHERE uid = $1", uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("committee invite not found", fmt.Errorf("invite UID: %s", uid))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get committee invite", err)
	}
	return invite, revision, nil
}

func (s *Store) listInvites(ctx context.Context, where string, args ...any) ([]*model.CommitteeInvite, error) {
	rows, err := s.pool.Query(ctx, selectInvite+where+" ORDER BY uid", args...)
	if err != nil {
		return nil, unexpected("failed to list committee invites", err)
	}
	invites, err := pgx.CollectRows(rows, collectInvite)
	if err != nil {
		return nil, unexpected("failed to list committee invites", err)
	}
	return invites, nil
}

// ListInvites returns the committee's invites.
func (s *Store) ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	return s.listInvites(ctx, " WHERE committee_uid = $1", committeeUID)
}

// ListAllInvites returns every invite.
func (s *Store) ListAllInvites(ctx context.Context) ([]*model.CommitteeInvite, error) {
	return s.listInvites(ctx, "")
}
