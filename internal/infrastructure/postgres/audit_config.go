// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

// AuditEnabled validates opt-in capture before constructing any storage backend.
// Auditing never claims atomicity when KV is authoritative or authentication is mocked.
func AuditEnabled(getenv func(string) string) (bool, error) {
	value := getenv("COMMITTEE_AUDIT_ENABLED")
	if value == "" || value == "false" {
		return false, nil
	}
	if value != "true" {
		return false, errs.NewValidation("COMMITTEE_AUDIT_ENABLED must be true or false")
	}
	mode := getenv("STORAGE_MODE")
	if mode != "postgres" && mode != "dual-pg" {
		return false, errs.NewValidation("committee auditing requires PostgreSQL-primary STORAGE_MODE")
	}
	if getenv("AUTH_SOURCE") == "mock" || getenv("JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL") != "" ||
		getenv("REPOSITORY_SOURCE") == "mock" || getenv("MESSAGING_SOURCE") == "mock" {
		return false, errs.NewValidation("committee auditing cannot use mocked infrastructure or authentication")
	}
	if _, err := AuditRetentionFromEnv(getenv); err != nil {
		return false, err
	}
	return true, nil
}

// WithAuditCapture selects automatic capture without changing the pool or the
// legacy store used for explicit storage copies and maintenance.
func (s *Store) WithAuditCapture() *Store {
	return &Store{pool: s.pool, auditCapture: true}
}

// CapturesCommitteeAudit reports whether this store intercepts source mutations.
func (s *Store) CapturesCommitteeAudit() bool { return s.auditCapture }
