-- Copyright The Linux Foundation and each contributor to LFX.
-- SPDX-License-Identifier: MIT

-- +goose Up
CREATE TABLE IF NOT EXISTS committee_audit_outbox (
    event_id text PRIMARY KEY,
    object_type text NOT NULL CHECK (object_type IN ('committee', 'committee_settings', 'committee_member')),
    object_id text NOT NULL,
    resource_revision bigint NOT NULL CHECK (resource_revision > 0),
    origin text NOT NULL DEFAULT 'mutation' CHECK (origin IN ('mutation', 'backfill')),
    payload bytea NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 1048576),
    captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    broker_delivered_at timestamptz,
    delivery_attempts bigint NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    last_attempt_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE UNIQUE INDEX IF NOT EXISTS committee_audit_mutation_revision
    ON committee_audit_outbox (object_type, object_id, resource_revision)
    WHERE origin = 'mutation';

CREATE INDEX IF NOT EXISTS committee_audit_outbox_pending
    ON committee_audit_outbox (next_attempt_at, captured_at, event_id)
    WHERE broker_delivered_at IS NULL;

COMMENT ON COLUMN committee_audit_outbox.payload IS 'SENSITIVE: contains PII and optional bearer JWT evidence';
COMMENT ON COLUMN committee_audit_outbox.object_id IS 'PII';

-- Deliberately no live-resource/user foreign keys or age-based cleanup. These
-- rows are the retained repair source, not a disposable queue after PubAck.

-- +goose Down
-- Retained audit data must not be destroyed by an application rollback.
SELECT 1;
