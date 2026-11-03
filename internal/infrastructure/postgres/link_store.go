// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

var (
	_ port.CommitteeLinkReader = (*Store)(nil)
	_ port.CommitteeLinkWriter = (*Store)(nil)
)

// linkColumns and folderColumns are the columns the store writes, in the order linkValues and
// folderValues return them. uid comes first and the revision is not listed: it comes from the sequence.
var (
	linkColumns = []string{
		"uid", "committee_uid", "folder_uid", "name", "url", "description", "created_by", "updated_by", "created_at", "updated_at",
	}
	folderColumns = []string{"uid", "committee_uid", "name", "created_by", "updated_by", "created_at", "updated_at"}
)

var (
	selectLink = "SELECT " + columnList(linkColumns) + ", revision FROM committee_links"
	insertLink = "INSERT INTO committee_links (" + columnList(linkColumns) + ") VALUES (" + placeholders(len(linkColumns)) + ")"
	updateLink = "UPDATE committee_links SET " + assignments(linkColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"

	selectFolder = "SELECT " + columnList(folderColumns) + ", revision FROM committee_link_folders"
	insertFolder = "INSERT INTO committee_link_folders (" + columnList(folderColumns) + ") VALUES (" + placeholders(len(folderColumns)) + ")"
	updateFolder = "UPDATE committee_link_folders SET " + assignments(folderColumns[1:], 3) +
		", revision = nextval('committee_revision_seq') WHERE uid = $1 AND revision = $2"
)

// The existence checks and deletes are scoped to the committee, as the KV store does by comparing
// the committee of the record it read: a record of another committee is not found.
const (
	linkExists   = "SELECT EXISTS (SELECT 1 FROM committee_links WHERE uid = $1)"
	folderExists = "SELECT EXISTS (SELECT 1 FROM committee_link_folders WHERE uid = $1)"

	linkExistsIn   = "SELECT EXISTS (SELECT 1 FROM committee_links WHERE uid = $1 AND committee_uid = $2)"
	folderExistsIn = "SELECT EXISTS (SELECT 1 FROM committee_link_folders WHERE uid = $1 AND committee_uid = $2)"

	deleteLink   = "DELETE FROM committee_links WHERE uid = $1 AND committee_uid = $2 AND revision = $3"
	deleteFolder = "DELETE FROM committee_link_folders WHERE uid = $1 AND committee_uid = $2 AND revision = $3"
)

func linkValues(l *model.CommitteeLink) ([]any, error) {
	createdBy, err := jsonObject(l.CreatedBy)
	if err != nil {
		return nil, err
	}
	updatedBy, err := jsonObject(l.UpdatedBy)
	if err != nil {
		return nil, err
	}
	return []any{l.UID, l.CommitteeUID, l.FolderUID, l.Name, l.URL, l.Description, createdBy, updatedBy, l.CreatedAt, l.UpdatedAt}, nil
}

func scanLink(row pgx.Row) (*model.CommitteeLink, uint64, error) {
	l := &model.CommitteeLink{}
	var createdBy, updatedBy []byte
	var revision int64
	if err := row.Scan(
		&l.UID, &l.CommitteeUID, &l.FolderUID, &l.Name, &l.URL, &l.Description, &createdBy, &updatedBy, &l.CreatedAt, &l.UpdatedAt, &revision,
	); err != nil {
		return nil, 0, err
	}
	l.CreatedAt, l.UpdatedAt = l.CreatedAt.UTC(), l.UpdatedAt.UTC()
	var err error
	if l.CreatedBy, err = decodeObject[model.CommitteeUser](createdBy); err != nil {
		return nil, 0, fmt.Errorf("decode created_by: %w", err)
	}
	if l.UpdatedBy, err = decodeObject[model.CommitteeUser](updatedBy); err != nil {
		return nil, 0, fmt.Errorf("decode updated_by: %w", err)
	}
	return l, uint64(revision), nil
}

func collectLink(row pgx.CollectableRow) (*model.CommitteeLink, error) {
	l, _, err := scanLink(row)
	return l, err
}

func folderValues(f *model.CommitteeLinkFolder) ([]any, error) {
	createdBy, err := jsonObject(f.CreatedBy)
	if err != nil {
		return nil, err
	}
	updatedBy, err := jsonObject(f.UpdatedBy)
	if err != nil {
		return nil, err
	}
	return []any{f.UID, f.CommitteeUID, f.Name, createdBy, updatedBy, f.CreatedAt, f.UpdatedAt}, nil
}

func scanFolder(row pgx.Row) (*model.CommitteeLinkFolder, uint64, error) {
	f := &model.CommitteeLinkFolder{}
	var createdBy, updatedBy []byte
	var revision int64
	if err := row.Scan(&f.UID, &f.CommitteeUID, &f.Name, &createdBy, &updatedBy, &f.CreatedAt, &f.UpdatedAt, &revision); err != nil {
		return nil, 0, err
	}
	f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
	var err error
	if f.CreatedBy, err = decodeObject[model.CommitteeUser](createdBy); err != nil {
		return nil, 0, fmt.Errorf("decode created_by: %w", err)
	}
	if f.UpdatedBy, err = decodeObject[model.CommitteeUser](updatedBy); err != nil {
		return nil, 0, fmt.Errorf("decode updated_by: %w", err)
	}
	return f, uint64(revision), nil
}

func collectFolder(row pgx.CollectableRow) (*model.CommitteeLinkFolder, error) {
	f, _, err := scanFolder(row)
	return f, err
}

// CreateLink persists a new link. Links have no unique name: only the uid can collide.
func (s *Store) CreateLink(ctx context.Context, link *model.CommitteeLink) error {
	if link == nil {
		return errs.NewValidation("link cannot be nil")
	}
	values, err := linkValues(link)
	if err != nil {
		return unexpected("failed to encode link", err)
	}
	if _, err := s.pool.Exec(ctx, insertLink, values...); err != nil {
		if _, ok := uniqueViolation(err); ok {
			return errs.NewConflict("link already exists")
		}
		return unexpected("failed to create link", err)
	}
	return nil
}

// UpdateLink replaces the link when the revision is still current. The record is found by uid alone,
// as in KV; a stale revision is a conflict and a missing link is not found.
func (s *Store) UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error {
	if link == nil {
		return errs.NewValidation("link cannot be nil")
	}
	values, err := linkValues(link)
	if err != nil {
		return unexpected("failed to encode link", err)
	}
	args := append([]any{values[0], int64(revision)}, values[1:]...)
	tag, err := s.pool.Exec(ctx, updateLink, args...)
	if err != nil {
		return unexpected("failed to update link", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, linkExists, "link UID", link.UID, "link not found", "link has been modified since it was last read")
	}
	return nil
}

// GetLink returns the link and its revision. A link of another committee is not found.
func (s *Store) GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error) {
	if linkUID == "" {
		return nil, 0, errs.NewValidation("link UID cannot be empty")
	}
	link, revision, err := scanLink(s.pool.QueryRow(ctx, selectLink+" WHERE uid = $1 AND committee_uid = $2", linkUID, committeeUID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("link not found", fmt.Errorf("link UID: %s", linkUID))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get link", err)
	}
	return link, revision, nil
}

// ListLinks returns the committee's links.
func (s *Store) ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	return s.listLinks(ctx, " WHERE committee_uid = $1", committeeUID)
}

