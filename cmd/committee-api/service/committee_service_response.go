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
	base.MailingList = p.MailingList
	base.ChatChannel = p.ChatChannel

	// Handle ParentUID (already a pointer, safe to assign directly)
	base.ParentUID = p.ParentUID

	// Handle calendar if present
	if p.Calendar != nil {
		base.Calendar = model.Calendar{
			Public: p.Calendar.Public,
		}

	}

	base.JoinMode = p.JoinMode

	return base
}

// convertPayloadToSettings converts GOA payload to CommitteeSettings domain model
func (s *committeeServicesrvc) convertPayloadToSettings(p *committeeservice.CreateCommitteePayload) *model.CommitteeSettings {
	settings := &model.CommitteeSettings{
		BusinessEmailRequired: p.BusinessEmailRequired,
		LastReviewedBy:        p.LastReviewedBy,
		Writers:               convertPayloadUsersToModel(p.Writers),
		Auditors:              convertPayloadUsersToModel(p.Auditors),
		ShowMeetingAttendees:  p.ShowMeetingAttendees,
		MemberVisibility:      p.MemberVisibility,
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
	base.MailingList = p.MailingList
	base.ChatChannel = p.ChatChannel

	// Handle ParentUID (already a pointer, safe to assign directly)
	base.ParentUID = p.ParentUID

	base.JoinMode = p.JoinMode

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
		Writers:               convertPayloadUsersToModel(p.Writers),
		Auditors:              convertPayloadUsersToModel(p.Auditors),
		ShowMeetingAttendees:  p.ShowMeetingAttendees,
		MemberVisibility:      p.MemberVisibility,
	}

	return settings
}

func (s *committeeServicesrvc) convertDomainToFullResponse(response *model.Committee) *committeeservice.CommitteeFullWithReadonlyAttributes {
	if response == nil {
		return nil
	}

	result := &committeeservice.CommitteeFullWithReadonlyAttributes{
		UID:             &response.CommitteeBase.UID,
		ProjectUID:      &response.ProjectUID,
		Name:            &response.Name,
		Category:        &response.Category,
		EnableVoting:    response.EnableVoting,
		SsoGroupEnabled: response.SSOGroupEnabled,
		RequiresReview:  response.RequiresReview,
		Public:          response.Public,
		JoinMode:        response.JoinMode,
	}

	// Only set optional fields if they have values
	if response.Description != "" {
		result.Description = &response.Description
	}
	if response.Website != nil && *response.Website != "" {
		result.Website = response.Website
	}
	if response.MailingList != nil && *response.MailingList != "" {
		result.MailingList = response.MailingList
	}
	if response.ChatChannel != nil && *response.ChatChannel != "" {
		result.ChatChannel = response.ChatChannel
	}
	if response.DisplayName != "" {
		result.DisplayName = &response.DisplayName
	}
	if response.ParentUID != nil && *response.ParentUID != "" {
		result.ParentUID = response.ParentUID
	}
	if response.SSOGroupName != "" {
		result.SsoGroupName = &response.SSOGroupName
	}
	if response.TotalMembers > 0 {
		result.TotalMembers = &response.TotalMembers
	}
	if response.TotalVotingRepos > 0 {
		result.TotalVotingRepos = &response.TotalVotingRepos
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
		if response.LastReviewedAt != nil && *response.LastReviewedAt != "" {
			result.LastReviewedAt = response.LastReviewedAt
		}
		if response.LastReviewedBy != nil && *response.LastReviewedBy != "" {
			result.LastReviewedBy = response.LastReviewedBy
		}
		if response.Writers != nil {
			result.Writers = convertModelUsersToResponse(response.Writers)
		}
		if response.Auditors != nil {
			result.Auditors = convertModelUsersToResponse(response.Auditors)
		}

		result.ShowMeetingAttendees = response.ShowMeetingAttendees
		result.MemberVisibility = response.MemberVisibility
	}

	return result
}

