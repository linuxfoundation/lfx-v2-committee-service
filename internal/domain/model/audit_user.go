// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import "strings"

// CloneCommitteeUser returns a shallow copy of u, or nil when u is nil.
func CloneCommitteeUser(u *CommitteeUser) *CommitteeUser {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

// NormalizeLegacyAuditUsers populates CreatedBy/UpdatedBy from legacy flat username
// fields when reading older KV records. Idempotent for records already migrated.
func NormalizeLegacyAuditUsers(createdBy **CommitteeUser, updatedBy **CommitteeUser, legacyCreatedByUsername, legacyUploadedByUsername string) {
	if createdBy == nil || updatedBy == nil {
		return
	}
	if *createdBy == nil {
		legacy := strings.TrimSpace(legacyCreatedByUsername)
		if legacy == "" {
			legacy = strings.TrimSpace(legacyUploadedByUsername)
		}
		if legacy != "" {
			*createdBy = &CommitteeUser{Username: legacy}
		}
	}
	if *updatedBy == nil && *createdBy != nil {
		*updatedBy = CloneCommitteeUser(*createdBy)
	}
}

// AuditCreatorUsername returns the LFID username used for indexer tags.
func AuditCreatorUsername(createdBy *CommitteeUser) string {
	if createdBy == nil {
		return ""
	}
	return strings.TrimSpace(createdBy.Username)
}

// AuditUserNeedsMigration reports whether a record still needs auth-service profile backfill.
func AuditUserNeedsMigration(u *CommitteeUser) bool {
	if u == nil {
		return false
	}
	if strings.TrimSpace(u.Username) == "" {
		return false
	}
	return strings.TrimSpace(u.Name) == ""
}
