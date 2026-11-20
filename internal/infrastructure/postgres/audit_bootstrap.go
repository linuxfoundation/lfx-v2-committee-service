// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// QueueAuditBootstrap retains projection-only baseline events for a committee
// and its current settings/members. Run with source writes paused at cutover.
// It never changes source rows, allocates a source revision, or invents history.
func (s *Store) QueueAuditBootstrap(ctx context.Context, uid string) (int, error) {
	return s.queueAuditBootstrap(ctx, uid, false)
}

// PreviewAuditBootstrap validates the full source selection without persisting events.
func (s *Store) PreviewAuditBootstrap(ctx context.Context, uid string) (int, error) {
	return s.queueAuditBootstrap(ctx, uid, true)
}

func (s *Store) queueAuditBootstrap(ctx context.Context, uid string, dryRun bool) (int, error) {
	attribution, ok := ctx.Value(constants.AuditAttributionContextID).(indexertypes.AuditAttribution)
	if !ok || uid == "" {
		return 0, errs.NewValidation("bootstrap requires a committee and trusted attribution")
	}
	var count int
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		bound := &Store{pool: s.pool, tx: tx}
		var locked string
		if err := tx.QueryRow(ctx, "SELECT uid FROM committees WHERE uid = $1 FOR UPDATE", uid).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errs.NewNotFound("bootstrap committee not found")
			}
			return unexpected("failed to lock bootstrap committee", err)
		}
		targets := []auditTarget{{kind: model.ResourceCommittee, uid: uid}, {kind: model.ResourceCommitteeSettings, uid: uid}}
		members, err := bound.ListMembersByCommittee(ctx, uid)
		if err != nil {
			return err
		}
		for _, member := range members {
			targets = append(targets, auditTarget{kind: model.ResourceCommitteeMember, uid: member.UID})
		}
		var occurredAt time.Time
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&occurredAt); err != nil {
			return unexpected("failed to capture bootstrap time", err)
		}
		occurredAt = occurredAt.UTC()
		for _, target := range targets {
			image, err := bound.readAuditImage(ctx, target)
			if err != nil {
				return err
			}
			if image == nil {
				continue
			}
			message, err := model.NewCommitteeAuditBootstrap(model.CommitteeAuditMutation{
				ObjectType: target.kind, EventID: uuid.NewString(),
				OccurredAt: occurredAt, Revision: image.revision, Action: model.ActionCreated,
				Attribution: attribution, Scope: indexertypes.AuditScope{After: image.scope},
				After: image.snapshot, Data: image.data, IndexingConfig: image.config,
			})
			if err != nil {
				return errs.NewValidation("invalid source state for audited bootstrap")
			}
			if err := model.PreflightCommitteeAuditMessage(message, target.kind); err != nil {
				return err
			}
			if !dryRun {
				if err := bound.seedAuditRevision(ctx, target, image.revision); err != nil {
					return err
				}
				if err := bound.insertAuditEvent(ctx, port.CommitteeAuditRecord{ObjectType: target.kind, Message: message}, indexertypes.AuditOriginBackfill); err != nil {
					return err
				}
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// CommitteeAuditQueueStatus is payload-free operational state, not audit history.
type CommitteeAuditQueueStatus struct {
	Retained        int64
	Pending         int64
	OldestPendingAt *time.Time
}

// AuditQueueStatus supports pending-count/age monitoring and source reconciliation.
// Broker-delivered rows remain retained and must not be assumed history-complete.
func (s *Store) AuditQueueStatus(ctx context.Context) (CommitteeAuditQueueStatus, error) {
	var status CommitteeAuditQueueStatus
	err := s.db().QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE broker_delivered_at IS NULL),
		min(captured_at) FILTER (WHERE broker_delivered_at IS NULL) FROM committee_audit_outbox`).Scan(
		&status.Retained, &status.Pending, &status.OldestPendingAt)
	if err != nil {
		return status, unexpected("failed to inspect committee audit queue", err)
	}
	return status, nil
}
