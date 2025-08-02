// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"

	"goa.design/goa/v3/security"
)

// committeeServicesrvc service implementation with clean architecture
type committeeServicesrvc struct {
	committeeWriterOrchestrator service.CommitteeWriter
}

// NewCommitteeService returns the committee-service service implementation with dependencies.
func NewCommitteeService(createCommitteeUseCase service.CommitteeWriter) committeeservice.Service {
	return &committeeServicesrvc{
		committeeWriterOrchestrator: createCommitteeUseCase,
	}
}

// JWTAuth implements the authorization logic for service "committee-service"
// for the "jwt" security scheme.
func (s *committeeServicesrvc) JWTAuth(ctx context.Context, token string, scheme *security.JWTScheme) (context.Context, error) {
	//
	// TBD: add authorization logic.
	//
	// In case of authorization failure this function should return
	// one of the generated error structs, e.g.:
	//
	//    return ctx, myservice.MakeUnauthorizedError("invalid token")
	//
	// Alternatively this function may return an instance of
	// goa.ServiceError with a Name field value that matches one of
	// the design error names, e.g:
	//
	//    return ctx, goa.PermanentError("unauthorized", "invalid token")
	//

	//return ctx, fmt.Errorf("not implemented")

	return ctx, nil
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
	result := s.convertDomainToReponse(response)

	return result, nil
}

// convertPayloadToDomain converts GOA payload to domain model
func (s *committeeServicesrvc) convertPayloadToDomain(p *committeeservice.CreateCommitteePayload) *model.Committee {
	// TODO
	request := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			ProjectUID:      *p.ProjectUID,
			Name:            p.Name,
			Category:        p.Category,
			Description:     *p.Description,
			Website:         p.Website,
			EnableVoting:    p.EnableVoting,
			SSOGroupEnabled: p.SsoGroupEnabled,
			RequiresReview:  p.RequiresReview,
			Public:          p.Public,
			DisplayName:     *p.DisplayName,
			ParentUID:       p.ParentUID,
			//Calendar:        p.Calendar,
		},
	}

	return request
}

func (s *committeeServicesrvc) convertDomainToReponse(response *model.Committee) *committeeservice.CommitteeFullWithReadonlyAttributes {
	result := &committeeservice.CommitteeFullWithReadonlyAttributes{
		UID:             &response.CommitteeBase.UID,
		ProjectUID:      &response.ProjectUID,
		Name:            &response.Name,
		Category:        &response.Category,
		Description:     &response.Description,
		Website:         response.Website,
		EnableVoting:    response.EnableVoting,
		SsoGroupEnabled: response.SSOGroupEnabled,
		SsoGroupName:    &response.SSOGroupName,
		Public:          response.Public,
		DisplayName:     &response.DisplayName,
		ParentUID:       response.ParentUID,
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
func (s *committeeServicesrvc) UpdateCommitteeBase(ctx context.Context, p *committeeservice.UpdateCommitteeBasePayload) (res *committeeservice.CommitteeFullWithReadonlyAttributes, err error) {
	res = &committeeservice.CommitteeFullWithReadonlyAttributes{}
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
