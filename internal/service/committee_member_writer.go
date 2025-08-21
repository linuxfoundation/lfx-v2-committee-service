// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"log/slog"
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

func (uc *committeeWriterOrchestrator) deleteMemberKeys(ctx context.Context, keys []string, isRollback bool) {

	if len(keys) == 0 {
		return
	}

	slog.DebugContext(ctx, "deleting member keys",
		"keys", keys,
		"is_rollback", isRollback,
	)

	for _, key := range keys {
		rev, errGet := uc.committeeReader.GetMemberRevision(ctx, key)
		if errGet != nil {
			slog.ErrorContext(ctx, "failed to get member revision",
				"error", errGet,
				"key", key,
				"is_rollback", isRollback,
				// This is critical because if we don't delete them,
				// the member would be locked for reuse for a long time.
				log.PriorityCritical(),
			)
			continue
		}

		errDelete := uc.committeeWriter.DeleteMember(ctx, key, rev)
		if errDelete != nil {
			slog.ErrorContext(ctx, "failed to delete member key",
				"error", errDelete,
				"key", key,
				"is_rollback", isRollback,
				// This is critical because if we don't delete them,
				// the member would be locked for reuse for a long time.
				log.PriorityCritical(),
			)
		}
		slog.DebugContext(ctx, "deleted member key",
			"key", key,
			"is_rollback", isRollback,
		)
	}
}

