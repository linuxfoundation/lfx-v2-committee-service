// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

var _ port.CommitteeAuditTransactions = (*Store)(nil)

// NextAuditRevision allocates a source revision, including for a deletion whose
// tombstone has no live row. Sequence gaps on rollback are expected; reuse is not.
func (s *Store) NextAuditRevision(ctx context.Context) (uint64, error) {
	var revision uint64
	if err := s.db().QueryRow(ctx, "SELECT nextval('committee_revision_seq')").Scan(&revision); err != nil {
		return 0, unexpected("failed to allocate audit revision", err)
	}
	return revision, nil
}

// MutateWithAudit rolls back both the source mutation and its events if the
// callback, construction, or outbox insert fails. It never publishes or persists
// dispatch headers; optional actor evidence stays in the protected outbox.
// This opt-in primitive does not change legacy write paths.
func (s *Store) MutateWithAudit(ctx context.Context, mutate func(port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error)) error {
	if mutate == nil || s.tx != nil {
		return errs.NewValidation("a top-level audit mutation callback is required")
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		bound := &Store{pool: s.pool, tx: tx}
		records, err := mutate(bound)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return errs.NewValidation("an audited mutation must provide audit events")
		}
		for _, record := range records {
			if err := bound.insertAuditRecord(ctx, record); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) insertAuditRecord(ctx context.Context, record port.CommitteeAuditRecord) error {
	return s.insertAuditEvent(ctx, record, indexertypes.AuditOriginMutation)
}

func (s *Store) insertAuditEvent(ctx context.Context, record port.CommitteeAuditRecord, origin indexertypes.AuditOrigin) error {
	payload, err := json.Marshal(record.Message)
	if err != nil || record.Message == nil {
		return errs.NewValidation("a frozen committee audit event is required")
	}
	message, err := indexertypes.RestoreMessageV2(payload, string(record.ObjectType), model.ValidateCommitteeAuditContent)
	if err != nil {
		return errs.NewValidation("invalid frozen committee audit event")
	}
	if err := model.PreflightCommitteeAuditMessage(message, record.ObjectType); err != nil {
		return err
	}
	// Preflight reserved maximum authorization overhead. This header-free restore
	// still owns the exact bytes persisted below; dispatch never changes them.
	envelope, err := message.Envelope("preflight-only", model.ValidateCommitteeAuditContent)
	if err != nil {
		return errs.NewValidation("a committee mutation event is required")
	}
	if envelope.Audit.Origin != origin {
		return errs.NewValidation("a source mutation cannot use a projection-only audit origin")
	}
	if err := model.ValidateCommitteeAuditEnvelope(envelope, record.ObjectType); err != nil {
		return errs.NewValidation("a committee mutation event is required")
	}
	if origin == indexertypes.AuditOriginMutation {
		if err := s.advanceAuditRevision(ctx, auditTarget{kind: record.ObjectType, uid: envelope.IndexingConfig.ObjectID}, envelope.Audit.ResourceRevision); err != nil {
			return err
		}
	}
	query := `INSERT INTO committee_audit_outbox
		(event_id, object_type, object_id, resource_revision, payload, origin)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if envelope.Audit.Origin == indexertypes.AuditOriginBackfill {
		// Retrying a frozen bootstrap operation retains its delivery status;
		// changed immutable bytes under that identity are a conflict.
		query += ` ON CONFLICT (event_id) DO UPDATE SET event_id = EXCLUDED.event_id
			WHERE committee_audit_outbox.payload = EXCLUDED.payload`
	}
	tag, err := s.db().Exec(ctx, query, envelope.Audit.EventID, string(record.ObjectType),
		envelope.IndexingConfig.ObjectID, int64(envelope.Audit.ResourceRevision), payload, string(envelope.Audit.Origin))
	if _, conflict := uniqueViolation(err); conflict {
		return errs.NewConflict("committee audit event identity or revision already exists")
	}
	if err != nil {
		return unexpected("failed to persist committee audit event", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.NewConflict("committee audit event identity already exists with different content")
	}
	return nil
}

// DeliverNextAuditEvent locks one pending event, invokes an acknowledged-broker
// delivery callback, and records only broker delivery. Concurrent relays skip
// locked rows. Unknown publish/commit outcomes retry the identical stored event.
// A successful callback MUST mean JetStream PubAck, not core NATS Publish.
// The API wires delivery only when its separate dispatch flag and fresh service
// authentication are configured; broker delivery is not history completion.
func (s *Store) DeliverNextAuditEvent(ctx context.Context, deliver func(context.Context, model.ResourceType, *indexertypes.MessageV2) error) (bool, error) {
	if deliver == nil || s.tx != nil {
		return false, errs.NewValidation("a top-level audit delivery callback is required")
	}
	var found bool
	var deliveryErr error
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var eventID, objectType string
		var payload []byte
		err := tx.QueryRow(ctx, `SELECT event_id, object_type, payload FROM committee_audit_outbox
			WHERE broker_delivered_at IS NULL AND next_attempt_at <= clock_timestamp()
			ORDER BY next_attempt_at, captured_at, event_id
			FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&eventID, &objectType, &payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return unexpected("failed to read pending committee audit event", err)
		}
		found = true
		message, err := indexertypes.RestoreMessageV2(payload, objectType, model.ValidateCommitteeAuditContent)
		if err != nil {
			deliveryErr = errs.NewUnexpected("invalid retained committee audit event")
		} else {
			// Reserve bounded time for the retry update and transaction commit.
			budget := 10 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				if remaining < 15*time.Second {
					budget = remaining * 2 / 3
				}
			}
			deliveryCtx, cancel := context.WithTimeout(ctx, budget)
			deliveryErr = deliver(deliveryCtx, model.ResourceType(objectType), message)
			cancel()
		}
		_, err = tx.Exec(ctx, `UPDATE committee_audit_outbox SET
			delivery_attempts = delivery_attempts + 1, last_attempt_at = clock_timestamp(),
			next_attempt_at = clock_timestamp() + make_interval(secs => LEAST(300, power(2, LEAST(delivery_attempts, 8)))::double precision),
			broker_delivered_at = CASE WHEN $2 THEN clock_timestamp() ELSE NULL END
			WHERE event_id = $1`, eventID, deliveryErr == nil)
		if err != nil {
			return unexpected("failed to record committee audit broker delivery", err)
		}
		return nil
	})
	if err != nil {
		return found, err
	}
	if deliveryErr != nil {
		// External errors may contain credentials or payloads. Keep them out of
		// retained operational state and the generic error/logging path.
		return found, errs.NewServiceUnavailable("committee audit broker delivery failed")
	}
	return found, nil
}

