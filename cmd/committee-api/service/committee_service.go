// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"

	"goa.design/goa/v3/security"
)

// committeeServicesrvc service implementation with clean architecture
type committeeServicesrvc struct {
	committeeWriterOrchestrator service.CommitteeWriter
	committeeReaderOrchestrator service.CommitteeReader
	auth                        port.Authenticator
	storage                     port.CommitteeReaderWriter
	publisher                   port.CommitteePublisher
	userReader                  port.UserReader
	linkReader                  service.CommitteeLinkDataReader
	linkWriter                  service.CommitteeLinkDataWriter
	docReader                   service.CommitteeDocumentDataReader
	docWriter                   service.CommitteeDocumentDataWriter
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

	ctx = context.WithValue(ctx, constants.PrincipalContextID, principal)
	return ctx, nil
}

// Create Committee
func (s *committeeServicesrvc) CreateCommittee(ctx context.Context, p *committeeservice.CreateCommitteePayload) (res *committeeservice.CommitteeFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeService.create-committee",
		"project_uid", p.ProjectUID,
		"name", p.Name,
		"x_sync", p.XSync,
	)

	// Convert payload to DTO
	request := s.convertPayloadToDomain(p)

	// Execute use case
	response, err := s.committeeWriterOrchestrator.Create(ctx, request, p.XSync)
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
		"x_sync", p.XSync,
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
	updatedCommittee, err := s.committeeWriterOrchestrator.Update(ctx, committee, parsedRevision, p.XSync)
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
		"x_sync", p.XSync,
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
	errDelete := s.committeeWriterOrchestrator.Delete(ctx, *p.UID, parsedRevision, p.XSync)
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
		"x_sync", p.XSync,
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
	updatedSettings, err := s.committeeWriterOrchestrator.UpdateSettings(ctx, settings, parsedRevision, p.XSync)
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
		"email", redaction.RedactEmail(p.Email),
		"x_sync", p.XSync,
	)

	// Convert payload to domain model
	request := s.convertMemberPayloadToDomain(p)

	// Execute use case
	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, request, p.XSync)
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

	// Execute use case
	committeeMember, revision, err := s.committeeReaderOrchestrator.GetMember(ctx, p.UID, p.MemberUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert domain model to GOA response
	result := s.convertMemberDomainToFullResponse(committeeMember)

	// Create result with ETag (using revision from NATS)
	revisionStr := fmt.Sprintf("%d", revision)
	res = &committeeservice.GetCommitteeMemberResult{
		Member: result,
		Etag:   &revisionStr,
	}

	return res, nil
}

// UpdateCommitteeMember updates an existing committee member
func (s *committeeServicesrvc) UpdateCommitteeMember(ctx context.Context, p *committeeservice.UpdateCommitteeMemberPayload) (res *committeeservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeMemberService.update-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
		"email", redaction.RedactEmail(p.Email),
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
			"member_uid", p.MemberUID,
		)
		return nil, wrapError(ctx, err)
	}

	// Convert payload to domain model
	committeeMember := s.convertPayloadToUpdateMember(p)

	// Execute use case
	updatedMember, err := s.committeeWriterOrchestrator.UpdateMember(ctx, committeeMember, parsedRevision, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertMemberDomainToFullResponse(updatedMember)

	return result, nil
}

// DeleteCommitteeMember removes a member from a committee
func (s *committeeServicesrvc) DeleteCommitteeMember(ctx context.Context, p *committeeservice.DeleteCommitteeMemberPayload) error {

	slog.DebugContext(ctx, "committeeMemberService.delete-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
			"member_uid", p.MemberUID,
		)
		return wrapError(ctx, err)
	}

	// Execute delete use case
	errDelete := s.committeeWriterOrchestrator.DeleteMember(ctx, p.MemberUID, parsedRevision, p.XSync)
	if errDelete != nil {
		return wrapError(ctx, errDelete)
	}

	return nil
}

// GetInvite retrieves a single invite by UID
func (s *committeeServicesrvc) GetInvite(ctx context.Context, p *committeeservice.GetInvitePayload) (*committeeservice.CommitteeInviteWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.get-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, _, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	return s.convertInviteDomainToResponse(invite), nil
}

