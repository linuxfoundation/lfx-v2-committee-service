// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package orgid classifies committee member organization.id values.
package orgid

import (
	"strings"

	"github.com/google/uuid"
)

// IsCDPUUID reports whether id looks like a CDP organization UUID stored by
// self-serve (not a Salesforce Account SFID).
func IsCDPUUID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if looksLikeSFID(id) {
		return false
	}
	if _, err := uuid.Parse(id); err == nil {
		return true
	}
	// CDP identifiers may appear as 32 hex chars without hyphens.
	if len(id) == 32 {
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
		return true
	}
	return false
}

func looksLikeSFID(id string) bool {
	if len(id) != 15 && len(id) != 18 {
		return false
	}
	for _, c := range id {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
