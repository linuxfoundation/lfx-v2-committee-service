// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/concurrent"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

// type committeeWriterOrchestrator from committee_writer.go

// CreateMember creates a new committee member includes validation and rollback support
func (uc *committeeWriterOrchestrator) CreateMember(ctx context.Context, committeeUID string, member *model.CommitteeMember) (*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "creating committee member",
		"committee_uid", committeeUID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
	)

	now := time.Now()
	member.UID = uuid.New().String()
	member.CreatedAt = now
	member.UpdatedAt = now

	// Track resources for rollback purposes
	var (
		memberCreated    bool
		rollbackRequired bool
	)
	defer func() {
		if err := recover(); err != nil || rollbackRequired {
			uc.rollbackMemberCreation(ctx, committeeUID, member.UID, memberCreated)
		}
	}()

	// Step 1: Validate that the committee exists
	committee, committeeRevision, errCommittee := uc.committeeReader.GetBase(ctx, committeeUID)
	if errCommittee != nil {
		slog.ErrorContext(ctx, "committee not found",
			"error", errCommittee,
			"committee_uid", committeeUID,
		)
		return nil, errCommittee
	}

	slog.DebugContext(ctx, "committee found",
		"committee_uid", committee.UID,
		"committee_name", committee.Name,
		"committee_category", committee.Category,
		"revision", committeeRevision,
	)

	// Get committee settings to check business email requirements
	var settings *model.CommitteeSettings
	settings, _, errSettings := uc.committeeReader.GetSettings(ctx, committeeUID)
	if errSettings != nil && !errors.Is(errSettings, errs.NotFound{}) {
		slog.ErrorContext(ctx, "failed to retrieve committee settings",
			"error", errSettings,
			"committee_uid", committeeUID,
		)
		return nil, errSettings
	}
	// Use empty settings if not found
	if settings == nil {
		settings = &model.CommitteeSettings{}
	}

	slog.DebugContext(ctx, "committee settings retrieved",
		"committee_uid", committeeUID,
		"business_email_required", settings.BusinessEmailRequired,
	)

	// Step 2: Validate member against committee requirements (domain validation)
	fullCommittee := &model.Committee{CommitteeBase: *committee, CommitteeSettings: settings}
	if errValidation := member.Validate(fullCommittee); errValidation != nil {
		slog.ErrorContext(ctx, "committee member validation failed",
			"error", errValidation,
			"member_uid", member.UID,
			"committee_uid", committeeUID,
			"committee_category", committee.Category,
			"member_email", redaction.RedactEmail(member.Email),
			"member_username", redaction.Redact(member.Username),
			"has_agency", member.Agency != "",
			"has_country", member.Country != "",
		)
		return nil, errValidation
	}

	// Step 3: Validate business email domain if required
	if settings.BusinessEmailRequired {
		if errEmailValidation := uc.validateCorporateEmailDomain(ctx, member.Email); errEmailValidation != nil {
			slog.WarnContext(ctx, "corporate email domain validation failed",
				"error", errEmailValidation,
				"email", redaction.RedactEmail(member.Email),
				"committee_uid", committeeUID,
			)
			return nil, errEmailValidation
		}
	}

	// Step 4: Check if member already exists in committee
	if errMemberExists := uc.validateMemberUniqueness(ctx, committeeUID, member); errMemberExists != nil {
		slog.WarnContext(ctx, "member already exists in committee",
			"error", errMemberExists,
			"committee_uid", committeeUID,
			"member_email", redaction.RedactEmail(member.Email),
		)
		return nil, errMemberExists
	}

	// Step 5: Validate username exists
	if errUsername := uc.validateUsernameExists(ctx, member.Username); errUsername != nil {
		slog.WarnContext(ctx, "username validation failed",
			"error", errUsername,
			"username", redaction.Redact(member.Username),
		)
		return nil, errUsername
	}

	// Step 6: Validate organization exists (external service call)
	if errOrganization := uc.validateOrganizationExists(ctx, member.Organization.Name); errOrganization != nil {
		slog.WarnContext(ctx, "organization validation failed",
			"error", errOrganization,
			"organization", member.Organization.Name,
		)
		return nil, errOrganization
	}

	// Step 7: Create the member record with rollback support
	errCreate := uc.committeeWriter.CreateMember(ctx, committeeUID, member)
	if errCreate != nil {
		slog.ErrorContext(ctx, "failed to create committee member",
			"error", errCreate,
			"committee_uid", committeeUID,
			"member_uid", member.UID,
		)
		return nil, errCreate
	}
	memberCreated = true

	slog.DebugContext(ctx, "committee member created successfully",
		"committee_uid", committeeUID,
		"member_uid", member.UID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
	)

	// Step 8: Add organization user engagement
	if errEngagement := uc.addOrganizationUserEngagement(ctx, member.Organization.Name, member.Username); errEngagement != nil {
		// Log the error but don't fail the member creation
		slog.WarnContext(ctx, "failed to add organization user engagement",
			"error", errEngagement,
			"organization", member.Organization.Name,
			"username", redaction.Redact(member.Username),
			"committee_uid", committeeUID,
			"member_uid", member.UID,
		)
	}

	// Step 9: Publish indexer and access control messages
	if errPublish := uc.publishMemberMessages(ctx, committeeUID, member); errPublish != nil {
		// Log the error but don't fail the member creation
		slog.WarnContext(ctx, "failed to publish member messages",
			"error", errPublish,
			"committee_uid", committeeUID,
			"member_uid", member.UID,
		)
	}

	return member, nil
}