// CreateInvite creates a new invite for a committee
func (s *committeeServicesrvc) CreateInvite(ctx context.Context, p *committeeservice.CreateInvitePayload) (*committeeservice.CommitteeInviteWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.create-invite",
		"committee_uid", p.UID,
		"invitee_email", redaction.RedactEmail(p.InviteeEmail),
	)

	// Verify committee exists
	_, _, err := s.storage.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	invite := &model.CommitteeInvite{
		UID:          uuid.New().String(),
		CommitteeUID: p.UID,
		InviteeEmail: p.InviteeEmail,
		Status:       "pending",
		CreatedAt:    time.Now().UTC(),
	}
	if p.Role != nil {
		invite.Role = *p.Role
	}

	// Check uniqueness — allow re-inviting if the existing invite is revoked
	_, errUnique := s.storage.UniqueInvite(ctx, invite)
	if errUnique != nil {
		var conflictErr errors.Conflict
		if !stderrors.As(errUnique, &conflictErr) {
			return nil, wrapError(ctx, errUnique)
		}

		// Uniqueness conflict: look for a revoked invite to reinstate
		existing, errList := s.storage.ListInvites(ctx, p.UID)
		if errList != nil {
			return nil, wrapError(ctx, errList)
		}
		var revokedInvite *model.CommitteeInvite
		for _, inv := range existing {
			if strings.EqualFold(inv.InviteeEmail, p.InviteeEmail) && inv.Status == "revoked" {
				revokedInvite = inv
				break
			}
		}
		if revokedInvite == nil {
			return nil, wrapError(ctx, errUnique)
		}

		// Reinstate the revoked invite by setting it back to pending
		_, rev, errGet := s.storage.GetInvite(ctx, revokedInvite.UID)
		if errGet != nil {
			return nil, wrapError(ctx, errGet)
		}
		revokedInvite.Status = "pending"
		if p.Role != nil {
			revokedInvite.Role = *p.Role
		}
		if errUpdate := s.storage.UpdateInvite(ctx, revokedInvite, rev); errUpdate != nil {
			return nil, wrapError(ctx, errUpdate)
		}

		s.publishInviteIndexerMessage(ctx, model.ActionUpdated, revokedInvite, p.XSync)

		return s.convertInviteDomainToResponse(revokedInvite), nil
	}

	if err := s.storage.CreateInvite(ctx, invite); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionCreated, invite, p.XSync)

	return s.convertInviteDomainToResponse(invite), nil
}

// RevokeInvite revokes a pending or declined invite
func (s *committeeServicesrvc) RevokeInvite(ctx context.Context, p *committeeservice.RevokeInvitePayload) error {
	slog.DebugContext(ctx, "committeeService.revoke-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, rev, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	if invite.Status == "accepted" || invite.Status == "revoked" {
		return wrapError(ctx, errors.NewConflict("invite has already been processed"))
	}

	invite.Status = "revoked"
	if err := s.storage.UpdateInvite(ctx, invite, rev); err != nil {
		return wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionUpdated, invite, false)

	return nil
}

// AcceptInvite accepts a pending or previously-declined invite and creates a committee member
func (s *committeeServicesrvc) AcceptInvite(ctx context.Context, p *committeeservice.AcceptInvitePayload) (*committeeservice.CommitteeMemberFullWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.accept-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, rev, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	// Enforce invite ownership: only the invitee can accept their own invite
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	if !strings.EqualFold(email, invite.InviteeEmail) {
		return nil, wrapError(ctx, errors.NewForbidden("you are not the invitee for this invite"))
	}

	if invite.Status == "accepted" || invite.Status == "revoked" {
		return nil, wrapError(ctx, errors.NewConflict("invite has already been processed"))
	}

	// Create the committee member first — if this fails the invite remains pending/declined
	// and the invitee can retry without being stuck in an inconsistent state.
	username, _ := ctx.Value(constants.PrincipalContextID).(string)
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: invite.CommitteeUID,
			Username:     username,
			Email:        invite.InviteeEmail,
			Role:         model.CommitteeMemberRole{Name: invite.Role},
			Status:       "Active",
		},
	}

	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, member, false)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Member created successfully — now mark the invite accepted.
	invite.Status = "accepted"
	if err := s.storage.UpdateInvite(ctx, invite, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionUpdated, invite, false)

	return s.convertMemberDomainToFullResponse(response), nil
}

