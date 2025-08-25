// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"fmt"
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
		// Member keys should use member-specific methods
		rev, errGet := uc.committeeReader.GetMemberRevision(ctx, key)
		if errGet != nil {
			slog.ErrorContext(ctx, "failed to get revision for member key deletion",
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
	member.CommitteeName = committee.Name

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
	if errPublish := uc.publishMemberMessages(ctx, model.ActionCreated, member); errPublish != nil {
		// Log the error but don't fail the member creation
		slog.WarnContext(ctx, "failed to publish member messages",
			"error", errPublish,
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
	}

	return member, nil
}

// UpdateMember updates an existing committee member
func (uc *committeeWriterOrchestrator) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "executing update committee member use case",
		"member_uid", member.UID,
		"committee_uid", member.CommitteeUID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
		"revision", revision,
	)

	// For rollback purposes and cleanup
	var (
		staleKeys        []string
		newKeys          []string
		rollbackRequired bool
		updateSucceeded  bool
	)
	defer func() {
		if err := recover(); err != nil || rollbackRequired {
			// Rollback new keys
			uc.deleteMemberKeys(ctx, newKeys, true)
		}
		if updateSucceeded && len(staleKeys) > 0 {
			slog.DebugContext(ctx, "cleaning up stale member keys",
				"keys_count", len(staleKeys),
			)
			go func() {
				// Cleanup stale keys in a separate goroutine
				// new context to avoid blocking the main flow
				ctxCleanup, cancel := context.WithTimeout(context.Background(), time.Second*10)
				defer cancel()
				uc.deleteMemberKeys(ctxCleanup, staleKeys, false)
			}()
		}
	}()

	// Step 1: Retrieve existing member data from the repository
	existing, existingRevision, errGet := uc.committeeReader.GetMember(ctx, member.UID)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to retrieve existing committee member",
			"error", errGet,
			"member_uid", member.UID,
		)
		return nil, errGet
	}

	// Verify revision matches to ensure optimistic locking
	// We will check again during the update process, but this is for fail-fast
	if existingRevision != revision {
		slog.WarnContext(ctx, "revision mismatch during member update",
			"expected_revision", revision,
			"current_revision", existingRevision,
			"member_uid", member.UID,
		)
		return nil, errs.NewConflict("committee member has been modified by another process")
	}

	// Verify that the member belongs to the requested committee
	if existing.CommitteeUID != member.CommitteeUID {
		slog.ErrorContext(ctx, "committee member does not belong to the requested committee",
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
			"member_committee_uid", existing.CommitteeUID,
		)
		return nil, errs.NewValidation("committee member does not belong to the requested committee")
	}

	slog.DebugContext(ctx, "existing committee member retrieved",
		"member_uid", existing.UID,
		"existing_email", redaction.RedactEmail(existing.Email),
		"existing_username", redaction.Redact(existing.Username),
		"existing_organization", existing.Organization.Name,
		"committee_uid", existing.CommitteeUID,
	)

	// Step 2: Validate that the committee exists and get settings
	committee, committeeRevision, errCommittee := uc.committeeReader.GetBase(ctx, member.CommitteeUID)
	if errCommittee != nil {
		slog.ErrorContext(ctx, "committee not found during member update",
			"error", errCommittee,
			"committee_uid", member.CommitteeUID,
		)
		return nil, errCommittee
	}
	member.CommitteeName = committee.Name

	slog.DebugContext(ctx, "committee found for member update",
		"committee_uid", committee.UID,
		"committee_name", committee.Name,
		"committee_category", committee.Category,
		"revision", committeeRevision,
	)

	// Step 3: Validate member against committee requirements (domain validation)
	// We use empty settings for basic validation since we only need settings for email validation
	basicSettings := &model.CommitteeSettings{}
	fullCommittee := &model.Committee{CommitteeBase: *committee, CommitteeSettings: basicSettings}
	if errValidation := member.Validate(fullCommittee); errValidation != nil {
		slog.ErrorContext(ctx, "committee member validation failed during update",
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

	// Step 4: Handle email changes - validate corporate domain and manage lookup keys
	emailChanged := existing.Email != member.Email
	if emailChanged {
		slog.DebugContext(ctx, "email change detected",
			"old_email", redaction.RedactEmail(existing.Email),
			"new_email", redaction.RedactEmail(member.Email),
		)

		// Get committee settings to check business email requirements (only when email changes)
		var settings *model.CommitteeSettings
		settings, _, errSettings := uc.committeeReader.GetSettings(ctx, member.CommitteeUID)
		if errSettings != nil {
			var notFoundErr errs.NotFound
			if !errors.As(errSettings, &notFoundErr) {
				slog.ErrorContext(ctx, "failed to retrieve committee settings for email validation",
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

		slog.DebugContext(ctx, "committee settings retrieved for email validation",
			"committee_uid", member.CommitteeUID,
			"business_email_required", settings.BusinessEmailRequired,
		)

		// Validate business email domain if required
		if settings.BusinessEmailRequired {
			if errEmailValidation := uc.validateCorporateEmailDomain(ctx, member.Email); errEmailValidation != nil {
				slog.WarnContext(ctx, "corporate email domain validation failed during update",
					"error", errEmailValidation,
					"email", redaction.RedactEmail(member.Email),
					"committee_uid", member.CommitteeUID,
				)
				return nil, errEmailValidation
			}
		}

		// Check if new email already exists in committee (uniqueness check)
		newLookupKey, errMemberExists := uc.committeeWriter.UniqueMember(ctx, member)
		if errMemberExists != nil {
			slog.WarnContext(ctx, "member with new email already exists in committee",
				"error", errMemberExists,
				"committee_uid", member.CommitteeUID,
				"new_email", redaction.RedactEmail(member.Email),
			)
			return nil, errMemberExists
		}
		newKeys = append(newKeys, newLookupKey)

		// Mark old lookup key for cleanup
		oldLookupKey := fmt.Sprintf(constants.KVLookupMemberPrefix, existing.BuildIndexKey(ctx))
		staleKeys = append(staleKeys, oldLookupKey)
	}

	// Step 5: Handle username changes - validate username exists
	usernameChanged := existing.Username != member.Username
	if usernameChanged {
		slog.DebugContext(ctx, "username change detected",
			"old_username", redaction.Redact(existing.Username),
			"new_username", redaction.Redact(member.Username),
		)

		if errUsername := uc.validateUsernameExists(ctx, member.Username); errUsername != nil {
			slog.ErrorContext(ctx, "username validation failed during update",
				"error", errUsername,
				"username", redaction.Redact(member.Username),
			)
			rollbackRequired = true
			return nil, errUsername
		}
	}

	// Step 6: Handle organization changes - validate organization exists
	organizationChanged := existing.Organization.Name != member.Organization.Name
	if organizationChanged {
		slog.DebugContext(ctx, "organization change detected",
			"old_organization", existing.Organization.Name,
			"new_organization", member.Organization.Name,
		)

		if errOrganization := uc.validateOrganizationExists(ctx, member.Organization.Name); errOrganization != nil {
			slog.ErrorContext(ctx, "organization validation failed during update",
				"error", errOrganization,
				"organization", member.Organization.Name,
			)
			rollbackRequired = true
			return nil, errOrganization
		}
	}

	// Step 7: Merge existing data with updated fields
	// Preserve immutable fields
	member.UID = existing.UID
	member.CreatedAt = existing.CreatedAt
	member.UpdatedAt = time.Now()

	slog.DebugContext(ctx, "merging existing member data with updates",
		"member_uid", member.UID,
		"email_changed", emailChanged,
		"username_changed", usernameChanged,
		"organization_changed", organizationChanged,
	)

	// Step 8: Update the member in storage
	updatedMember, errUpdate := uc.committeeWriter.UpdateMember(ctx, member, revision)
	if errUpdate != nil {
		slog.ErrorContext(ctx, "failed to update committee member",
			"error", errUpdate,
			"member_uid", member.UID,
		)
		rollbackRequired = true
		return nil, errUpdate
	}

	// Use the returned member from storage (which may have been modified)
	member = updatedMember

	slog.DebugContext(ctx, "committee member updated successfully",
		"member_uid", member.UID,
		"committee_uid", member.CommitteeUID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
	)

	// Step 9: Add organization user engagement if organization changed
	if organizationChanged {
		if errEngagement := uc.addOrganizationUserEngagement(ctx, member.Organization.Name, member.Username); errEngagement != nil {
			// Log the error but don't fail the member update
			slog.WarnContext(ctx, "failed to add organization user engagement during update",
				"error", errEngagement,
				"organization", member.Organization.Name,
				"username", redaction.Redact(member.Username),
				"committee_uid", member.CommitteeUID,
				"member_uid", member.UID,
			)
		}
	}

	// Step 10: Publish indexer messages
	if errPublish := uc.publishMemberMessages(ctx, model.ActionUpdated, member); errPublish != nil {
		// Log the error but don't fail the member update
		slog.WarnContext(ctx, "failed to publish member update messages",
			"error", errPublish,
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
	}

	slog.DebugContext(ctx, "committee member update completed successfully",
		"member_uid", member.UID,
		"stale_keys_count", len(staleKeys),
	)

	// Mark update as successful for defer cleanup
	updateSucceeded = true
	return member, nil
}

// DeleteMember removes a committee member
func (uc *committeeWriterOrchestrator) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	slog.DebugContext(ctx, "executing delete committee member use case",
		"member_uid", uid,
		"revision", revision,
	)

	// Step 1: Retrieve existing member data to get all the information needed for cleanup
	existing, existingRevision, errGet := uc.committeeReader.GetMember(ctx, uid)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to retrieve existing committee member for deletion",
			"error", errGet,
			"member_uid", uid,
		)
		return errGet
	}

	// Verify revision matches to ensure optimistic locking
	if existingRevision != revision {
		slog.WarnContext(ctx, "revision mismatch during member deletion",
			"expected_revision", revision,
			"current_revision", existingRevision,
			"member_uid", uid,
		)
		return errs.NewConflict("committee member has been modified by another process")
	}

	slog.DebugContext(ctx, "existing committee member retrieved for deletion",
		"member_uid", existing.UID,
		"member_email", redaction.RedactEmail(existing.Email),
		"member_username", redaction.Redact(existing.Username),
		"committee_uid", existing.CommitteeUID,
	)

	// Step 2: Build list of secondary indices to delete
	var indicesToDelete []string

	// Build member lookup index key (committee_uid + email hash)
	memberIndexKey := fmt.Sprintf(constants.KVLookupMemberPrefix, existing.BuildIndexKey(ctx))
	indicesToDelete = append(indicesToDelete, memberIndexKey)

	slog.DebugContext(ctx, "secondary indices identified for member deletion",
		"member_uid", uid,
		"indices_count", len(indicesToDelete),
		"indices", indicesToDelete,
	)

	// Step 3: Delete the main member record
	errDelete := uc.committeeWriter.DeleteMember(ctx, uid, revision)
	if errDelete != nil {
		slog.ErrorContext(ctx, "failed to delete committee member",
			"error", errDelete,
			"member_uid", uid,
		)
		return errDelete
	}

	slog.DebugContext(ctx, "committee member main record deleted successfully",
		"member_uid", uid,
	)

	// Step 4: Delete secondary indices
	// We use the deleteMemberKeys method which handles errors gracefully and logs them
	// We don't abort here - secondary indices have a minor impact during deletion
	uc.deleteMemberKeys(ctx, indicesToDelete, false)

	// Step 5: Publish indexer message for member deletion
	if errPublish := uc.publishMemberMessages(ctx, model.ActionDeleted, uid); errPublish != nil {
		slog.ErrorContext(ctx, "failed to publish member deletion message",
			"error", errPublish,
			"member_uid", uid,
		)
		return errPublish
	}

	slog.DebugContext(ctx, "committee member deletion completed successfully",
		"member_uid", uid,
		"indices_deleted", len(indicesToDelete),
	)

	return nil
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

// publishMemberMessages publishes indexer and access control messages for committee member operations
func (uc *committeeWriterOrchestrator) publishMemberMessages(ctx context.Context, action model.MessageAction, data any) error {
	slog.DebugContext(ctx, "publishing member messages",
		"action", action,
	)

	// Build indexer message for the member
	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
	}

	// Add tags for create/update operations (when we have the full member data)
	if member, ok := data.(*model.CommitteeMember); ok {
		indexerMessage.Tags = member.Tags()
	}

	message, errBuildIndexerMessage := indexerMessage.Build(ctx, data)
	if errBuildIndexerMessage != nil {
		slog.ErrorContext(ctx, "failed to build member indexer message",
			"error", errBuildIndexerMessage,
			"action", action,
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
			"action", action,
		)
		return errPublishingMessage
	}

	return nil
}
