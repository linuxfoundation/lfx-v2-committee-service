// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

	"goa.design/goa/v3/security"
)

// committeeServicesrvc service implementation with clean architecture
type committeeServicesrvc struct {
	committeeWriterOrchestrator service.CommitteeWriter
	committeeReaderOrchestrator service.CommitteeReader
	auth                        port.Authenticator
	storage                     port.CommitteeReaderWriter
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

// GetCommitteeBase retrieves the committee base information by UID.
func (s *committeeServicesrvc) GetCommitteeBase(ctx context.Context, p *committeeservice.GetCommitteeBasePayload) (res *committeeservice.GetCommitteeBaseResult, err error) {

	slog.DebugContext(ctx, "committeeService.get-committee-base",
		"committee_uid", p.UID,
	)

	// Execute use case
	committeeBase, revision, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert domain model to GOA response
	result := s.convertBaseToResponse(committeeBase)

	// Create result with ETag (using revision from NATS)
	revisionStr := fmt.Sprintf("%d", revision)
	res = &committeeservice.GetCommitteeBaseResult{
		CommitteeBase: result,
		Etag:          &revisionStr,
	}

	return res, nil
}

// Update Committee
func (s *committeeServicesrvc) UpdateCommitteeBase(ctx context.Context, p *committeeservice.UpdateCommitteeBasePayload) (res *committeeservice.CommitteeBaseWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.update-committee-base",
		"committee_uid", p.UID,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
		)
		return nil, wrapError(ctx, err)
	}

	// Convert payload to domain model
	committee := s.convertPayloadToUpdateBase(p)

	// Execute use case
	updatedCommittee, err := s.committeeWriterOrchestrator.Update(ctx, committee, parsedRevision)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertBaseToResponse(&updatedCommittee.CommitteeBase)

	return result, nil
}

// Delete Committee
func (s *committeeServicesrvc) DeleteCommittee(ctx context.Context, p *committeeservice.DeleteCommitteePayload) error {
	slog.DebugContext(ctx, "committeeService.delete-committee",
		"committee_uid", p.UID,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
		)
		return wrapError(ctx, err)
	}

	// Execute delete use case
	errDelete := s.committeeWriterOrchestrator.Delete(ctx, *p.UID, parsedRevision)
	if errDelete != nil {
		return wrapError(ctx, errDelete)
	}

	return nil
}

// Get Committee Settings
func (s *committeeServicesrvc) GetCommitteeSettings(ctx context.Context, p *committeeservice.GetCommitteeSettingsPayload) (res *committeeservice.GetCommitteeSettingsResult, err error) {

	slog.DebugContext(ctx, "committeeService.get-committee-settings",
		"committee_uid", p.UID,
	)

	// Execute use case
	committeeSettings, revision, err := s.committeeReaderOrchestrator.GetSettings(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert domain model to GOA response
	result := s.convertSettingsToResponse(committeeSettings)

	// Create result with ETag (using revision from NATS)
	revisionStr := fmt.Sprintf("%d", revision)
	res = &committeeservice.GetCommitteeSettingsResult{
		CommitteeSettings: result,
		Etag:              &revisionStr,
	}

	return res, nil
}

// Update Committee Settings
func (s *committeeServicesrvc) UpdateCommitteeSettings(ctx context.Context, p *committeeservice.UpdateCommitteeSettingsPayload) (res *committeeservice.CommitteeSettingsWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.update-committee-settings",
		"committee_uid", p.UID,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
		)
		return nil, wrapError(ctx, err)
	}

	// Convert payload to domain model
	settings := s.convertPayloadToUpdateSettings(p)

	// Execute use case
	updatedSettings, err := s.committeeWriterOrchestrator.UpdateSettings(ctx, settings, parsedRevision)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertSettingsToResponse(updatedSettings)

	return result, nil
}

// CreateCommitteeMember adds a new member to a committee
func (s *committeeServicesrvc) CreateCommitteeMember(ctx context.Context, p *committeeservice.CreateCommitteeMemberPayload) (res *committeeservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeMemberService.create-committee-member",
		"committee_uid", p.UID,
		"username", p.Username,
		"email", p.Email,
	)

	// Convert payload to domain model
	request := s.convertMemberPayloadToDomain(p)

	// Execute use case
	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, request)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertMemberDomainToFullResponse(response)

	return result, nil
}

