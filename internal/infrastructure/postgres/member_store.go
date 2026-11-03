// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

var (
	_ port.CommitteeMemberReader = (*Store)(nil)
	_ port.CommitteeMemberWriter = (*Store)(nil)
)

// memberColumns are the committee_members columns the store writes, in the order memberValues
// returns them. uid comes first and the revision is not listed: it comes from the sequence.
var memberColumns = []string{
	"uid", "committee_uid", "username", "avatar", "email", "first_name", "last_name", "job_title",
	"linkedin_profile", "role_name", "role_start_date", "role_end_date", "appointed_by", "status",
	"voting_status", "voting_start_date", "voting_end_date", "organization_id", "organization_name",
	"organization_website", "committee_name", "committee_category", "project_uid", "project_slug",
	"created_at", "updated_at",
}

var (
	selectMember = "SELECT " + columnList(memberColumns) + ", revision FROM committee_members"
	insertMember = "INSERT INTO committee_members (" + columnList(memberColumns) + ") VALUES (" + placeholders(len(memberColumns)) + ")"
	updateMember = "UPDATE committee_members SET " + assignments(memberColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"
)

const memberExists = "SELECT EXISTS (SELECT 1 FROM committee_members WHERE uid = $1)"

func memberValues(m *model.CommitteeMember) []any {
	return []any{
		m.UID, m.CommitteeUID, m.Username, m.Avatar, m.Email, m.FirstName, m.LastName, m.JobTitle,
		m.LinkedInProfile, m.Role.Name, m.Role.StartDate, m.Role.EndDate, m.AppointedBy, m.Status,
		m.Voting.Status, m.Voting.StartDate, m.Voting.EndDate, m.Organization.ID, m.Organization.Name,
		m.Organization.Website, m.CommitteeName, m.CommitteeCategory, m.ProjectUID, m.ProjectSlug,
		m.CreatedAt, m.UpdatedAt,
	}
}

// scanMember reads a row selected with selectMember.
func scanMember(row pgx.Row) (*model.CommitteeMember, uint64, error) {
	m := &model.CommitteeMember{}
	var revision int64
	if err := row.Scan(
		&m.UID, &m.CommitteeUID, &m.Username, &m.Avatar, &m.Email, &m.FirstName, &m.LastName, &m.JobTitle,
		&m.LinkedInProfile, &m.Role.Name, &m.Role.StartDate, &m.Role.EndDate, &m.AppointedBy, &m.Status,
		&m.Voting.Status, &m.Voting.StartDate, &m.Voting.EndDate, &m.Organization.ID, &m.Organization.Name,
		&m.Organization.Website, &m.CommitteeName, &m.CommitteeCategory, &m.ProjectUID, &m.ProjectSlug,
		&m.CreatedAt, &m.UpdatedAt, &revision,
	); err != nil {
		return nil, 0, err
	}
	m.CreatedAt, m.UpdatedAt = m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	return m, uint64(revision), nil
}

func collectMember(row pgx.CollectableRow) (*model.CommitteeMember, error) {
	m, _, err := scanMember(row)
	return m, err
}

// memberConflict translates a unique violation on committee_members into the conflict the KV store
// reports for the same lookup key. It returns nil when err is not one.
func memberConflict(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok {
		return nil
	}
	if constraint == "committee_members_committee_email_key" {
		return errs.NewConflict("member with the same email already exists in the committee")
	}
	return errs.NewConflict("committee member already exists")
}

// CreateMember persists a new member. The (committee, normalized email) unique index decides a
// duplicate, so an email that differs only by case or surrounding whitespace is the same member.
func (s *Store) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	if member == nil {
		return errs.NewValidation("committee member cannot be nil")
	}
	if member.CommitteeUID == "" || member.UID == "" {
		return errs.NewValidation("committee member CommitteeUID and UID must be non-empty")
	}
	if _, err := s.pool.Exec(ctx, insertMember, memberValues(member)...); err != nil {
		if conflict := memberConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to create committee member", err)
	}
	return nil
}

// UpdateMember replaces the member when the revision is still current and returns it. A stale
// revision is a conflict and a missing member is not found.
func (s *Store) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	if member == nil {
		return nil, errs.NewValidation("committee member cannot be nil")
	}
	if member.UID == "" {
		return nil, errs.NewValidation("committee member UID cannot be empty")
	}
	values := memberValues(member)
	args := append([]any{values[0], revisionArg(revision)}, values[1:]...)

	tag, err := s.pool.Exec(ctx, updateMember, args...)
	if err != nil {
		if conflict := memberConflict(err); conflict != nil {
			return nil, conflict
		}
		return nil, unexpected("failed to update committee member", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, s.missingOrStale(ctx, memberExists, "member UID", member.UID, "committee member not found", "committee member revision conflict")
	}
	return member, nil
}

// DeleteMember removes the member when the revision is still current; its email is free again once
// it returns.
func (s *Store) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	if uid == "" {
		return errs.NewValidation("committee member UID cannot be empty")
	}
	tag, err := s.pool.Exec(ctx, "DELETE FROM committee_members WHERE uid = $1 AND revision = $2", uid, revisionArg(revision))
	if err != nil {
		return unexpected("failed to delete committee member", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, memberExists, "member UID", uid, "committee member not found", "committee member revision conflict")
	}
	return nil
}