// ListAllLinks returns the committee's links, or every link when committeeUID is empty.
func (s *Store) ListAllLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	if committeeUID != "" {
		return s.ListLinks(ctx, committeeUID)
	}
	return s.listLinks(ctx, "")
}

func (s *Store) listLinks(ctx context.Context, where string, args ...any) ([]*model.CommitteeLink, error) {
	rows, err := s.pool.Query(ctx, selectLink+where+" ORDER BY uid", args...)
	if err != nil {
		return nil, unexpected("failed to list links", err)
	}
	links, err := pgx.CollectRows(rows, collectLink)
	if err != nil {
		return nil, unexpected("failed to list links", err)
	}
	return links, nil
}

// DeleteLink removes the link when the revision is still current.
func (s *Store) DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64) error {
	tag, err := s.pool.Exec(ctx, deleteLink, linkUID, committeeUID, int64(revision))
	if err != nil {
		return unexpected("failed to delete link", err)
	}
	if tag.RowsAffected() == 0 {
		return s.classifyRejected(ctx, linkExistsIn, []any{linkUID, committeeUID}, "link UID", linkUID, "link not found", "link has been modified since it was last read")
	}
	return nil
}

// folderConflict translates a unique violation on committee_link_folders into the conflict the KV
// store reports for its name lookup key. It returns nil when err is not one.
func folderConflict(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok {
		return nil
	}
	if constraint == "committee_link_folders_committee_name_key" {
		return errs.NewConflict("folder with the same name already exists for this committee")
	}
	return errs.NewConflict("folder already exists")
}