// UpdateMember updates an existing committee member (placeholder implementation)
func (uc *committeeWriterOrchestrator) UpdateMember(ctx context.Context, committeeUID string, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	// TODO: Implement committee member update logic
	slog.DebugContext(ctx, "updating committee member (placeholder)",
		"committee_uid", committeeUID,
		"member_uid", member.UID,
		"revision", revision,
	)
	return nil, errs.NewUnexpected("committee member update not yet implemented")
}

// DeleteMember removes a committee member (placeholder implementation)
func (uc *committeeWriterOrchestrator) DeleteMember(ctx context.Context, committeeUID, memberUID string, revision uint64) error {
	// TODO: Implement committee member deletion logic
	slog.DebugContext(ctx, "deleting committee member (placeholder)",
		"committee_uid", committeeUID,
		"member_uid", memberUID,
		"revision", revision,
	)
	return errs.NewUnexpected("committee member deletion not yet implemented")
}

// validateCorporateEmailDomain validates if the email domain is a corporate domain
// TODO: Implement actual corporate email domain validation logic
func (uc *committeeWriterOrchestrator) validateCorporateEmailDomain(ctx context.Context, email string) error {
	slog.DebugContext(ctx, "validating corporate email domain (placeholder)",
		"email", redaction.RedactEmail(email),
	)

	// For now, we'll implement basic validation - reject common personal email domains
	personalDomains := []string{
		"gmail.com", "yahoo.com", "hotmail.com", "outlook.com",
		"aol.com", "icloud.com", "protonmail.com", "yandex.com",
	}

	emailParts := strings.Split(email, "@")
	if len(emailParts) != 2 {
		return errs.NewValidation("invalid email format")
	}

	domain := strings.ToLower(emailParts[1])
	for _, personalDomain := range personalDomains {
		if domain == personalDomain {
			return errs.NewValidation("personal email domains are not allowed for this committee")
		}
	}

	// TODO: Implement more sophisticated corporate domain validation
	// This could involve checking against a whitelist of known corporate domains
	// or using an external service to validate corporate domains

	return nil
}

// validateMemberUniqueness checks if a member with the same email already exists in the committee
func (uc *committeeWriterOrchestrator) validateMemberUniqueness(ctx context.Context, committeeUID string, member *model.CommitteeMember) error {
	slog.DebugContext(ctx, "validating member uniqueness",
		"committee_uid", committeeUID,
		"member_email", redaction.RedactEmail(member.Email),
	)

	// Try to create a unique key for the member
	// This will fail if a member with the same email already exists
	_, errUnique := uc.committeeWriter.UniqueMemberUsername(ctx, committeeUID, member)
	if errUnique != nil {
		if errors.As(errUnique, &errs.Conflict{}) {
			return errs.NewConflict("a member with this email already exists in the committee")
		}
		return errUnique
	}

	return nil
}

