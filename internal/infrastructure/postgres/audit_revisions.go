// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func (s *Store) auditRevision(ctx context.Context, kind model.ResourceType, uid string, rowRevision uint64) (uint64, error) {
	var revision uint64
	err := s.db().QueryRow(ctx, `SELECT resource_revision FROM committee_audit_revisions WHERE object_type = $1 AND object_id = $2`, string(kind), uid).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return rowRevision, nil
	}
	if err != nil {
		return 0, unexpected("failed to read authoritative audit revision", err)
	}
	return revision, nil
}

func (s *Store) seedAuditRevision(ctx context.Context, target auditTarget, revision uint64) error {
	_, err := s.db().Exec(ctx, `INSERT INTO committee_audit_revisions (object_type, object_id, resource_revision)
		VALUES ($1,$2,$3) ON CONFLICT (object_type, object_id) DO NOTHING`, string(target.kind), target.uid, int64(revision))
	if err != nil {
		return unexpected("failed to initialize authoritative audit revision", err)
	}
	return nil
}

func (s *Store) advanceAuditRevision(ctx context.Context, target auditTarget, revision uint64) error {
	tag, err := s.db().Exec(ctx, `INSERT INTO committee_audit_revisions (object_type, object_id, resource_revision)
		VALUES ($1,$2,$3) ON CONFLICT (object_type, object_id) DO UPDATE SET resource_revision = EXCLUDED.resource_revision
		WHERE committee_audit_revisions.resource_revision < EXCLUDED.resource_revision`, string(target.kind), target.uid, int64(revision))
	if err != nil {
		return unexpected("failed to advance authoritative audit revision", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.NewValidation("authoritative audit revision must advance")
	}
	return nil
}
