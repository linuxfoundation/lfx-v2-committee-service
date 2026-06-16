// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

func organizationFromOptionalFields(id, name, website *string) model.CommitteeMemberOrganization {
	var org model.CommitteeMemberOrganization
	if id != nil {
		org.ID = strings.TrimSpace(*id)
	}
	if name != nil {
		org.Name = strings.TrimSpace(*name)
	}
	if website != nil {
		org.Website = strings.TrimSpace(*website)
	}
	normalizeMemberOrganization(&org)
	return org
}

func normalizeMemberOrganization(org *model.CommitteeMemberOrganization) {
	if org == nil {
		return
	}
	org.ID = utils.NormalizeAccountSFID(org.ID)
	org.Name = strings.TrimSpace(org.Name)
	org.Website = strings.TrimSpace(org.Website)
}

// mergeInviteOrganization applies override fields when set; unset override fields keep base values.
func mergeInviteOrganization(base, override model.CommitteeMemberOrganization) model.CommitteeMemberOrganization {
	merged := base
	if strings.TrimSpace(override.ID) != "" {
		merged.ID = strings.TrimSpace(override.ID)
	}
	if strings.TrimSpace(override.Name) != "" {
		merged.Name = strings.TrimSpace(override.Name)
	}
	if strings.TrimSpace(override.Website) != "" {
		merged.Website = strings.TrimSpace(override.Website)
	}
	normalizeMemberOrganization(&merged)
	return merged
}
