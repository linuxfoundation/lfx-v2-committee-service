// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// readCascadeAuditMembers captures all locked member rows and their ETags in a
// single query, reusing one parent read. Completed cascades need no after-image
// lookups: successful DeleteCommitteeCascade removes this entire locked selection.
func (s *Store) readCascadeAuditMembers(ctx context.Context, committeeUID string) (map[string]*auditImage, error) {
	base, _, err := s.GetBase(ctx, committeeUID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db().Query(ctx, selectMember+" WHERE committee_uid = $1 ORDER BY uid FOR UPDATE", committeeUID)
	if err != nil {
		return nil, unexpected("failed to capture cascade audit members", err)
	}
	images := make(map[string]*auditImage)
	var ids []string
	for rows.Next() {
		member, revision, err := scanMember(rows)
		if err != nil {
			rows.Close()
			return nil, unexpected("failed to capture cascade audit member", err)
		}
		if member.ProjectUID == "" {
			member.ProjectUID = base.ProjectUID
		}
		snapshot, err := model.SnapshotCommitteeMember(*member)
		if err != nil {
			rows.Close()
			return nil, err
		}
		images[member.UID] = &auditImage{data: *member, snapshot: snapshot, revision: revision, rowRevision: revision,
			scope: []string{"project:" + member.ProjectUID, "committee:" + committeeUID}, config: member.IndexingConfig()}
		ids = append(ids, member.UID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, unexpected("failed to capture cascade audit members", err)
	}
	if len(ids) == 0 {
		return images, nil
	}
	revisions, err := s.db().Query(ctx, `SELECT object_id, resource_revision FROM committee_audit_revisions
		WHERE object_type = 'committee_member' AND object_id = ANY($1)`, ids)
	if err != nil {
		return nil, unexpected("failed to capture cascade audit lineage", err)
	}
	defer revisions.Close()
	for revisions.Next() {
		var uid string
		var revision uint64
		if err := revisions.Scan(&uid, &revision); err != nil {
			return nil, unexpected("failed to capture cascade audit lineage", err)
		}
		images[uid].revision = revision
	}
	if err := revisions.Err(); err != nil {
		return nil, unexpected("failed to capture cascade audit lineage", err)
	}
	return images, nil
}
