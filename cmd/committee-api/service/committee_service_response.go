// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// convertPayloadToDomain converts GOA payload to domain model
func (s *committeeServicesrvc) convertPayloadToDomain(p *committeeservice.CreateCommitteePayload) *model.Committee {
	// Convert payload to domain - split into Base and Settings
	base := s.convertPayloadToBase(p)
	settings := s.convertPayloadToSettings(p)

	request := &model.Committee{
		CommitteeBase:     base,
		CommitteeSettings: settings,
	}

	return request
}

// convertPayloadToBase converts GOA payload to CommitteeBase domain model
func (s *committeeServicesrvc) convertPayloadToBase(p *committeeservice.CreateCommitteePayload) model.CommitteeBase {
	// Check for nil payload to avoid panic
	if p == nil {
		return model.CommitteeBase{}
	}

	base := model.CommitteeBase{
		Name:            p.Name,
		Category:        p.Category,
		ProjectUID:      p.ProjectUID,
		EnableVoting:    p.EnableVoting,
		SSOGroupEnabled: p.SsoGroupEnabled,
		RequiresReview:  p.RequiresReview,
		Public:          p.Public,
	}

	// Handle Description with nil check
	if p.Description != nil {
		base.Description = *p.Description
	}

	// Handle DisplayName with nil check
	if p.DisplayName != nil {
		base.DisplayName = *p.DisplayName
	}

	// Handle Website (already a pointer, safe to assign directly)
	base.Website = p.Website

	// Handle ParentUID (already a pointer, safe to assign directly)
	base.ParentUID = p.ParentUID

	// Handle calendar if present
	if p.Calendar != nil {
		base.Calendar = model.Calendar{
			Public: p.Calendar.Public,
		}

	}

	return base
}

// convertPayloadToSettings converts GOA payload to CommitteeSettings domain model
func (s *committeeServicesrvc) convertPayloadToSettings(p *committeeservice.CreateCommitteePayload) *model.CommitteeSettings {
	settings := &model.CommitteeSettings{
		BusinessEmailRequired: p.BusinessEmailRequired,
		LastReviewedBy:        p.LastReviewedBy,
		Writers:               p.Writers,
		Auditors:              p.Auditors,
	}

	// Handle LastReviewedAt - GOA validates format via Pattern constraint
	if p.LastReviewedAt != nil && *p.LastReviewedAt != "" {
		settings.LastReviewedAt = p.LastReviewedAt
	}

	return settings
}

// convertPayloadToUpdateBase converts GOA UpdateCommitteeBasePayload to CommitteeBase domain model
func (s *committeeServicesrvc) convertPayloadToUpdateBase(p *committeeservice.UpdateCommitteeBasePayload) *model.Committee {
	// Check for nil payload to avoid panic
	if p == nil || p.UID == nil {
		return &model.Committee{}
	}

	base := model.CommitteeBase{
		UID:             *p.UID, // UID is required for updates
		Name:            p.Name,
		ProjectUID:      p.ProjectUID,
		Category:        p.Category,
		EnableVoting:    p.EnableVoting,
		SSOGroupEnabled: p.SsoGroupEnabled,
		RequiresReview:  p.RequiresReview,
		Public:          p.Public,
	}

	// Handle Description with nil check
	if p.Description != nil {
		base.Description = *p.Description
	}

	// Handle DisplayName with nil check
	if p.DisplayName != nil {
		base.DisplayName = *p.DisplayName
	}

	// Handle Website (already a pointer, safe to assign directly)
	base.Website = p.Website

	// Handle ParentUID (already a pointer, safe to assign directly)
	base.ParentUID = p.ParentUID

	// Handle calendar if present
	if p.Calendar != nil {
		base.Calendar = model.Calendar{
			Public: p.Calendar.Public,
		}
	}

	// Create committee with base data only (no settings for base update)
	committee := &model.Committee{
		CommitteeBase:     base,
		CommitteeSettings: nil, // Settings are not updated in base update
	}

	return committee
}

// convertPayloadToUpdateSettings converts GOA UpdateCommitteeSettingsPayload to CommitteeSettings domain model
func (s *committeeServicesrvc) convertPayloadToUpdateSettings(p *committeeservice.UpdateCommitteeSettingsPayload) *model.CommitteeSettings {
	// Check for nil payload to avoid panic
	if p == nil {
		return &model.CommitteeSettings{}
	}

	settings := &model.CommitteeSettings{
		UID:                   *p.UID, // UID is required for updates
		BusinessEmailRequired: p.BusinessEmailRequired,
		LastReviewedAt:        p.LastReviewedAt,
		LastReviewedBy:        p.LastReviewedBy,
		Writers:               p.Writers,
		Auditors:              p.Auditors,
	}

	return settings
}

