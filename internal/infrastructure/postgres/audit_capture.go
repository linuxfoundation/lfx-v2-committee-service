// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

type auditTarget struct {
	kind       model.ResourceType
	uid        string
	parentUID  string
	settings   bool
	cascade    bool
	projection bool
}

type auditImage struct {
	data        any
	snapshot    any
	revision    uint64
	rowRevision uint64
	scope       []string
	config      *indexertypes.IndexingConfig
}

func (s *Store) capturesAudit() bool { return s.auditCapture && s.tx == nil }

// captureAudit runs the existing SQL mutation against a transaction-bound store,
// then freezes the actual before/after state and outbox records before commit.
// The parent lock serializes adopted child mutations against committee deletion.
func (s *Store) captureAudit(ctx context.Context, target auditTarget, mutate func(*Store) error) error {
	attribution, ok := ctx.Value(constants.AuditAttributionContextID).(indexertypes.AuditAttribution)
	if !ok {
		return errs.NewForbidden("trusted audit attribution is required for this mutation")
	}
	projection, _ := ctx.Value(constants.AuditProjectionContextID).(bool)
	projection = projection || target.projection
	return s.withTx(ctx, func(tx pgx.Tx) error {
		bound := &Store{pool: s.pool, tx: tx}
		parentUID := target.parentUID
		if target.kind != model.ResourceCommitteeMember {
			parentUID = target.uid
		} else if parentUID == "" {
			member, _, err := bound.GetMember(ctx, target.uid)
			if err != nil {
				return err
			}
			parentUID = member.CommitteeUID
		}
		var lockedUID string
		err := tx.QueryRow(ctx, "SELECT uid FROM committees WHERE uid = $1 FOR UPDATE", parentUID).Scan(&lockedUID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return unexpected("failed to lock audit scope", err)
		}
		if errors.Is(err, pgx.ErrNoRows) && target.kind != model.ResourceCommittee {
			return errs.NewNotFound("committee audit scope not found")
		}
		targets := []auditTarget{target}
		var cascadeMembers map[string]*auditImage
		if target.settings {
			targets = append(targets, auditTarget{kind: model.ResourceCommitteeSettings, uid: target.uid})
		}
		if target.cascade {
			cascadeMembers, err = bound.readCascadeAuditMembers(ctx, target.uid)
			if err != nil {
				return err
			}
			for uid := range cascadeMembers {
				targets = append(targets, auditTarget{kind: model.ResourceCommitteeMember, uid: uid})
			}
			// Seed member lineage in one statement while the selected rows are locked.
			if _, err := tx.Exec(ctx, `INSERT INTO committee_audit_revisions (object_type, object_id, resource_revision)
				SELECT 'committee_member', uid, revision FROM committee_members WHERE committee_uid = $1
				ON CONFLICT (object_type, object_id) DO NOTHING`, target.uid); err != nil {
				return unexpected("failed to seed cascade audit revisions", err)
			}
		}
		before := make([]*auditImage, len(targets))
		for i, resource := range targets {
			var err error
			if target.cascade && resource.kind == model.ResourceCommitteeMember {
				before[i] = cascadeMembers[resource.uid]
			} else {
				before[i], err = bound.readAuditImage(ctx, resource)
			}
			if err != nil {
				return err
			}
			if before[i] != nil && (!target.cascade || resource.kind != model.ResourceCommitteeMember) {
				if err := bound.seedAuditRevision(ctx, resource, before[i].revision); err != nil {
					return err
				}
			}
		}
		if err := mutate(bound); err != nil {
			return err
		}
		var occurredAt time.Time
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&occurredAt); err != nil {
			return unexpected("failed to capture audit occurrence time", err)
		}
		occurredAt = occurredAt.UTC()
		for i, resource := range targets {
			var after *auditImage
			var err error
			if !target.cascade {
				after, err = bound.readAuditImage(ctx, resource)
				if err != nil {
					return err
				}
			}
			old := before[i]
			if old == nil && after == nil {
				continue
			}
			if old != nil && after != nil {
				if after.rowRevision < old.rowRevision || after.rowRevision == old.rowRevision && !reflect.DeepEqual(old.data, after.data) {
					return errs.NewValidation("audit revision sequence must be advanced before updating")
				}
				if old.rowRevision == after.rowRevision {
					continue
				}
			}
			current := after
			if projection && (old == nil || after == nil || !validDerivedAuditChange(resource.kind, old.data, after.data)) {
				return errs.NewValidation("a projection cannot create, delete or change authoritative committee fields")
			}
			m := model.CommitteeAuditMutation{ObjectType: resource.kind, EventID: uuid.NewString(), OccurredAt: occurredAt,
				Action: model.ActionUpdated, Attribution: attribution}
			if old != nil {
				m.Before, m.Scope.Before = old.snapshot, old.scope
			}
			if after != nil {
				m.After, m.Scope.After, m.Revision = after.snapshot, after.scope, after.revision
			}
			if old == nil {
				m.Action = model.ActionCreated
			}
			if after == nil {
				m.Action, current, m.Data = model.ActionDeleted, old, resource.uid
				m.Revision, err = bound.NextAuditRevision(ctx)
				if err != nil {
					return err
				}
				if m.Revision <= old.revision {
					return errs.NewValidation("audit revision sequence must be advanced before deletion")
				}
			} else {
				m.Data = after.data
			}
			m.IndexingConfig = current.config
			origin := indexertypes.AuditOriginMutation
			var message *indexertypes.MessageV2
			if projection {
				origin = indexertypes.AuditOriginReindex
				m.Revision = old.revision
				message, err = model.NewCommitteeAuditProjection(m, origin)
			} else {
				if after != nil {
					m.Revision = after.rowRevision
				}
				message, err = model.NewCommitteeAuditMutation(m)
			}
			if err != nil {
				return errs.NewValidation("failed to construct committee audit mutation")
			}
			if err := bound.insertAuditEvent(ctx, port.CommitteeAuditRecord{ObjectType: resource.kind, Message: message}, origin); err != nil {
				return err
			}
		}
		return nil
	})
}