// RequeueAuditEvent schedules the original retained bytes for authenticated
// replay. It never refreshes refs/evidence/time or bypasses indexer erasure fences.
func (s *Store) RequeueAuditEvent(ctx context.Context, eventID string) error {
	if eventID == "" {
		return errs.NewValidation("an audit event ID is required")
	}
	tag, err := s.db().Exec(ctx, `UPDATE committee_audit_outbox
		SET broker_delivered_at = NULL, retired_at = NULL, next_attempt_at = clock_timestamp()
		WHERE event_id = $1`, eventID)
	if err != nil {
		return unexpected("failed to schedule retained committee audit event", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.NewNotFound("retained committee audit event not found")
	}
	return nil
}

// ReadRetainedAuditEvent restores a repair source without changing delivery
// state. It is an internal operational primitive, not a history-query API.
func (s *Store) ReadRetainedAuditEvent(ctx context.Context, eventID string) (*indexertypes.MessageV2, model.ResourceType, error) {
	var objectType string
	var payload []byte
	err := s.db().QueryRow(ctx, "SELECT object_type, payload FROM committee_audit_outbox WHERE event_id = $1", eventID).Scan(&objectType, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", errs.NewNotFound("committee audit event not found")
	}
	if err != nil {
		return nil, "", unexpected("failed to read retained committee audit event", err)
	}
	message, err := indexertypes.RestoreMessageV2(payload, objectType, model.ValidateCommitteeAuditContent)
	if err != nil {
		return nil, "", errs.NewUnexpected("invalid retained committee audit event")
	}
	return message, model.ResourceType(objectType), nil
}
