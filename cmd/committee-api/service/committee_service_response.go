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
	if p == nil {
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
