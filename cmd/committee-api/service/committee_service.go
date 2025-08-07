// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

	"goa.design/goa/v3/security"
)

// committeeServicesrvc service implementation with clean architecture
type committeeServicesrvc struct {
	committeeWriterOrchestrator service.CommitteeWriter
	auth                        port.Authenticator
}

// JWTAuth implements the authorization logic for service "committee-service"
// for the "jwt" security scheme.
func (s *committeeServicesrvc) JWTAuth(ctx context.Context, token string, scheme *security.JWTScheme) (context.Context, error) {

	// Parse the Heimdall-authorized principal from the token
	principal, err := s.auth.ParsePrincipal(ctx, token, slog.Default())
	if err != nil {
		slog.ErrorContext(ctx, "committeeService.jwt-auth",
			"error", err,
			"token_length", len(token),
		)
		return ctx, err
	}

	// Return a new context containing the principal as a value
	return context.WithValue(ctx, constants.PrincipalContextID, principal), nil
}

// Create Committee
func (s *committeeServicesrvc) CreateCommittee(ctx context.Context, p *committeeservice.CreateCommitteePayload) (res *committeeservice.CommitteeFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeService.create-committee",
		"project_uid", p.ProjectUID,
		"name", p.Name,
	)

	// Convert payload to DTO
	request := s.convertPayloadToDomain(p)

	// Execute use case
	response, err := s.committeeWriterOrchestrator.Create(ctx, request)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertDomainToFullResponse(response)

	return result, nil
}

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
		EnableVoting:    p.EnableVoting,
		SSOGroupEnabled: p.SsoGroupEnabled,
		RequiresReview:  p.RequiresReview,
		Public:          p.Public,
	}

	// Handle ProjectUID with nil check
	if p.ProjectUID != nil {
		base.ProjectUID = *p.ProjectUID
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

// Get Committee
func (s *committeeServicesrvc) GetCommitteeBase(ctx context.Context, p *committeeservice.GetCommitteeBasePayload) (res *committeeservice.GetCommitteeBaseResult, err error) {
	res = &committeeservice.GetCommitteeBaseResult{}
	slog.DebugContext(ctx, "committeeService.get-committee-base",
		"committee_uid", p.UID,
	)
	return
}

// Update Committee
func (s *committeeServicesrvc) UpdateCommitteeBase(ctx context.Context, p *committeeservice.UpdateCommitteeBasePayload) (res *committeeservice.CommitteeBaseWithReadonlyAttributes, err error) {
	res = &committeeservice.CommitteeBaseWithReadonlyAttributes{}
	slog.DebugContext(ctx, "committeeService.update-committee-base",
		"committee_uid", p.UID,
	)
	return
}

// Delete Committee
func (s *committeeServicesrvc) DeleteCommittee(ctx context.Context, p *committeeservice.DeleteCommitteePayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee",
		"committee_uid", p.UID,
	)
	return
}

// Get Committee Settings
func (s *committeeServicesrvc) GetCommitteeSettings(ctx context.Context, p *committeeservice.GetCommitteeSettingsPayload) (res *committeeservice.GetCommitteeSettingsResult, err error) {
	res = &committeeservice.GetCommitteeSettingsResult{}
	slog.DebugContext(ctx, "committeeService.get-committee-settings",
		"committee_uid", p.UID,
	)
	return
}

// Update Committee Settings
func (s *committeeServicesrvc) UpdateCommitteeSettings(ctx context.Context, p *committeeservice.UpdateCommitteeSettingsPayload) (res *committeeservice.CommitteeSettingsWithReadonlyAttributes, err error) {
	res = &committeeservice.CommitteeSettingsWithReadonlyAttributes{}
	slog.DebugContext(ctx, "committeeService.update-committee-settings",
		"committee_uid", p.UID,
	)
	return
}

// Check if the service is able to take inbound requests.
func (s *committeeServicesrvc) Readyz(ctx context.Context) (res []byte, err error) {
	return
}

// Check if the service is alive.
func (s *committeeServicesrvc) Livez(ctx context.Context) (res []byte, err error) {
	return
}

// NewCommitteeService returns the committee-service service implementation with dependencies.
func NewCommitteeService(createCommitteeUseCase service.CommitteeWriter, authService port.Authenticator) committeeservice.Service {
	return &committeeServicesrvc{
		committeeWriterOrchestrator: createCommitteeUseCase,
		auth:                        authService,
	}
}