// DeclineInvite declines a pending invite
func (s *committeeServicesrvc) DeclineInvite(ctx context.Context, p *committeeservice.DeclineInvitePayload) (*committeeservice.CommitteeInviteWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.decline-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, rev, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	// Enforce invite ownership: only the invitee can decline their own invite
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	if !strings.EqualFold(email, invite.InviteeEmail) {
		return nil, wrapError(ctx, errors.NewForbidden("you are not the invitee for this invite"))
	}

	if invite.Status != "pending" {
		return nil, wrapError(ctx, errors.NewConflict("invite has already been processed"))
	}

	invite.Status = "declined"
	if err := s.storage.UpdateInvite(ctx, invite, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionUpdated, invite, false)

	return s.convertInviteDomainToResponse(invite), nil
}

// GetApplication retrieves a single application by UID
func (s *committeeServicesrvc) GetApplication(ctx context.Context, p *committeeservice.GetApplicationPayload) (*committeeservice.CommitteeApplicationWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.get-application",
		"committee_uid", p.UID,
		"application_uid", p.ApplicationUID,
	)

	application, _, err := s.storage.GetApplication(ctx, p.ApplicationUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if application.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("application not found in this committee"))
	}

	return s.convertApplicationDomainToResponse(application), nil
}

// SubmitApplication submits an application to join a committee
func (s *committeeServicesrvc) SubmitApplication(ctx context.Context, p *committeeservice.SubmitApplicationPayload) (*committeeservice.CommitteeApplicationWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.submit-application",
		"committee_uid", p.UID,
	)

	// Verify committee exists and get base to check join_mode
	base, _, err := s.storage.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if base.JoinMode != "application" {
		return nil, wrapError(ctx, errors.NewForbidden("committee does not accept applications"))
	}

	// Resolve the applicant's email via the auth-service NATS lookup.
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	slog.DebugContext(ctx, "committeeService.submit-application: resolved applicant",
		"committee_uid", p.UID,
		"applicant_email_redacted", redaction.RedactEmail(email),
	)

	application := &model.CommitteeApplication{
		UID:            uuid.New().String(),
		CommitteeUID:   p.UID,
		ApplicantEmail: email,
		Status:         "pending",
		CreatedAt:      time.Now().UTC(),
	}
	if p.Message != nil {
		application.Message = *p.Message
	}

	// Check uniqueness — allow reapplying if the existing application is rejected
	_, errUnique := s.storage.UniqueApplication(ctx, application)
	if errUnique != nil {
		var conflictErr errors.Conflict
		if !stderrors.As(errUnique, &conflictErr) {
			return nil, wrapError(ctx, errUnique)
		}

		// Uniqueness conflict: look for a rejected application to reinstate
		existing, errList := s.storage.ListApplications(ctx, p.UID)
		if errList != nil {
			return nil, wrapError(ctx, errList)
		}
		var rejectedApp *model.CommitteeApplication
		for _, app := range existing {
			if strings.EqualFold(app.ApplicantEmail, email) && app.Status == "rejected" {
				rejectedApp = app
				break
			}
		}
		if rejectedApp == nil {
			return nil, wrapError(ctx, errUnique)
		}

		// Reinstate the rejected application by setting it back to pending
		_, rev, errGet := s.storage.GetApplication(ctx, rejectedApp.UID)
		if errGet != nil {
			return nil, wrapError(ctx, errGet)
		}
		rejectedApp.Status = "pending"
		rejectedApp.ReviewerNotes = ""
		if p.Message != nil {
			rejectedApp.Message = *p.Message
		}
		if errUpdate := s.storage.UpdateApplication(ctx, rejectedApp, rev); errUpdate != nil {
			return nil, wrapError(ctx, errUpdate)
		}

		s.publishApplicationIndexerMessage(ctx, model.ActionUpdated, rejectedApp, p.XSync)

		return s.convertApplicationDomainToResponse(rejectedApp), nil
	}

	if err := s.storage.CreateApplication(ctx, application); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishApplicationIndexerMessage(ctx, model.ActionCreated, application, p.XSync)

	return s.convertApplicationDomainToResponse(application), nil
}