func (s *committeeServicesrvc) convertDomainToFullResponse(response *model.Committee) *committeeservice.CommitteeFullWithReadonlyAttributes {
	result := &committeeservice.CommitteeFullWithReadonlyAttributes{
		UID:              &response.CommitteeBase.UID,
		ProjectUID:       &response.ProjectUID,
		Name:             &response.Name,
		Category:         &response.Category,
		Description:      &response.Description,
		Website:          response.Website,
		EnableVoting:     response.EnableVoting,
		SsoGroupEnabled:  response.SSOGroupEnabled,
		RequiresReview:   response.RequiresReview,
		Public:           response.Public,
		DisplayName:      &response.DisplayName,
		ParentUID:        response.ParentUID,
		SsoGroupName:     &response.SSOGroupName,
		TotalMembers:     &response.TotalMembers,
		TotalVotingRepos: &response.TotalVotingRepos,
	}

	// Handle Calendar mapping
	result.Calendar = &struct {
		Public bool
	}{
		Public: response.Calendar.Public,
	}

	// Include settings data if available
	if response.CommitteeSettings != nil {
		result.BusinessEmailRequired = response.BusinessEmailRequired
		result.LastReviewedAt = response.LastReviewedAt
		result.LastReviewedBy = response.LastReviewedBy
		result.Writers = response.Writers
		result.Auditors = response.Auditors
	}

	return result
}

// convertBaseToResponse converts domain CommitteeBase to GOA response type
func (s *committeeServicesrvc) convertBaseToResponse(base *model.CommitteeBase) *committeeservice.CommitteeBaseWithReadonlyAttributes {
	result := &committeeservice.CommitteeBaseWithReadonlyAttributes{
		UID:              &base.UID,
		ProjectUID:       &base.ProjectUID,
		Name:             &base.Name,
		ProjectName:      &base.ProjectName,
		Category:         &base.Category,
		Description:      &base.Description,
		Website:          base.Website,
		EnableVoting:     base.EnableVoting,
		SsoGroupEnabled:  base.SSOGroupEnabled,
		RequiresReview:   base.RequiresReview,
		Public:           base.Public,
		DisplayName:      &base.DisplayName,
		ParentUID:        base.ParentUID,
		SsoGroupName:     &base.SSOGroupName,
		TotalMembers:     &base.TotalMembers,
		TotalVotingRepos: &base.TotalVotingRepos,
	}

	// Handle Calendar mapping
	result.Calendar = &struct {
		Public bool
	}{
		Public: base.Calendar.Public,
	}

	return result
}

// convertSettingsToResponse converts domain CommitteeSettings to GOA response type
func (s *committeeServicesrvc) convertSettingsToResponse(settings *model.CommitteeSettings) *committeeservice.CommitteeSettingsWithReadonlyAttributes {
	result := &committeeservice.CommitteeSettingsWithReadonlyAttributes{
		UID:                   &settings.UID,
		BusinessEmailRequired: settings.BusinessEmailRequired,
		LastReviewedAt:        settings.LastReviewedAt,
		LastReviewedBy:        settings.LastReviewedBy,
	}

	// Convert timestamps to strings if they exist
	if !settings.CreatedAt.IsZero() {
		createdAt := settings.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		result.CreatedAt = &createdAt
	}

	if !settings.UpdatedAt.IsZero() {
		updatedAt := settings.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
		result.UpdatedAt = &updatedAt
	}

	return result
}

// convertMemberPayloadToDomain converts GOA CreateCommitteeMemberPayload to domain model
func (s *committeeServicesrvc) convertMemberPayloadToDomain(p *committeeservice.CreateCommitteeMemberPayload) *model.CommitteeMember {
	// Check for nil payload to avoid panic
	if p == nil {
		return &model.CommitteeMember{}
	}

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: p.UID,
			Email:        p.Email,
			AppointedBy:  p.AppointedBy,
			Status:       p.Status,
		},
	}

	// Handle Username with nil check
	if p.Username != nil {
		member.Username = *p.Username
	}

	// Handle FirstName with nil check
	if p.FirstName != nil {
		member.FirstName = *p.FirstName
	}

	// Handle LastName with nil check
	if p.LastName != nil {
		member.LastName = *p.LastName
	}

	// Handle JobTitle with nil check
	if p.JobTitle != nil {
		member.JobTitle = *p.JobTitle
	}

	// Handle Role if present
	if p.Role != nil {
		member.Role = model.CommitteeMemberRole{
			Name: p.Role.Name,
		}
		if p.Role.StartDate != nil {
			member.Role.StartDate = *p.Role.StartDate
		}
		if p.Role.EndDate != nil {
			member.Role.EndDate = *p.Role.EndDate
		}
	}

	// Handle Voting if present
	if p.Voting != nil {
		member.Voting = model.CommitteeMemberVotingInfo{
			Status: p.Voting.Status,
		}
		if p.Voting.StartDate != nil {
			member.Voting.StartDate = *p.Voting.StartDate
		}
		if p.Voting.EndDate != nil {
			member.Voting.EndDate = *p.Voting.EndDate
		}
	}

	// Handle Agency with nil check (for GAC members)
	if p.Agency != nil {
		member.Agency = *p.Agency
	}

	// Handle Country with nil check (for GAC members)
	if p.Country != nil {
		member.Country = *p.Country
	}

	// Handle Organization if present
	if p.Organization != nil {
		if p.Organization.ID != nil {
			member.Organization.ID = *p.Organization.ID
		}
		if p.Organization.Name != nil {
			member.Organization.Name = *p.Organization.Name
		}
		if p.Organization.Website != nil {
			member.Organization.Website = *p.Organization.Website
		}
	}

	return member
}