func validDerivedAuditChange(kind model.ResourceType, before, after any) bool {
	switch kind {
	case model.ResourceCommittee:
		old, ok := before.(model.CommitteeBase)
		current, valid := after.(model.CommitteeBase)
		if !ok || !valid {
			return false
		}
		current.TotalMembers, current.TotalVotingRepos, current.HasMailingList = old.TotalMembers, old.TotalVotingRepos, old.HasMailingList
		current.UpdatedAt = old.UpdatedAt
		return reflect.DeepEqual(old, current)
	case model.ResourceCommitteeMember:
		old, ok := before.(model.CommitteeMember)
		current, valid := after.(model.CommitteeMember)
		if !ok || !valid {
			return false
		}
		current.Username, current.Avatar, current.Email = old.Username, old.Avatar, old.Email
		current.FirstName, current.LastName, current.JobTitle, current.LinkedInProfile = old.FirstName, old.LastName, old.JobTitle, old.LinkedInProfile
		current.CommitteeName, current.CommitteeCategory = old.CommitteeName, old.CommitteeCategory
		current.ProjectUID, current.ProjectSlug = old.ProjectUID, old.ProjectSlug
		current.Organization.Name, current.Organization.Website = old.Organization.Name, old.Organization.Website
		current.UpdatedAt = old.UpdatedAt
		return reflect.DeepEqual(old, current)
	default:
		return false
	}
}

func (s *Store) readAuditImage(ctx context.Context, target auditTarget) (*auditImage, error) {
	image := &auditImage{}
	var committeeUID, projectUID string
	var err error
	switch target.kind {
	case model.ResourceCommittee:
		var base *model.CommitteeBase
		base, image.revision, err = scanCommittee(s.db().QueryRow(ctx, selectCommittee+" WHERE uid = $1 FOR UPDATE", target.uid))
		if err == nil {
			image.data = *base
			image.snapshot, err = model.SnapshotCommittee(*base)
			committeeUID, projectUID = base.UID, base.ProjectUID
			image.config = base.IndexingConfig()
		}
	case model.ResourceCommitteeSettings:
		var settings *model.CommitteeSettings
		settings, image.revision, err = scanSettings(s.db().QueryRow(ctx, selectSettings+" WHERE committee_uid = $1 FOR UPDATE", target.uid))
		if err == nil {
			base, _, readErr := s.GetBase(ctx, target.uid)
			if readErr != nil {
				return nil, readErr
			}
			committeeUID, projectUID = target.uid, base.ProjectUID
			image.data = *settings
			image.snapshot, err = model.SnapshotCommitteeSettings(*settings, projectUID)
			image.config = settings.IndexingConfig((&model.Committee{CommitteeBase: *base}).Tags())
			image.config.HistoryCheckObject = "committee:" + committeeUID
		}
	case model.ResourceCommitteeMember:
		var member *model.CommitteeMember
		member, image.revision, err = scanMember(s.db().QueryRow(ctx, selectMember+" WHERE uid = $1 FOR UPDATE", target.uid))
		if err == nil {
			base, _, readErr := s.GetBase(ctx, member.CommitteeUID)
			if readErr != nil {
				return nil, readErr
			}
			// Preserve an existing source project so a denormalization update
			// after a committee move records the actual before/after scope.
			// Legacy rows without it use their authoritative parent's scope.
			if member.ProjectUID == "" {
				member.ProjectUID = base.ProjectUID
			}
			committeeUID, projectUID = member.CommitteeUID, member.ProjectUID
			image.data = *member
			image.snapshot, err = model.SnapshotCommitteeMember(*member)
			image.config = member.IndexingConfig()
		}
	default:
		return nil, errs.NewValidation("unsupported committee audit target")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unexpected("failed to capture committee audit state", err)
	}
	image.rowRevision = image.revision
	image.revision, err = s.auditRevision(ctx, target.kind, target.uid, image.rowRevision)
	if err != nil {
		return nil, err
	}
	image.scope = []string{"project:" + projectUID, "committee:" + committeeUID}
	return image, nil
}