// ApproveApplication approves a pending application and creates a committee member
func (s *committeeServicesrvc) ApproveApplication(ctx context.Context, p *committeeservice.ApproveApplicationPayload) (*committeeservice.CommitteeMemberFullWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.approve-application",
		"committee_uid", p.UID,
		"application_uid", p.ApplicationUID,
	)

	application, rev, err := s.storage.GetApplication(ctx, p.ApplicationUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if application.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("application not found in this committee"))
	}

	if application.Status != "pending" {
		return nil, wrapError(ctx, errors.NewConflict("application has already been processed"))
	}

	// Create the committee member first — if this fails the application remains pending
	// and the reviewer can retry without being stuck in an inconsistent state.
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: application.CommitteeUID,
			Email:        application.ApplicantEmail,
			Status:       "Active",
		},
	}

	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, member, false)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Member created successfully — now mark the application approved.
	application.Status = "approved"
	if p.ReviewerNotes != nil {
		application.ReviewerNotes = *p.ReviewerNotes
	}

	if err := s.storage.UpdateApplication(ctx, application, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishApplicationIndexerMessage(ctx, model.ActionUpdated, application, false)

	return s.convertMemberDomainToFullResponse(response), nil
}

// RejectApplication rejects a pending application
func (s *committeeServicesrvc) RejectApplication(ctx context.Context, p *committeeservice.RejectApplicationPayload) (*committeeservice.CommitteeApplicationWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.reject-application",
		"committee_uid", p.UID,
		"application_uid", p.ApplicationUID,
	)

	application, rev, err := s.storage.GetApplication(ctx, p.ApplicationUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if application.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("application not found in this committee"))
	}

	if application.Status != "pending" {
		return nil, wrapError(ctx, errors.NewConflict("application has already been processed"))
	}

	application.Status = "rejected"
	if p.ReviewerNotes != nil {
		application.ReviewerNotes = *p.ReviewerNotes
	}

	if err := s.storage.UpdateApplication(ctx, application, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishApplicationIndexerMessage(ctx, model.ActionUpdated, application, false)

	return s.convertApplicationDomainToResponse(application), nil
}

// JoinCommittee allows self-join when join_mode is "open"
func (s *committeeServicesrvc) JoinCommittee(ctx context.Context, p *committeeservice.JoinCommitteePayload) (*committeeservice.CommitteeMemberFullWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.join-committee", "committee_uid", p.UID)

	// Verify committee exists and get base to check join_mode
	base, _, err := s.storage.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if base.JoinMode != "open" {
		return nil, wrapError(ctx, errors.NewForbidden("committee join_mode is not open"))
	}

	// Get username from context — Heimdall injects the user's username as a JWT claim.
	username, _ := ctx.Value(constants.PrincipalContextID).(string)
	if username == "" {
		return nil, wrapError(ctx, errors.NewValidation("unable to determine user username from identity"))
	}

	// Resolve the user's email via the auth-service NATS lookup.
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Create member via the existing orchestrator
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: p.UID,
			Username:     username,
			Email:        email,
			Status:       "Active",
		},
	}

	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, member, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	return s.convertMemberDomainToFullResponse(response), nil
}

// LeaveCommittee allows a member to leave a committee
func (s *committeeServicesrvc) LeaveCommittee(ctx context.Context, p *committeeservice.LeaveCommitteePayload) error {
	slog.DebugContext(ctx, "committeeService.leave-committee", "committee_uid", p.UID)

	// Resolve the user's email via the auth-service NATS lookup.
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return wrapError(ctx, err)
	}

	// Find the member by listing all members and matching email
	members, err := s.storage.ListMembers(ctx, p.UID)
	if err != nil {
		return wrapError(ctx, err)
	}

	var memberToRemove *model.CommitteeMember
	for _, m := range members {
		if strings.EqualFold(m.Email, email) {
			memberToRemove = m
			break
		}
	}

	if memberToRemove == nil {
		return wrapError(ctx, errors.NewNotFound("you are not a member of this committee"))
	}

	// Get revision for optimistic locking
	rev, err := s.storage.GetMemberRevision(ctx, memberToRemove.UID)
	if err != nil {
		return wrapError(ctx, err)
	}

	// Use orchestrator (not direct storage) to ensure event publishing and cleanup
	if err := s.committeeWriterOrchestrator.DeleteMember(ctx, memberToRemove.UID, rev, p.XSync); err != nil {
		return wrapError(ctx, err)
	}

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

// resolveCallerEmail looks up the primary email for the authenticated caller by sending
// their principal (from context) to the auth-service via NATS.
func (s *committeeServicesrvc) resolveCallerEmail(ctx context.Context) (string, error) {
	if s.userReader == nil {
		return "", errors.NewServiceUnavailable("user reader is not configured")
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return "", errors.NewValidation("unable to determine user identity from token")
	}

	userEmails, err := s.userReader.EmailsByPrincipal(ctx, principal)
	if err != nil {
		return "", err
	}

	if userEmails.PrimaryEmail == "" {
		return "", errors.NewValidation("no primary email found for user")
	}

	return userEmails.PrimaryEmail, nil
}

