// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAuditEnabled(t *testing.T) {
	for _, tc := range []struct {
		name             string
		env              map[string]string
		enabled, invalid bool
	}{
		{"default", nil, false, false},
		{"explicitly disabled", map[string]string{"COMMITTEE_AUDIT_ENABLED": "false"}, false, false},
		{"invalid flag", map[string]string{"COMMITTEE_AUDIT_ENABLED": "yes"}, false, true},
		{"kv forbidden", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "kv"}, false, true},
		{"dual forbidden", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "dual"}, false, true},
		{"postgres", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "postgres", "COMMITTEE_AUDIT_OUTBOX_RETENTION": "24h"}, true, false},
		{"dual-pg", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "dual-pg", "COMMITTEE_AUDIT_OUTBOX_RETENTION": "24h"}, true, false},
		{"retention required", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "postgres"}, false, true},
		{"mock auth forbidden", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "postgres", "AUTH_SOURCE": "mock", "COMMITTEE_AUDIT_OUTBOX_RETENTION": "24h"}, false, true},
		{"mock principal forbidden", map[string]string{"COMMITTEE_AUDIT_ENABLED": "true", "STORAGE_MODE": "postgres", "JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL": "first-last", "COMMITTEE_AUDIT_OUTBOX_RETENTION": "24h"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AuditEnabled(func(key string) string { return tc.env[key] })
			require.Equal(t, tc.enabled, got)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
