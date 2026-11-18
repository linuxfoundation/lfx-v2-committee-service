// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// AuditRetentionFromEnv requires an explicit policy limit; no retention period
// is invented for credential-bearing retired outbox records.
func AuditRetentionFromEnv(getenv func(string) string) (time.Duration, error) {
	retention, err := time.ParseDuration(getenv("COMMITTEE_AUDIT_OUTBOX_RETENTION"))
	if err != nil || retention <= 0 {
		return 0, fmt.Errorf("COMMITTEE_AUDIT_OUTBOX_RETENTION must be an explicit positive duration")
	}
	return retention, nil
}

// RetireAuditEvent is an operator confirmation of completed indexer processing,
// not an automatic consequence of PubAck. Requeue cancels retirement.
func (s *Store) RetireAuditEvent(ctx context.Context, eventID string, verifiedAttempt uint64) error {
	if eventID == "" || verifiedAttempt == 0 || verifiedAttempt > math.MaxInt64 {
		return errs.NewValidation("an audit event ID and verified delivery attempt are required")
	}
	tag, err := s.db().Exec(ctx, `UPDATE committee_audit_outbox SET retired_at = COALESCE(retired_at, clock_timestamp())
		WHERE event_id = $1 AND broker_delivered_at IS NOT NULL AND delivery_attempts = $2`, eventID, int64(verifiedAttempt))
	if err != nil {
		return unexpected("failed to retire retained audit event", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.NewConflict("audit event is missing, pending or its verified delivery changed")
	}
	return nil
}

// AuditDeliveryAttempt exposes the current broker-acknowledged generation for
// operator reconciliation. It is not proof of completed indexer processing.
func (s *Store) AuditDeliveryAttempt(ctx context.Context, eventID string) (uint64, error) {
	if eventID == "" {
		return 0, errs.NewValidation("an audit event ID is required")
	}
	var attempt uint64
	err := s.db().QueryRow(ctx, `SELECT delivery_attempts FROM committee_audit_outbox
		WHERE event_id = $1 AND broker_delivered_at IS NOT NULL`, eventID).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errs.NewConflict("audit event is missing or pending")
	}
	if err != nil {
		return 0, unexpected("failed to inspect audit delivery generation", err)
	}
	return attempt, nil
}

// PreviewAuditRetirement checks the same generation and acknowledgment predicates
// as retirement without changing state. A subsequent replay still causes conflict.
func (s *Store) PreviewAuditRetirement(ctx context.Context, eventID string, verifiedAttempt uint64) error {
	if verifiedAttempt == 0 || verifiedAttempt > math.MaxInt64 {
		return errs.NewValidation("a verified audit delivery attempt is required")
	}
	current, err := s.AuditDeliveryAttempt(ctx, eventID)
	if err != nil {
		return err
	}
	if current != verifiedAttempt {
		return errs.NewConflict("verified audit delivery changed")
	}
	return nil
}

// CommitteeAuditPruneBatchSize bounds a separately committed retention pass.
const CommitteeAuditPruneBatchSize = 1000

// PruneRetiredAuditEvents deletes only explicitly retired, broker-delivered
// events beyond the policy limit. Pending repair sources and revision lineage remain.
func (s *Store) PruneRetiredAuditEvents(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, errs.NewValidation("a positive audit retention duration is required")
	}
	if s.tx != nil {
		return 0, errs.NewValidation("audit pruning requires a separately committed batch")
	}
	tag, err := s.db().Exec(ctx, `WITH eligible AS (
		SELECT event_id FROM committee_audit_outbox
		WHERE retired_at IS NOT NULL AND broker_delivered_at IS NOT NULL
		AND retired_at < clock_timestamp() - make_interval(secs => $1)
		ORDER BY retired_at, event_id LIMIT $2 FOR UPDATE SKIP LOCKED
	) DELETE FROM committee_audit_outbox o USING eligible e WHERE o.event_id = e.event_id`, retention.Seconds(), CommitteeAuditPruneBatchSize)
	if err != nil {
		return 0, unexpected("failed to prune retired audit events", err)
	}
	return tag.RowsAffected(), nil
}