// publishInviteIndexerMessage publishes an indexer message for invite operations.
// Publishing is best-effort: failures are logged but do not fail the request.
// IndexingConfig is required because there is no server-side enricher for committee_invite.
func (s *committeeServicesrvc) publishInviteIndexerMessage(ctx context.Context, action model.MessageAction, invite *model.CommitteeInvite, sync bool) {
	tags := invite.Tags()
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             invite.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", invite.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", invite.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", invite.CommitteeUID)},
		SortName:             invite.InviteeEmail,
		NameAndAliases:       []string{invite.InviteeEmail},
		Fulltext:             invite.InviteeEmail,
		Tags:                 tags,
	}

	var data any
	if action == model.ActionDeleted {
		data = invite.UID
	} else {
		public := false
		indexingConfig.Public = &public
		data = invite
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action:         action,
		Tags:           tags,
		IndexingConfig: indexingConfig,
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build invite indexer message",
			"error", err,
			"action", string(action),
			"invite_uid", invite.UID,
		)
		return
	}

	if pubErr := s.publisher.Indexer(ctx, constants.IndexCommitteeInviteSubject, built, sync); pubErr != nil {
		slog.WarnContext(ctx, "failed to publish invite indexer message",
			"error", pubErr,
			"action", string(action),
			"invite_uid", invite.UID,
		)
	}
}

// publishApplicationIndexerMessage publishes an indexer message for application operations.
// Publishing is best-effort: failures are logged but do not fail the request.
// IndexingConfig is required because there is no server-side enricher for committee_application.
func (s *committeeServicesrvc) publishApplicationIndexerMessage(ctx context.Context, action model.MessageAction, application *model.CommitteeApplication, sync bool) {
	tags := application.Tags()
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             application.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", application.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", application.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", application.CommitteeUID)},
		Fulltext:             application.Message,
		Tags:                 tags,
	}

	var data any
	if action == model.ActionDeleted {
		data = application.UID
	} else {
		public := false
		indexingConfig.Public = &public
		data = application
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action:         action,
		Tags:           tags,
		IndexingConfig: indexingConfig,
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build application indexer message",
			"error", err,
			"action", string(action),
			"application_uid", application.UID,
		)
		return
	}

	if pubErr := s.publisher.Indexer(ctx, constants.IndexCommitteeApplicationSubject, built, sync); pubErr != nil {
		slog.WarnContext(ctx, "failed to publish application indexer message",
			"error", pubErr,
			"action", string(action),
			"application_uid", application.UID,
		)
	}
}

// NewCommitteeService returns the committee-service service implementation with dependencies.
func NewCommitteeService(createCommitteeUseCase service.CommitteeWriter, readCommitteeUseCase service.CommitteeReader, authService port.Authenticator, storage port.CommitteeReaderWriter, publisher port.CommitteePublisher, userReader port.UserReader, linkReader service.CommitteeLinkDataReader, linkWriter service.CommitteeLinkDataWriter, docReader service.CommitteeDocumentDataReader, docWriter service.CommitteeDocumentDataWriter) committeeservice.Service {
	return &committeeServicesrvc{
		committeeWriterOrchestrator: createCommitteeUseCase,
		committeeReaderOrchestrator: readCommitteeUseCase,
		auth:                        authService,
		storage:                     storage,
		publisher:                   publisher,
		userReader:                  userReader,
		linkReader:                  linkReader,
		linkWriter:                  linkWriter,
		docReader:                   docReader,
		docWriter:                   docWriter,
	}
}

// ListCommitteeLinks returns all links for a committee, optionally filtered by folder.
func (s *committeeServicesrvc) ListCommitteeLinks(ctx context.Context, p *committeeservice.ListCommitteeLinksPayload) (res []*committeeservice.CommitteeLinkWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.list-committee-links", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	links, err := s.linkReader.ListLinks(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	result := make([]*committeeservice.CommitteeLinkWithReadonlyAttributes, 0, len(links))
	for _, l := range links {
		if p.FolderUID != nil && (l.FolderUID == nil || *l.FolderUID != *p.FolderUID) {
			continue
		}
		result = append(result, domainLinkToGoa(l))
	}
	return result, nil
}