// convertBaseToResponse converts domain CommitteeBase to GOA response type
func (s *committeeServicesrvc) convertBaseToResponse(base *model.CommitteeBase) *committeeservice.CommitteeBaseWithReadonlyAttributes {
	if base == nil {
		return nil
	}

	result := &committeeservice.CommitteeBaseWithReadonlyAttributes{
		UID:             &base.UID,
		ProjectUID:      &base.ProjectUID,
		Name:            &base.Name,
		Category:        &base.Category,
		EnableVoting:    base.EnableVoting,
		SsoGroupEnabled: base.SSOGroupEnabled,
		RequiresReview:  base.RequiresReview,
		Public:          base.Public,
		JoinMode:        base.JoinMode,
	}

	// Only set optional fields if they have values
	if base.ProjectName != "" {
		result.ProjectName = &base.ProjectName
	}
	if base.Description != "" {
		result.Description = &base.Description
	}
	if base.Website != nil && *base.Website != "" {
		result.Website = base.Website
	}
	if base.MailingList != nil && *base.MailingList != "" {
		result.MailingList = base.MailingList
	}
	if base.ChatChannel != nil && *base.ChatChannel != "" {
		result.ChatChannel = base.ChatChannel
	}
	if base.DisplayName != "" {
		result.DisplayName = &base.DisplayName
	}
	if base.ParentUID != nil && *base.ParentUID != "" {
		result.ParentUID = base.ParentUID
	}
	if base.SSOGroupName != "" {
		result.SsoGroupName = &base.SSOGroupName
	}
	if base.TotalMembers > 0 {
		result.TotalMembers = &base.TotalMembers
	}
	if base.TotalVotingRepos > 0 {
		result.TotalVotingRepos = &base.TotalVotingRepos
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
	if settings == nil {
		return nil
	}

	result := &committeeservice.CommitteeSettingsWithReadonlyAttributes{
		UID:                   &settings.UID,
		BusinessEmailRequired: settings.BusinessEmailRequired,
		ShowMeetingAttendees:  settings.ShowMeetingAttendees,
		MemberVisibility:      settings.MemberVisibility,
	}

	// Only set optional fields if they have values
	if settings.LastReviewedAt != nil && *settings.LastReviewedAt != "" {
		result.LastReviewedAt = settings.LastReviewedAt
	}
	if settings.LastReviewedBy != nil && *settings.LastReviewedBy != "" {
		result.LastReviewedBy = settings.LastReviewedBy
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

	result.Writers = convertModelUsersToResponse(settings.Writers)
	result.Auditors = convertModelUsersToResponse(settings.Auditors)

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

	// Handle LinkedinProfile with nil check
	if p.LinkedinProfile != nil {
		member.LinkedInProfile = *p.LinkedinProfile
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

	// Handle LinkedinProfile with nil check
	if p.LinkedinProfile != nil {
		member.LinkedInProfile = *p.LinkedinProfile
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
		Email:        &member.Email,
		AppointedBy:  member.AppointedBy,
		Status:       member.Status,
	}

	// Only set optional fields if they have values
	if member.Username != "" {
		result.Username = &member.Username
	}
	if member.FirstName != "" {
		result.FirstName = &member.FirstName
	}
	if member.LastName != "" {
		result.LastName = &member.LastName
	}
	if member.JobTitle != "" {
		result.JobTitle = &member.JobTitle
	}
	if member.LinkedInProfile != "" {
		result.LinkedinProfile = &member.LinkedInProfile
	}
	if member.CommitteeName != "" {
		result.CommitteeName = &member.CommitteeName
	}
	if member.CommitteeCategory != "" {
		result.CommitteeCategory = &member.CommitteeCategory
	}

	// Handle Role mapping - only include if role has meaningful data
	if member.Role.Name != "" {
		role := &struct {
			Name      string
			StartDate *string
			EndDate   *string
		}{
			Name: member.Role.Name,
		}
		if member.Role.StartDate != "" {
			role.StartDate = &member.Role.StartDate
		}
		if member.Role.EndDate != "" {
			role.EndDate = &member.Role.EndDate
		}
		result.Role = role
	}

	// Handle Voting mapping - only include if voting has meaningful data
	if member.Voting.Status != "" {
		voting := &struct {
			Status    string
			StartDate *string
			EndDate   *string
		}{
			Status: member.Voting.Status,
		}
		if member.Voting.StartDate != "" {
			voting.StartDate = &member.Voting.StartDate
		}
		if member.Voting.EndDate != "" {
			voting.EndDate = &member.Voting.EndDate
		}
		result.Voting = voting
	}

	// Handle Organization mapping - only include if organization has meaningful data
	if member.Organization.ID != "" || member.Organization.Name != "" || member.Organization.Website != "" {
		org := &struct {
			ID      *string
			Name    *string
			Website *string
		}{}
		if member.Organization.ID != "" {
			org.ID = &member.Organization.ID
		}
		if member.Organization.Name != "" {
			org.Name = &member.Organization.Name
		}
		if member.Organization.Website != "" {
			org.Website = &member.Organization.Website
		}
		result.Organization = org
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

func (s *committeeServicesrvc) convertInviteDomainToResponse(invite *model.CommitteeInvite) *committeeservice.CommitteeInviteWithReadonlyAttributes {
	if invite == nil {
		return nil
	}
	result := &committeeservice.CommitteeInviteWithReadonlyAttributes{
		UID:          &invite.UID,
		CommitteeUID: &invite.CommitteeUID,
		InviteeEmail: &invite.InviteeEmail,
		Status:       invite.Status,
	}
	if invite.Role != "" {
		result.Role = &invite.Role
	}
	if !invite.CreatedAt.IsZero() {
		createdAt := invite.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		result.CreatedAt = &createdAt
	}
	return result
}

// convertPayloadUsersToModel converts Goa payload user objects to domain model CommitteeUser slice.
// Returns nil when users is nil (field omitted by caller) and an empty non-nil slice when users
// is an explicit empty array, preserving the caller's intent to clear the list.
func convertPayloadUsersToModel(users []*committeeservice.CommitteeUser) []model.CommitteeUser {
	if users == nil {
		return nil
	}
	if len(users) == 0 {
		return []model.CommitteeUser{}
	}
	result := make([]model.CommitteeUser, 0, len(users))
	for _, u := range users {
		if u == nil || u.Username == nil || *u.Username == "" {
			continue
		}
		cu := model.CommitteeUser{}
		if u.Avatar != nil {
			cu.Avatar = *u.Avatar
		}
		if u.Email != nil {
			cu.Email = *u.Email
		}
		if u.Name != nil {
			cu.Name = *u.Name
		}
		cu.Username = *u.Username
		result = append(result, cu)
	}
	return result
}

// convertModelUsersToResponse converts domain model CommitteeUser slice to Goa response type.
// Returns nil when users is nil so that omitted fields are not serialized as empty arrays.
func convertModelUsersToResponse(users []model.CommitteeUser) []*committeeservice.CommitteeUser {
	if users == nil {
		return nil
	}
	result := make([]*committeeservice.CommitteeUser, 0, len(users))
	for _, u := range users {
		cu := &committeeservice.CommitteeUser{}
		if u.Avatar != "" {
			cu.Avatar = &u.Avatar
		}
		if u.Email != "" {
			cu.Email = &u.Email
		}
		if u.Name != "" {
			cu.Name = &u.Name
		}
		if u.Username != "" {
			cu.Username = &u.Username
		}
		result = append(result, cu)
	}
	return result
}

func (s *committeeServicesrvc) convertApplicationDomainToResponse(app *model.CommitteeApplication) *committeeservice.CommitteeApplicationWithReadonlyAttributes {
	if app == nil {
		return nil
	}
	result := &committeeservice.CommitteeApplicationWithReadonlyAttributes{
		UID:          &app.UID,
		CommitteeUID: &app.CommitteeUID,
		Status:       app.Status,
	}
	if app.ApplicantEmail != "" {
		result.ApplicantEmail = &app.ApplicantEmail
	}
	if app.Message != "" {
		result.Message = &app.Message
	}
	if app.ReviewerNotes != "" {
		result.ReviewerNotes = &app.ReviewerNotes
	}
	if !app.CreatedAt.IsZero() {
		createdAt := app.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		result.CreatedAt = &createdAt
	}
	return result
}