// CreateLinkFolder persists a new folder. The (committee, name) unique index decides a duplicate;
// the name is case-sensitive.
func (s *Store) CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error {
	if folder == nil {
		return errs.NewValidation("folder cannot be nil")
	}
	values, err := folderValues(folder)
	if err != nil {
		return unexpected("failed to encode folder", err)
	}
	if _, err := s.pool.Exec(ctx, insertFolder, values...); err != nil {
		if conflict := folderConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to create folder", err)
	}
	return nil
}

// UpdateLinkFolder replaces the folder when the revision is still current. Unlike KV, which keeps
// the lookup key of the name the folder was created with, a changed name is checked against the unique index.
func (s *Store) UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error {
	if folder == nil {
		return errs.NewValidation("folder cannot be nil")
	}
	values, err := folderValues(folder)
	if err != nil {
		return unexpected("failed to encode folder", err)
	}
	args := append([]any{values[0], int64(revision)}, values[1:]...)
	tag, err := s.pool.Exec(ctx, updateFolder, args...)
	if err != nil {
		if conflict := folderConflict(err); conflict != nil {
			return conflict
		}
		return unexpected("failed to update folder", err)
	}
	if tag.RowsAffected() == 0 {
		return s.missingOrStale(ctx, folderExists, "folder UID", folder.UID, "folder not found", "folder has been modified since it was last read")
	}
	return nil
}

// GetLinkFolder returns the folder and its revision. A folder of another committee is not found.
func (s *Store) GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error) {
	if folderUID == "" {
		return nil, 0, errs.NewValidation("folder UID cannot be empty")
	}
	folder, revision, err := scanFolder(s.pool.QueryRow(ctx, selectFolder+" WHERE uid = $1 AND committee_uid = $2", folderUID, committeeUID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errs.NewNotFound("folder not found", fmt.Errorf("folder UID: %s", folderUID))
	}
	if err != nil {
		return nil, 0, unexpected("failed to get folder", err)
	}
	return folder, revision, nil
}

// ListLinkFolders returns the committee's folders.
func (s *Store) ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	return s.listFolders(ctx, " WHERE committee_uid = $1", committeeUID)
}

// ListAllLinkFolders returns the committee's folders, or every folder when committeeUID is empty.
func (s *Store) ListAllLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	if committeeUID != "" {
		return s.ListLinkFolders(ctx, committeeUID)
	}
	return s.listFolders(ctx, "")
}

func (s *Store) listFolders(ctx context.Context, where string, args ...any) ([]*model.CommitteeLinkFolder, error) {
	rows, err := s.pool.Query(ctx, selectFolder+where+" ORDER BY uid", args...)
	if err != nil {
		return nil, unexpected("failed to list folders", err)
	}
	folders, err := pgx.CollectRows(rows, collectFolder)
	if err != nil {
		return nil, unexpected("failed to list folders", err)
	}
	return folders, nil
}

// DeleteLinkFolder removes the folder when the revision is still current. Links in the folder are
// left alone, as in KV; the schema has no foreign key.
func (s *Store) DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error {
	tag, err := s.pool.Exec(ctx, deleteFolder, folderUID, committeeUID, int64(revision))
	if err != nil {
		return unexpected("failed to delete folder", err)
	}
	if tag.RowsAffected() == 0 {
		return s.classifyRejected(ctx, folderExistsIn, []any{folderUID, committeeUID}, "folder UID", folderUID, "folder not found", "folder has been modified since it was last read")
	}
	return nil
}
