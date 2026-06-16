// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

func organizationHasData(org model.CommitteeMemberOrganization) bool {
	return org.ID != "" || org.Name != "" || org.Website != ""
}

func organizationPtrFromFields(id, name, website *string) *model.CommitteeMemberOrganization {
	org := organizationFromOptionalFields(id, name, website)
	if !organizationHasData(org) {
		return nil
	}
	return &org
}

func inviteOrganizationValue(invite *model.CommitteeInvite) model.CommitteeMemberOrganization {
	if invite == nil || invite.Organization == nil {
		return model.CommitteeMemberOrganization{}
	}
	return *invite.Organization
}

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

// acceptInviteOrganization selects the organization for member creation on invite accept.
// When the accept payload includes an organization ID, the entire payload organization is
// used. Otherwise the stored invite organization is used as-is (no field-level merging).
func acceptInviteOrganization(invite *model.CommitteeInvite, id, name, website *string) model.CommitteeMemberOrganization {
	payloadOrg := organizationFromOptionalFields(id, name, website)
	if strings.TrimSpace(payloadOrg.ID) != "" {
		return payloadOrg
	}
	return inviteOrganizationValue(invite)
}