// convertPayloadToUpdateMember converts GOA UpdateCommitteeMemberPayload to domain model
func (s *committeeServicesrvc) convertPayloadToUpdateMember(p *committeeservice.UpdateCommitteeMemberPayload) *model.CommitteeMember {
	// Check for nil payload to avoid panic
	if p == nil {
		return &model.CommitteeMember{}
	}

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          p.MemberUID, // Member UID is required for updates
			CommitteeUID: p.UID,       // Committee UID from path parameter
			Email:        p.Email,
			AppointedBy:  p.AppointedBy,
			Status:       p.Status,
		},
	}

	// Handle Username with nil check
	if p.Username != nil {
		member.Username = *p.Username
	}

	// Handle FirstName with nil check
	if p.FirstName != nil {
		member.FirstName = *p.FirstName
	}

	// Handle LastName with nil check
	if p.LastName != nil {
		member.LastName = *p.LastName
	}

	// Handle JobTitle with nil check
	if p.JobTitle != nil {
		member.JobTitle = *p.JobTitle
	}

	// Handle Role if present
	if p.Role != nil {
		member.Role = model.CommitteeMemberRole{
			Name: p.Role.Name,
		}
		if p.Role.StartDate != nil {
			member.Role.StartDate = *p.Role.StartDate
		}
		if p.Role.EndDate != nil {
			member.Role.EndDate = *p.Role.EndDate
		}
	}

	// Handle Voting if present
	if p.Voting != nil {
		member.Voting = model.CommitteeMemberVotingInfo{
			Status: p.Voting.Status,
		}
		if p.Voting.StartDate != nil {
			member.Voting.StartDate = *p.Voting.StartDate
		}
		if p.Voting.EndDate != nil {
			member.Voting.EndDate = *p.Voting.EndDate
		}
	}

	// Handle Agency with nil check (for GAC members)
	if p.Agency != nil {
		member.Agency = *p.Agency
	}

	// Handle Country with nil check (for GAC members)
	if p.Country != nil {
		member.Country = *p.Country
	}

	// Handle Organization if present
	if p.Organization != nil {
		if p.Organization.ID != nil {
			member.Organization.ID = *p.Organization.ID
		}
		if p.Organization.Name != nil {
			member.Organization.Name = *p.Organization.Name
		}
		if p.Organization.Website != nil {
			member.Organization.Website = *p.Organization.Website
		}
	}

	return member
}

// convertMemberDomainToFullResponse converts domain CommitteeMember to GOA response type
func (s *committeeServicesrvc) convertMemberDomainToFullResponse(member *model.CommitteeMember) *committeeservice.CommitteeMemberFullWithReadonlyAttributes {
	if member == nil {
		return nil
	}

	result := &committeeservice.CommitteeMemberFullWithReadonlyAttributes{
		CommitteeUID: &member.CommitteeUID,
		UID:          &member.UID,
		Username:     &member.Username,
		Email:        &member.Email,
		FirstName:    &member.FirstName,
		LastName:     &member.LastName,
		JobTitle:     &member.JobTitle,
		AppointedBy:  member.AppointedBy,
		Status:       member.Status,
		Agency:       &member.Agency,
		Country:      &member.Country,
	}

	// Only set CommitteeName if it's not empty
	if member.CommitteeName != "" {
		result.CommitteeName = &member.CommitteeName
	}

	// Only set CommitteeCategory if it's not empty
	if member.CommitteeCategory != "" {
		result.CommitteeCategory = &member.CommitteeCategory
	}

	// Handle Role mapping
	result.Role = &struct {
		Name      string
		StartDate *string
		EndDate   *string
	}{
		Name:      member.Role.Name,
		StartDate: &member.Role.StartDate,
		EndDate:   &member.Role.EndDate,
	}

	// Handle Voting mapping
	result.Voting = &struct {
		Status    string
		StartDate *string
		EndDate   *string
	}{
		Status:    member.Voting.Status,
		StartDate: &member.Voting.StartDate,
		EndDate:   &member.Voting.EndDate,
	}

	// Handle Organization mapping
	result.Organization = &struct {
		ID      *string
		Name    *string
		Website *string
	}{
		ID:      &member.Organization.ID,
		Name:    &member.Organization.Name,
		Website: &member.Organization.Website,
	}

	// Convert timestamps to strings if they exist
	if !member.CreatedAt.IsZero() {
		createdAt := member.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		result.CreatedAt = &createdAt
	}

	if !member.UpdatedAt.IsZero() {
		updatedAt := member.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
		result.UpdatedAt = &updatedAt
	}

	return result
}