// CreateCommitteeLink creates a new link for a committee.
func (s *committeeServicesrvc) CreateCommitteeLink(ctx context.Context, p *committeeservice.CreateCommitteeLinkPayload) (res *committeeservice.CommitteeLinkWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.create-committee-link", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return nil, errors.NewValidation("unable to determine user identity from token")
	}

	link := &model.CommitteeLink{
		CommitteeUID: *p.UID,
		Name:         p.Name,
		URL:          p.URL,
		CreatedByUID: principal,
	}
	if p.Description != nil {
		link.Description = *p.Description
	}
	if p.FolderUID != nil {
		link.FolderUID = p.FolderUID
	}
	if p.CreatedByName != nil {
		link.CreatedByName = *p.CreatedByName
	}

	created, err := s.linkWriter.CreateLink(ctx, link)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	return domainLinkToGoa(created), nil
}

// GetCommitteeLink returns a single link for a committee with an ETag.
func (s *committeeServicesrvc) GetCommitteeLink(ctx context.Context, p *committeeservice.GetCommitteeLinkPayload) (res *committeeservice.GetCommitteeLinkResult, err error) {
	slog.DebugContext(ctx, "committeeService.get-committee-link", "committee_uid", p.UID, "link_uid", p.LinkUID)

	link, revision, err := s.linkReader.GetLink(ctx, *p.UID, *p.LinkUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	revisionStr := fmt.Sprintf("%d", revision)
	return &committeeservice.GetCommitteeLinkResult{
		CommitteeLink: domainLinkToGoa(link),
		Etag:          &revisionStr,
	}, nil
}

// DeleteCommitteeLink removes a link from a committee.
func (s *committeeServicesrvc) DeleteCommitteeLink(ctx context.Context, p *committeeservice.DeleteCommitteeLinkPayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee-link", "committee_uid", p.UID, "link_uid", p.LinkUID)

	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag", "error", err, "etag", p.IfMatch, "link_uid", p.LinkUID)
		return wrapError(ctx, err)
	}

	if err := s.linkWriter.DeleteLink(ctx, *p.UID, *p.LinkUID, parsedRevision); err != nil {
		return wrapError(ctx, err)
	}
	return nil
}

// ListCommitteeLinkFolders returns all link folders for a committee.
func (s *committeeServicesrvc) ListCommitteeLinkFolders(ctx context.Context, p *committeeservice.ListCommitteeLinkFoldersPayload) (res []*committeeservice.CommitteeLinkFolderWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.list-committee-link-folders", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	folders, err := s.linkReader.ListLinkFolders(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	result := make([]*committeeservice.CommitteeLinkFolderWithReadonlyAttributes, 0, len(folders))
	for _, f := range folders {
		result = append(result, domainFolderToGoa(f))
	}
	return result, nil
}

// GetCommitteeLinkFolder returns a single link folder for a committee with an ETag.
func (s *committeeServicesrvc) GetCommitteeLinkFolder(ctx context.Context, p *committeeservice.GetCommitteeLinkFolderPayload) (res *committeeservice.GetCommitteeLinkFolderResult, err error) {
	slog.DebugContext(ctx, "committeeService.get-committee-link-folder", "committee_uid", p.UID, "folder_uid", p.FolderUID)

	folder, revision, err := s.linkReader.GetLinkFolder(ctx, *p.UID, *p.FolderUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	revisionStr := fmt.Sprintf("%d", revision)
	return &committeeservice.GetCommitteeLinkFolderResult{
		CommitteeLinkFolder: domainFolderToGoa(folder),
		Etag:                &revisionStr,
	}, nil
}

// CreateCommitteeLinkFolder creates a new link folder for a committee.
func (s *committeeServicesrvc) CreateCommitteeLinkFolder(ctx context.Context, p *committeeservice.CreateCommitteeLinkFolderPayload) (res *committeeservice.CommitteeLinkFolderWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.create-committee-link-folder", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return nil, errors.NewValidation("unable to determine user identity from token")
	}

	folder := &model.CommitteeLinkFolder{
		CommitteeUID: *p.UID,
		Name:         p.Name,
		CreatedByUID: principal,
	}
	if p.CreatedByName != nil {
		folder.CreatedByName = *p.CreatedByName
	}

	created, err := s.linkWriter.CreateLinkFolder(ctx, folder)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	return domainFolderToGoa(created), nil
}