// GetCommitteeMember retrieves a specific committee member by UID
func (s *committeeServicesrvc) GetCommitteeMember(ctx context.Context, p *committeeservice.GetCommitteeMemberPayload) (res *committeeservice.GetCommitteeMemberResult, err error) {

	slog.DebugContext(ctx, "committeeMemberService.get-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
	)

	// TODO: Execute use case
	// committeeMember, revision, err := s.committeeMemberReaderOrchestrator.Get(ctx, *p.UID, *p.MemberUID)
	// if err != nil {
	// 	return nil, wrapError(ctx, err)
	// }

	// TODO: Convert domain model to GOA response
	// result := s.convertMemberToResponse(committeeMember)

	// TODO: Create result with ETag (using revision from NATS)
	// revisionStr := fmt.Sprintf("%d", revision)
	// res = &committeeservice.GetCommitteeMemberResult{
	// 	Member: result,
	// 	Etag:   &revisionStr,
	// }

	// TODO: Remove this placeholder return
	return nil, nil
}

// UpdateCommitteeMember updates an existing committee member
func (s *committeeServicesrvc) UpdateCommitteeMember(ctx context.Context, p *committeeservice.UpdateCommitteeMemberPayload) (res *committeeservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeMemberService.update-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
	)

	// TODO: Parse ETag to get revision for optimistic locking
	// parsedRevision, err := etagValidator(p.IfMatch)
	// if err != nil {
	// 	slog.ErrorContext(ctx, "invalid ETag",
	// 		"error", err,
	// 		"etag", p.IfMatch,
	// 		"committee_uid", p.UID,
	// 		"member_uid", p.MemberUID,
	// 	)
	// 	return nil, wrapError(ctx, err)
	// }

	// TODO: Convert payload to domain model
	// committeeMember := s.convertPayloadToUpdateMember(p)

	// TODO: Execute use case
	// updatedMember, err := s.committeeMemberWriterOrchestrator.Update(ctx, committeeMember, parsedRevision)
	// if err != nil {
	// 	return nil, wrapError(ctx, err)
	// }

	// TODO: Convert response to GOA result
	// result := s.convertMemberToResponse(updatedMember)

	// TODO: Remove this placeholder return
	return nil, nil
}

// DeleteCommitteeMember removes a member from a committee
func (s *committeeServicesrvc) DeleteCommitteeMember(ctx context.Context, p *committeeservice.DeleteCommitteeMemberPayload) error {

	slog.DebugContext(ctx, "committeeMemberService.delete-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
	)

	// TODO: Parse ETag to get revision for optimistic locking
	// parsedRevision, err := etagValidator(p.IfMatch)
	// if err != nil {
	// 	slog.ErrorContext(ctx, "invalid ETag",
	// 		"error", err,
	// 		"etag", p.IfMatch,
	// 		"committee_uid", p.UID,
	// 		"member_uid", p.MemberUID,
	// 	)
	// 	return wrapError(ctx, err)
	// }

	// TODO: Execute delete use case
	// errDelete := s.committeeMemberWriterOrchestrator.Delete(ctx, *p.UID, *p.MemberUID, parsedRevision)
	// if errDelete != nil {
	// 	return wrapError(ctx, errDelete)
	// }

	// TODO: Remove this placeholder return
	return nil
}

// Check if the service is able to take inbound requests.
func (s *committeeServicesrvc) Readyz(ctx context.Context) (res []byte, err error) {
	// Check NATS readiness
	if err := s.storage.IsReady(ctx); err != nil {
		slog.ErrorContext(ctx, "service not ready", "error", err)
		return nil, err // This will automatically return ServiceUnavailable
	}

	return []byte("OK\n"), nil
}

// Check if the service is alive.
func (s *committeeServicesrvc) Livez(ctx context.Context) (res []byte, err error) {
	// This always returns as long as the service is still running. As this
	// endpoint is expected to be used as a Kubernetes liveness check, this
	// service must likewise self-detect non-recoverable errors and
	// self-terminate.
	return []byte("OK\n"), nil
}

// NewCommitteeService returns the committee-service service implementation with dependencies.
func NewCommitteeService(createCommitteeUseCase service.CommitteeWriter, readCommitteeUseCase service.CommitteeReader, authService port.Authenticator, storage port.CommitteeReaderWriter) committeeservice.Service {
	return &committeeServicesrvc{
		committeeWriterOrchestrator: createCommitteeUseCase,
		committeeReaderOrchestrator: readCommitteeUseCase,
		auth:                        authService,
		storage:                     storage,
	}
}
