// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"

	committeemembersservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_members_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"

	// TODO: Uncomment when interfaces are implemented
	// "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

	"goa.design/goa/v3/security"
)

// committeeMemberServicesrvc service implementation with clean architecture
type committeeMemberServicesrvc struct {
	// TODO: Uncomment when interfaces are implemented
	// committeeMemberWriterOrchestrator service.CommitteeMemberWriter
	// committeeMemberReaderOrchestrator service.CommitteeMemberReader
	auth port.Authenticator
	// storage port.CommitteeMemberReaderWriter
}

// JWTAuth implements the authorization logic for service "committee-members-service"
// for the "jwt" security scheme.
func (s *committeeMemberServicesrvc) JWTAuth(ctx context.Context, token string, scheme *security.JWTScheme) (context.Context, error) {

	// Parse the Heimdall-authorized principal from the token
	principal, err := s.auth.ParsePrincipal(ctx, token, slog.Default())
	if err != nil {
		slog.ErrorContext(ctx, "committeeMemberService.jwt-auth",
			"error", err,
			"token_length", len(token),
		)
		return ctx, err
	}

	// Return a new context containing the principal as a value
	return context.WithValue(ctx, constants.PrincipalContextID, principal), nil
}

// CreateCommitteeMember adds a new member to a committee
func (s *committeeMemberServicesrvc) CreateCommitteeMember(ctx context.Context, p *committeemembersservice.CreateCommitteeMemberPayload) (res *committeemembersservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeMemberService.create-committee-member",
		"committee_uid", p.UID,
		"username", p.Username,
		"email", p.Email,
	)

	// TODO: Convert payload to DTO
	// request := s.convertPayloadToDomain(p)

	// TODO: Execute use case
	// response, err := s.committeeMemberWriterOrchestrator.Create(ctx, request)
	// if err != nil {
	// 	return nil, wrapError(ctx, err)
	// }

	// TODO: Convert response to GOA result
	// result := s.convertDomainToFullResponse(response)

	// TODO: Remove this placeholder return
	return nil, nil
}

// GetCommitteeMember retrieves a specific committee member by UID
func (s *committeeMemberServicesrvc) GetCommitteeMember(ctx context.Context, p *committeemembersservice.GetCommitteeMemberPayload) (res *committeemembersservice.GetCommitteeMemberResult, err error) {

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
	// res = &committeemembersservice.GetCommitteeMemberResult{
	// 	Member: result,
	// 	Etag:   &revisionStr,
	// }

	// TODO: Remove this placeholder return
	return nil, nil
}

// UpdateCommitteeMember updates an existing committee member
func (s *committeeMemberServicesrvc) UpdateCommitteeMember(ctx context.Context, p *committeemembersservice.UpdateCommitteeMemberPayload) (res *committeemembersservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

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
func (s *committeeMemberServicesrvc) DeleteCommitteeMember(ctx context.Context, p *committeemembersservice.DeleteCommitteeMemberPayload) error {

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

// NewCommitteeMemberService returns the committee-members-service service implementation with dependencies.
func NewCommitteeMemberService(
	// TODO: Uncomment when interfaces are implemented
	// committeeMemberWriterUseCase service.CommitteeMemberWriter,
	// committeeMemberReaderUseCase service.CommitteeMemberReader,
	authService port.Authenticator,
	// storage port.CommitteeMemberReaderWriter,
) committeemembersservice.Service {
	return &committeeMemberServicesrvc{
		// TODO: Uncomment when interfaces are implemented
		// committeeMemberWriterOrchestrator: committeeMemberWriterUseCase,
		// committeeMemberReaderOrchestrator: committeeMemberReaderUseCase,
		auth: authService,
		// storage: storage,
	}
}
