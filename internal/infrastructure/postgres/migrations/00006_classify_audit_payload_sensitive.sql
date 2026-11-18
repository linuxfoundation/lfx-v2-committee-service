-- Copyright The Linux Foundation and each contributor to LFX.
-- SPDX-License-Identifier: MIT

-- +goose Up
-- Apply the credential classification to databases that already ran migration 4.
COMMENT ON COLUMN committee_audit_outbox.payload IS 'SENSITIVE: contains PII and optional bearer JWT evidence';

-- +goose Down
-- Rollback must not downgrade classification of retained credential-bearing data.
SELECT 1;