// DeleteCommitteeLinkFolder removes a link folder from a committee.
func (s *committeeServicesrvc) DeleteCommitteeLinkFolder(ctx context.Context, p *committeeservice.DeleteCommitteeLinkFolderPayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee-link-folder", "committee_uid", p.UID, "folder_uid", p.FolderUID)

	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag", "error", err, "etag", p.IfMatch, "folder_uid", p.FolderUID)
		return wrapError(ctx, err)
	}

	if err := s.linkWriter.DeleteLinkFolder(ctx, *p.UID, *p.FolderUID, parsedRevision); err != nil {
		return wrapError(ctx, err)
	}
	return nil
}

// domainLinkToGoa converts a domain CommitteeLink to its Goa result type.
func domainLinkToGoa(l *model.CommitteeLink) *committeeservice.CommitteeLinkWithReadonlyAttributes {
	uid := l.UID
	committeeUID := l.CommitteeUID
	name := l.Name
	url := l.URL
	createdAt := l.CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := l.UpdatedAt.UTC().Format(time.RFC3339)

	res := &committeeservice.CommitteeLinkWithReadonlyAttributes{
		UID:          &uid,
		CommitteeUID: &committeeUID,
		FolderUID:    l.FolderUID,
		Name:         &name,
		URL:          &url,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
	if l.Description != "" {
		desc := l.Description
		res.Description = &desc
	}
	if l.CreatedByUID != "" {
		v := l.CreatedByUID
		res.CreatedByUID = &v
	}
	if l.CreatedByName != "" {
		v := l.CreatedByName
		res.CreatedByName = &v
	}
	return res
}

// domainFolderToGoa converts a domain CommitteeLinkFolder to its Goa result type.
func domainFolderToGoa(f *model.CommitteeLinkFolder) *committeeservice.CommitteeLinkFolderWithReadonlyAttributes {
	uid := f.UID
	committeeUID := f.CommitteeUID
	name := f.Name
	createdAt := f.CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := f.UpdatedAt.UTC().Format(time.RFC3339)

	res := &committeeservice.CommitteeLinkFolderWithReadonlyAttributes{
		UID:          &uid,
		CommitteeUID: &committeeUID,
		Name:         &name,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
	if f.CreatedByUID != "" {
		v := f.CreatedByUID
		res.CreatedByUID = &v
	}
	if f.CreatedByName != "" {
		v := f.CreatedByName
		res.CreatedByName = &v
	}
	return res
}

// ─── Committee Document Endpoints ───

// UploadCommitteeDocument handles multipart file upload for a committee document.
func (s *committeeServicesrvc) UploadCommitteeDocument(ctx context.Context, p *committeeservice.UploadCommitteeDocumentPayload) (res *committeeservice.CommitteeDocumentWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.upload-committee-document", "committee_uid", p.UID)

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return nil, errors.NewValidation("unable to determine user identity from token")
	}

	// Verify committee exists before attaching a document to it.
	if _, _, err := s.storage.GetBase(ctx, p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	doc := &model.CommitteeDocument{
		CommitteeUID:  p.UID,
		Name:          p.Name,
		UploadedByUID: principal,
	}
	if p.Description != nil {
		doc.Description = *p.Description
	}
	if p.UploadedByName != nil {
		doc.UploadedByName = *p.UploadedByName
	}
	doc.FileName = p.FileName
	doc.ContentType = p.ContentType

	created, err := s.docWriter.UploadDocument(ctx, doc, p.File)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	return domainDocumentToGoa(created), nil
}

// ListCommitteeDocuments returns all documents for a committee.
func (s *committeeServicesrvc) ListCommitteeDocuments(ctx context.Context, p *committeeservice.ListCommitteeDocumentsPayload) (res []*committeeservice.CommitteeDocumentWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.list-committee-documents", "committee_uid", p.UID)

	// Verify committee exists so unknown UIDs return 404, not 200 with empty list.
	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	docs, err := s.docReader.ListDocuments(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	result := make([]*committeeservice.CommitteeDocumentWithReadonlyAttributes, 0, len(docs))
	for _, d := range docs {
		result = append(result, domainDocumentToGoa(d))
	}
	return result, nil
}

// GetCommitteeDocument returns metadata for a single committee document with an ETag.
func (s *committeeServicesrvc) GetCommitteeDocument(ctx context.Context, p *committeeservice.GetCommitteeDocumentPayload) (res *committeeservice.GetCommitteeDocumentResult, err error) {
	slog.DebugContext(ctx, "committeeService.get-committee-document", "committee_uid", p.UID, "document_uid", p.DocumentUID)

	doc, revision, err := s.docReader.GetDocumentMetadata(ctx, *p.UID, *p.DocumentUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	revisionStr := fmt.Sprintf("%d", revision)
	return &committeeservice.GetCommitteeDocumentResult{
		CommitteeDocument: domainDocumentToGoa(doc),
		Etag:              &revisionStr,
	}, nil
}

// DownloadCommitteeDocument returns the raw file data for a committee document.
// The returned io.ReadCloser also implements io.WriterTo so it can set
// Content-Type and Content-Disposition headers before writing bytes to the
// http.ResponseWriter (Goa calls WriteTo after the empty EncodeResponse no-op).
func (s *committeeServicesrvc) DownloadCommitteeDocument(ctx context.Context, p *committeeservice.DownloadCommitteeDocumentPayload) (body io.ReadCloser, err error) {
	slog.DebugContext(ctx, "committeeService.download-committee-document", "committee_uid", p.UID, "document_uid", p.DocumentUID)

	doc, _, err := s.docReader.GetDocumentMetadata(ctx, *p.UID, *p.DocumentUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	fileData, err := s.docReader.GetDocumentFile(ctx, *p.DocumentUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	return &documentDownloadBody{
		data:        fileData,
		contentType: doc.ContentType,
		fileName:    doc.FileName,
	}, nil
}

// DeleteCommitteeDocument deletes a committee document using optimistic locking via If-Match.
func (s *committeeServicesrvc) DeleteCommitteeDocument(ctx context.Context, p *committeeservice.DeleteCommitteeDocumentPayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee-document", "committee_uid", p.UID, "document_uid", p.DocumentUID)

	revision, err := etagValidator(&p.IfMatch)
	if err != nil {
		return wrapError(ctx, err)
	}

	if err := s.docWriter.DeleteDocument(ctx, p.UID, p.DocumentUID, revision); err != nil {
		return wrapError(ctx, err)
	}
	return nil
}

// domainDocumentToGoa converts a domain CommitteeDocument to its Goa result type.
func domainDocumentToGoa(d *model.CommitteeDocument) *committeeservice.CommitteeDocumentWithReadonlyAttributes {
	uid := d.UID
	committeeUID := d.CommitteeUID
	name := d.Name
	fileName := d.FileName
	fileSize := d.FileSize
	contentType := d.ContentType
	createdAt := d.CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := d.UpdatedAt.UTC().Format(time.RFC3339)

	res := &committeeservice.CommitteeDocumentWithReadonlyAttributes{
		UID:          &uid,
		CommitteeUID: &committeeUID,
		Name:         &name,
		FileName:     &fileName,
		FileSize:     &fileSize,
		ContentType:  &contentType,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
	if d.Description != "" {
		v := d.Description
		res.Description = &v
	}
	if d.UploadedByUID != "" {
		v := d.UploadedByUID
		res.UploadedByUID = &v
	}
	if d.UploadedByName != "" {
		v := d.UploadedByName
		res.UploadedByName = &v
	}
	return res
}

// documentDownloadBody is an io.ReadCloser that also implements io.WriterTo.
// When Goa calls WriteTo(w) with the http.ResponseWriter, it sets the
// Content-Type and Content-Disposition headers before writing file bytes,
// ensuring headers are sent before the body without touching generated code.
type documentDownloadBody struct {
	data        []byte
	contentType string
	fileName    string
	offset      int
}

func (b *documentDownloadBody) Read(p []byte) (n int, err error) {
	if b.offset >= len(b.data) {
		return 0, io.EOF
	}
	n = copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *documentDownloadBody) Close() error { return nil }

func (b *documentDownloadBody) WriteTo(w io.Writer) (int64, error) {
	if hw, ok := w.(http.ResponseWriter); ok {
		if b.contentType != "" {
			hw.Header().Set("Content-Type", b.contentType)
		}
		if b.fileName != "" {
			// Sanitize filename for RFC 6266 compliance: replace characters
			// that could cause header injection or malformed headers.
			safeName := strings.Map(func(r rune) rune {
				if r == '"' || r == '\\' || r == '\n' || r == '\r' {
					return '_'
				}
				return r
			}, b.fileName)
			hw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeName))
		}
	}
	n, err := w.Write(b.data)
	return int64(n), err
}