// GetMember returns the member and its revision.
func (s *Store) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	if uid == "" {
		return nil, 0, errs.NewValidation("committee member UID cannot be empty")
	}
	member, revision, err := scanMember(s.pool.QueryRow(ctx, selectMember+" WHERE uid = $1", uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("committee member not found", fmt.Errorf("member UID: %s", uid))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get committee member", err)
	}
	return member, revision, nil
}

// GetMemberRevision returns the member's current revision.
func (s *Store) GetMemberRevision(ctx context.Context, uid string) (uint64, error) {
	if uid == "" {
		return 0, errs.NewValidation("committee member UID cannot be empty")
	}
	var revision int64
	err := s.pool.QueryRow(ctx, "SELECT revision FROM committee_members WHERE uid = $1", uid).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errs.NewNotFound("committee member not found", fmt.Errorf("member UID: %s", uid))
	}
	if err != nil {
		return 0, unexpected("failed to get committee member revision", err)
	}
	return uint64(revision), nil
}

// listMembers runs a member query and collects the rows, ordered by uid so the result is stable.
func (s *Store) listMembers(ctx context.Context, failure, where string, args ...any) ([]*model.CommitteeMember, error) {
	rows, err := s.pool.Query(ctx, selectMember+where+" ORDER BY uid", args...)
	if err != nil {
		return nil, unexpected(failure, err)
	}
	members, err := pgx.CollectRows(rows, collectMember)
	if err != nil {
		return nil, unexpected(failure, err)
	}
	return members, nil
}

// ListMembersByCommittee returns the committee's members.
func (s *Store) ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {
	if committeeUID == "" {
		return nil, errs.NewValidation("committee UID cannot be empty")
	}
	return s.listMembers(ctx, "failed to list committee members", " WHERE committee_uid = $1", committeeUID)
}

// ListMembersByOrganization returns the members held by an organization. The KV index is keyed by
// the 18-character form of the SFID, so a member whose stored ID is the 15-character form is found
// by either spelling: the query matches the 18-character form and its 15-character prefix.
func (s *Store) ListMembersByOrganization(ctx context.Context, orgSFID string) ([]*model.CommitteeMember, error) {
	orgSFID = utils.NormalizeAccountSFID(orgSFID)
	if orgSFID == "" {
		return nil, errs.NewValidation("organization SFID cannot be empty")
	}
	short := orgSFID
	if len(orgSFID) == 18 {
		short = orgSFID[:15]
	}
	// organization_id <> '' repeats the predicate of the partial index so the planner can use it.
	return s.listMembers(ctx, "failed to list committee members by organization",
		" WHERE organization_id IN ($1, $2) AND organization_id <> ''", orgSFID, short)
}

// ListMembersByEmail returns the members with the email, compared after the same trim and
// lowercase the KV index applies, across all committees.
func (s *Store) ListMembersByEmail(ctx context.Context, email string) ([]*model.CommitteeMember, error) {
	if strings.TrimSpace(strings.ToLower(email)) == "" {
		return nil, errs.NewValidation("email cannot be empty")
	}
	return s.listMembers(ctx, "failed to list committee members by email",
		" WHERE committee_normalize_key(email) = committee_normalize_key($1)", email)
}

// ListMembersByUsername returns the members with the username, compared like ListMembersByEmail.
func (s *Store) ListMembersByUsername(ctx context.Context, username string) ([]*model.CommitteeMember, error) {
	if strings.TrimSpace(strings.ToLower(username)) == "" {
		return nil, errs.NewValidation("username cannot be empty")
	}
	// username <> '' repeats the predicate of the partial index so the planner can use it.
	return s.listMembers(ctx, "failed to list committee members by username",
		" WHERE username <> '' AND committee_normalize_key(username) = committee_normalize_key($1)", username)
}

// ListAllMembers returns every member. Prefer EachMember for the whole table: it streams.
func (s *Store) ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error) {
	return s.listMembers(ctx, "failed to list all committee members", "")
}

// EachMember streams every member to fn without holding them all in memory, and stops at the first
// error fn returns. fn runs while the query is open, so a call back into the store from fn uses a
// second connection from the pool.
func (s *Store) EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	rows, err := s.pool.Query(ctx, selectMember+" ORDER BY uid")
	if err != nil {
		return unexpected("failed to stream committee members", err)
	}
	defer rows.Close()
	for rows.Next() {
		member, err := collectMember(rows)
		if err != nil {
			return unexpected("failed to read a committee member while streaming", err)
		}
		if err := fn(member); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return unexpected("failed to stream committee members", err)
	}
	return nil
}

// EachMemberStrict is EachMember. The KV store skips a record it cannot read and the strict form
// reports that afterwards; a row that cannot be read here fails the stream in both.
func (s *Store) EachMemberStrict(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	return s.EachMember(ctx, fn)
}
