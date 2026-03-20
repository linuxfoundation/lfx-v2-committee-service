// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
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

	// Verify committee exists and get settings to check join_mode
	settings, _, err := s.storage.GetSettings(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if settings.JoinMode != "application" {
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

	// Verify committee exists and get settings to check join_mode
	settings, _, err := s.storage.GetSettings(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if settings.JoinMode != "open" {
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
func NewCommitteeService(createCommitteeUseCase service.CommitteeWriter, readCommitteeUseCase service.CommitteeReader, authService port.Authenticator, storage port.CommitteeReaderWriter, publisher port.CommitteePublisher, userReader port.UserReader) committeeservice.Service {
	return &committeeServicesrvc{
		committeeWriterOrchestrator: createCommitteeUseCase,
		committeeReaderOrchestrator: readCommitteeUseCase,
		auth:                        authService,
		storage:                     storage,
		publisher:                   publisher,
		userReader:                  userReader,
	}
}