// CreateMember creates a new committee member includes validation and rollback support
func (uc *committeeWriterOrchestrator) CreateMember(ctx context.Context, member *model.CommitteeMember) (*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "creating committee member",
		"committee_uid", member.CommitteeUID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
	)

	now := time.Now()
	member.UID = uuid.New().String()
	member.CreatedAt = now
	member.UpdatedAt = now

	// Track resources for rollback purposes
	var (
		keys             []string
		rollbackRequired bool
	)
	defer func() {
		if err := recover(); err != nil || rollbackRequired {
			uc.deleteMemberKeys(ctx, keys, rollbackRequired)
		}
	}()

	// Step 1: Validate that the committee exists
	committee, committeeRevision, errCommittee := uc.committeeReader.GetBase(ctx, member.CommitteeUID)
	if errCommittee != nil {
		slog.ErrorContext(ctx, "committee not found",
			"error", errCommittee,
			"committee_uid", member.CommitteeUID,
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
	settings, _, errSettings := uc.committeeReader.GetSettings(ctx, member.CommitteeUID)
	if errSettings != nil {
		var notFoundErr errs.NotFound
		if !errors.As(errSettings, &notFoundErr) {
			slog.ErrorContext(ctx, "failed to retrieve committee settings",
				"error", errSettings,
				"committee_uid", member.CommitteeUID,
			)
			return nil, errSettings
		}
	}
	// Use empty settings if not found
	if settings == nil {
		settings = &model.CommitteeSettings{}
	}

	slog.DebugContext(ctx, "committee settings retrieved",
		"committee_uid", member.CommitteeUID,
		"business_email_required", settings.BusinessEmailRequired,
	)

	// Step 2: Validate member against committee requirements (domain validation)
	fullCommittee := &model.Committee{CommitteeBase: *committee, CommitteeSettings: settings}
	if errValidation := member.Validate(fullCommittee); errValidation != nil {
		slog.ErrorContext(ctx, "committee member validation failed",
			"error", errValidation,
			"member_uid", member.UID,
			"committee_uid", member.CommitteeUID,
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
				"committee_uid", member.CommitteeUID,
			)
			return nil, errEmailValidation
		}
	}

	// Step 4: Validate username exists
	if errUsername := uc.validateUsernameExists(ctx, member.Username); errUsername != nil {
		slog.ErrorContext(ctx, "username validation failed",
			"error", errUsername,
			"username", redaction.Redact(member.Username),
		)
		return nil, errUsername
	}

	// Step 5: Validate organization exists (external service call)
	if errOrganization := uc.validateOrganizationExists(ctx, member.Organization.Name); errOrganization != nil {
		slog.ErrorContext(ctx, "organization validation failed",
			"error", errOrganization,
			"organization", member.Organization.Name,
		)
		return nil, errOrganization
	}

	// Step 6: Check if member already exists in committee
	key, errMemberExists := uc.committeeWriter.UniqueMember(ctx, member)
	if errMemberExists != nil {
		slog.WarnContext(ctx, "member already exists in committee",
			"error", errMemberExists,
			"committee_uid", member.CommitteeUID,
			"member_email", redaction.RedactEmail(member.Email),
		)
		return nil, errMemberExists
	}
	keys = append(keys, key)

	// Step 7: Create the member record with rollback support
	errCreate := uc.committeeWriter.CreateMember(ctx, member)
	if errCreate != nil {
		slog.ErrorContext(ctx, "failed to create committee member",
			"error", errCreate,
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
		rollbackRequired = true
		return nil, errCreate
	}
	keys = append(keys, member.UID)

	slog.DebugContext(ctx, "committee member created successfully",
		"committee_uid", member.CommitteeUID,
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
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
	}

	// Step 9: Publish indexer and access control messages
	if errPublish := uc.publishMemberMessages(ctx, member.CommitteeUID, member); errPublish != nil {
		// Log the error but don't fail the member creation
		slog.WarnContext(ctx, "failed to publish member messages",
			"error", errPublish,
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
	}

	return member, nil
}

// UpdateMember updates an existing committee member (placeholder implementation)
func (uc *committeeWriterOrchestrator) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	return nil, errs.NewUnexpected("committee member update not yet implemented")
}

// DeleteMember removes a committee member (placeholder implementation)
func (uc *committeeWriterOrchestrator) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	// TODO: Implement committee member deletion logic
	slog.DebugContext(ctx, "deleting committee member (placeholder)",
		"member_uid", uid,
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

	// TODO: https://linuxfoundation.atlassian.net/browse/LFXV2-328
	// Implement actual corporate email domain validation logic
	// This could involve calling LFX user service /v1/users/public-email

	return nil
}

// validateUsernameExists validates if the username exists in external systems
// TODO: Implement actual external service integration
func (uc *committeeWriterOrchestrator) validateUsernameExists(ctx context.Context, username string) error {
	slog.DebugContext(ctx, "validating username exists (placeholder)",
		"username", redaction.Redact(username),
	)

	// TODO: https://linuxfoundation.atlassian.net/browse/LFXV2-329
	// Implement actual username validation against external services
	// This could involve calling LFX user service or similar
	// For now, we'll just validate that username is not empty

	return nil
}

// validateOrganizationExists validates if the organization exists in external systems
// TODO: Implement actual external service integration
func (uc *committeeWriterOrchestrator) validateOrganizationExists(ctx context.Context, organizationName string) error {
	slog.DebugContext(ctx, "validating organization exists (placeholder)",
		"organization", redaction.Redact(organizationName),
	)

	// TODO: https://linuxfoundation.atlassian.net/browse/LFXV2-330
	// Implement actual organization validation against external services
	// This could involve calling LFX organization service or similar
	// For now, we'll just validate that organization name is not empty

	return nil
}

// addOrganizationUserEngagement adds user engagement to organization
// TODO: Implement actual external API integration
func (uc *committeeWriterOrchestrator) addOrganizationUserEngagement(ctx context.Context, organizationName, username string) error {
	slog.DebugContext(ctx, "adding organization user engagement (placeholder)",
		"organization", redaction.Redact(organizationName),
		"username", redaction.Redact(username),
	)

	// TODO: https://linuxfoundation.atlassian.net/browse/LFXV2-331 - Implement actual external API call
	// Example: POST /orgs/{org}/users/{username}/engagements
	// This should add the user engagement record to track committee participation

	return nil
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
		Tags:   member.Tags(),
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
		// TODO: https://linuxfoundation.atlassian.net/browse/LFXV2-332
		// Evaluate if we need to publish access control messages for members
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