// validateUsernameExists validates if the username exists in external systems
// TODO: Implement actual external service integration
func (uc *committeeWriterOrchestrator) validateUsernameExists(ctx context.Context, username string) error {
	slog.DebugContext(ctx, "validating username exists (placeholder)",
		"username", redaction.Redact(username),
	)

	// TODO: Implement actual username validation against external services
	// This could involve calling LFX user service or similar
	// For now, we'll just validate that username is not empty
	if username == "" {
		return errs.NewValidation("username is required")
	}

	// TODO: Add actual external service call here
	// Example: call to /users/{username} endpoint to verify user exists

	return nil
}

// validateOrganizationExists validates if the organization exists in external systems
// TODO: Implement actual external service integration
func (uc *committeeWriterOrchestrator) validateOrganizationExists(ctx context.Context, organizationName string) error {
	slog.DebugContext(ctx, "validating organization exists (placeholder)",
		"organization", redaction.Redact(organizationName),
	)

	// TODO: Implement actual organization validation against external services
	// This could involve calling LFX organization service or similar
	// For now, we'll just validate that organization name is not empty
	if organizationName == "" {
		return errs.NewValidation("organization name is required")
	}

	// TODO: Add actual external service call here
	// Example: call to /organizations endpoint to verify organization exists

	return nil
}

// addOrganizationUserEngagement adds user engagement to organization
// TODO: Implement actual external API integration
func (uc *committeeWriterOrchestrator) addOrganizationUserEngagement(ctx context.Context, organizationName, username string) error {
	slog.DebugContext(ctx, "adding organization user engagement (placeholder)",
		"organization", redaction.Redact(organizationName),
		"username", redaction.Redact(username),
	)

	// TODO: Implement actual external API call
	// Example: POST /orgs/{org}/users/{username}/engagements
	// This should add the user engagement record to track committee participation

	return nil
}

// rollbackMemberCreation handles rollback of member creation in case of failures
func (uc *committeeWriterOrchestrator) rollbackMemberCreation(ctx context.Context, committeeUID, memberUID string, memberCreated bool) {
	slog.WarnContext(ctx, "rolling back member creation",
		"committee_uid", committeeUID,
		"member_uid", memberUID,
		"member_created", memberCreated,
	)

	if memberCreated {
		// Get member revision for deletion
		rev, errRev := uc.committeeReader.GetMemberRevision(ctx, committeeUID, memberUID)
		if errRev != nil {
			slog.ErrorContext(ctx, "failed to get member revision for rollback",
				"error", errRev,
				"committee_uid", committeeUID,
				"member_uid", memberUID,
				log.PriorityCritical(),
			)
			return
		}

		// Delete the created member
		errDelete := uc.committeeWriter.DeleteMember(ctx, committeeUID, memberUID, rev)
		if errDelete != nil {
			slog.ErrorContext(ctx, "failed to delete member during rollback",
				"error", errDelete,
				"committee_uid", committeeUID,
				"member_uid", memberUID,
				log.PriorityCritical(),
			)
		} else {
			slog.DebugContext(ctx, "successfully rolled back member creation",
				"committee_uid", committeeUID,
				"member_uid", memberUID,
			)
		}
	}
}

// publishMemberMessages publishes indexer and access control messages for the new member
func (uc *committeeWriterOrchestrator) publishMemberMessages(ctx context.Context, committeeUID string, member *model.CommitteeMember) error {
	slog.DebugContext(ctx, "publishing member messages",
		"committee_uid", committeeUID,
		"member_uid", member.UID,
	)

	// Build indexer message for the member
	indexerMessage := model.CommitteeIndexerMessage{
		Action: model.ActionCreated,
		Tags:   member.Tags(committeeUID),
	}

	message, errBuildIndexerMessage := indexerMessage.Build(ctx, member)
	if errBuildIndexerMessage != nil {
		slog.ErrorContext(ctx, "failed to build member indexer message",
			"error", errBuildIndexerMessage,
			"committee_uid", committeeUID,
			"member_uid", member.UID,
		)
		return errs.NewUnexpected("failed to build member indexer message", errBuildIndexerMessage)
	}

	// Publish messages concurrently
	messages := []func() error{
		func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeMemberSubject, message)
		},
		// TODO: Add access control message if needed for members
		// func() error {
		//     return uc.committeePublisher.Access(ctx, constants.UpdateAccessCommitteeMemberSubject, accessMessage)
		// },
	}

	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish member messages",
			"error", errPublishingMessage,
			"committee_uid", committeeUID,
			"member_uid", member.UID,
		)
		return errPublishingMessage
	}

	return nil
}
