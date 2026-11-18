-- Copyright The Linux Foundation and each contributor to LFX.
-- SPDX-License-Identifier: MIT

-- +goose Up
-- Audit lineage survives derived row/ETag updates, deletion and outbox pruning.
CREATE TABLE IF NOT EXISTS committee_audit_revisions (
    object_type text NOT NULL CHECK (object_type IN ('committee', 'committee_settings', 'committee_member')),
    object_id text NOT NULL,
    resource_revision bigint NOT NULL CHECK (resource_revision > 0),
    PRIMARY KEY (object_type, object_id)
);
COMMENT ON COLUMN committee_audit_revisions.object_id IS 'PII';

ALTER TABLE committee_audit_outbox DROP CONSTRAINT IF EXISTS committee_audit_outbox_origin_check;
ALTER TABLE committee_audit_outbox ADD CONSTRAINT committee_audit_outbox_origin_check
    CHECK (origin IN ('mutation', 'backfill', 'reindex'));
ALTER TABLE committee_audit_outbox ADD COLUMN IF NOT EXISTS retired_at timestamptz;
CREATE INDEX IF NOT EXISTS committee_audit_outbox_retired
    ON committee_audit_outbox (retired_at) WHERE retired_at IS NOT NULL;

-- +goose Down
-- Retained repair sources and revision lineage must survive application rollback.
SELECT 1;
