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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
	fgaconstants "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/constants"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
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
			var notFoundErr errs.NotFound
			if errors.As(errGet, &notFoundErr) {
				// Key already absent — nothing to delete. This is expected for
				// the committee→member index on members created before the backfill
				// was run, and for any key that was never written (e.g. failed partial write).
				slog.DebugContext(ctx, "member key already absent during cleanup",
					"key", key,
					"is_rollback", isRollback,
				)
				continue
			}
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
func (uc *committeeWriterOrchestrator) CreateMember(ctx context.Context, member *model.CommitteeMember, sync bool, skipEnrichment bool) (*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "creating committee member",
		"committee_uid", member.CommitteeUID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
		"sync", sync,
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
	member.CommitteeCategory = committee.Category
	member.ProjectUID = committee.ProjectUID
	member.ProjectSlug = committee.ProjectSlug

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

	// Step 4: Resolve username from email, overriding any caller-supplied plain LFID.
	// Clear first so a failed lookup never leaves an unverified value at rest.
	if member.Email != "" && !skipEnrichment {
		member.Username = ""
		slog.DebugContext(ctx, "resolving username from email",
			"email", redaction.RedactEmail(member.Email),
		)
		username, errLookup := uc.lookupUsernameByEmail(ctx, member.Email)
		if errLookup != nil {
			slog.WarnContext(ctx, "failed to lookup username by email",
				"error", errLookup,
				"email", redaction.RedactEmail(member.Email),
			)
			// Continue without username - it's an optional field
		} else if username != "" {
			member.Username = username
			slog.DebugContext(ctx, "username resolved from email",
				"email", redaction.RedactEmail(member.Email),
				"username", redaction.Redact(member.Username),
			)
		}
	}

	// Step 5: Validate username exists
	if errUsername := uc.validateUsernameExists(ctx, member.Username); errUsername != nil {
		slog.ErrorContext(ctx, "username validation failed",
			"error", errUsername,
			"username", redaction.Redact(member.Username),
		)
		return nil, errUsername
	}

	// Step 6: Validate organization exists (external service call)
	if errOrganization := uc.validateOrganizationExists(ctx, member.Organization.Name); errOrganization != nil {
		slog.ErrorContext(ctx, "organization validation failed",
			"error", errOrganization,
			"organization", member.Organization.Name,
		)
		return nil, errOrganization
	}

	// Step 7: Check if member already exists in committee
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

	// Step 8: Create the member record with rollback support
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

	// Step 8b: Write the committee→member secondary index so ListMembersByCommittee can use a
	// targeted prefix scan rather than a full bucket scan.
	indexKey, errIndex := uc.committeeWriter.IndexMemberByCommittee(ctx, member)
	// Append before checking the error: if the write partially succeeded and
	// returned an error, rollback must still be able to clean up the written key.
	if indexKey != "" {
		keys = append(keys, indexKey)
	}
	if errIndex != nil {
		slog.ErrorContext(ctx, "failed to write committee member index",
			"error", errIndex,
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
		rollbackRequired = true
		return nil, errIndex
	}

	// Step 8c: Write the organization→member secondary index (Org Lens, LFXV2-1865) so a company's
	// committee seats can be listed from committee-service's own KV without a query-service call.
	// No-op (empty key) for members with no organization.id.
	orgIndexKey, errOrgIndex := uc.committeeWriter.IndexMemberByOrganization(ctx, member)
	if orgIndexKey != "" {
		keys = append(keys, orgIndexKey)
	}
	if errOrgIndex != nil {
		slog.ErrorContext(ctx, "failed to write committee member organization index",
			"error", errOrgIndex,
			"organization_id", member.Organization.ID,
			"member_uid", member.UID,
		)
		rollbackRequired = true
		return nil, errOrgIndex
	}

	// Step 8d: Write the email→member secondary index so v1-sync-helper can find all committee seats
	// for a given email via a server-side filtered scan rather than a full bucket scan.
	// No-op (empty key) for members with no email.
	emailIndexKey, errEmailIndex := uc.committeeWriter.IndexMemberByEmail(ctx, member)
	if emailIndexKey != "" {
		keys = append(keys, emailIndexKey)
	}
	if errEmailIndex != nil {
		slog.ErrorContext(ctx, "failed to write committee member email index",
			"error", errEmailIndex,
			"member_uid", member.UID,
		)
		rollbackRequired = true
		return nil, errEmailIndex
	}

	slog.DebugContext(ctx, "committee member created successfully",
		"committee_uid", member.CommitteeUID,
		"member_uid", member.UID,
		"member_email", redaction.RedactEmail(member.Email),
		"member_username", redaction.Redact(member.Username),
	)

	// Step 9: Add organization user engagement
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

	// Step 10: Publish indexer and access control messages
	eventData := &model.CommitteeMemberMessageData{
		Member:           member,
		SkipNotification: member.SkipNotification,
	}
	if errPublish := uc.publishMemberMessages(ctx, model.ActionCreated, eventData, sync); errPublish != nil {
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
func (uc *committeeWriterOrchestrator) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64, sync bool, skipEnrichment bool) (*model.CommitteeMember, error) {
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
	member.CommitteeCategory = committee.Category
	member.ProjectUID = committee.ProjectUID
	member.ProjectSlug = committee.ProjectSlug

	slog.DebugContext(ctx, "committee found for member update",
		"committee_uid", committee.UID,
		"committee_name", committee.Name,
		"committee_category", committee.Category,
		"revision", committeeRevision,
	)

	// Step 3: Fetch committee settings and validate member against committee requirements
	var settings *model.CommitteeSettings
	settings, _, errSettings := uc.committeeReader.GetSettings(ctx, member.CommitteeUID)
	if errSettings != nil {
		var notFoundErr errs.NotFound
		if !errors.As(errSettings, &notFoundErr) {
			slog.ErrorContext(ctx, "failed to retrieve committee settings during member update",
				"error", errSettings,
				"committee_uid", member.CommitteeUID,
			)
			return nil, errSettings
		}
	}
	if settings == nil {
		settings = &model.CommitteeSettings{}
	}

	slog.DebugContext(ctx, "committee settings retrieved for member update",
		"committee_uid", member.CommitteeUID,
		"business_email_required", settings.BusinessEmailRequired,
	)

	fullCommittee := &model.Committee{CommitteeBase: *committee, CommitteeSettings: settings}
	if errValidation := member.ValidateUpdate(fullCommittee, existing); errValidation != nil {
		slog.ErrorContext(ctx, "committee member validation failed during update",
			"error", errValidation,
			"member_uid", member.UID,
			"committee_uid", member.CommitteeUID,
			"committee_category", committee.Category,
			"member_email", redaction.RedactEmail(member.Email),
			"member_username", redaction.Redact(member.Username),
		)
		return nil, errValidation
	}

	// Step 4: Handle email changes - validate corporate domain and manage lookup keys
	oldEmailHash := existing.BuildEmailIndexKey(ctx)
	newEmailHash := member.BuildEmailIndexKey(ctx)
	emailChanged := oldEmailHash != newEmailHash
	if emailChanged {
		slog.DebugContext(ctx, "email change detected",
			"old_email", redaction.RedactEmail(existing.Email),
			"new_email", redaction.RedactEmail(member.Email),
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

		// Check if new email already exists in committee (uniqueness check).
		// emailChanged is hash-based so case-only changes never reach this block,
		// preventing write+stale-delete of identical uniqueness keys.
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

		// Mark old uniqueness and email-index keys for cleanup.
		staleKeys = append(staleKeys, fmt.Sprintf(constants.KVLookupMemberPrefix, existing.BuildIndexKey(ctx)))

		newEmailIndexKey, errEmailIndex := uc.committeeWriter.IndexMemberByEmail(ctx, member)
		if newEmailIndexKey != "" {
			newKeys = append(newKeys, newEmailIndexKey)
		}
		if errEmailIndex != nil {
			slog.ErrorContext(ctx, "failed to write email index during member update",
				"error", errEmailIndex,
				"member_uid", member.UID,
			)
			rollbackRequired = true
			return nil, errEmailIndex
		}
		if oldEmailHash != "" {
			staleKeys = append(staleKeys, fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, oldEmailHash, existing.UID))
		}
	}

	// Resolve username from email when auth can map the email to an LFID.
	// When lookup fails or returns empty, keep the stored username only if the email did not change.
	// If the email changed, clear username to avoid persisting a username/email mismatch.
	// Invite acceptance and X-Skip-Enrichment skip this block and persist caller-supplied identity.
	if member.Email != "" && !skipEnrichment {
		slog.DebugContext(ctx, "resolving username from email during update",
			"email", redaction.RedactEmail(member.Email),
			"stored_username", redaction.Redact(existing.Username),
		)
		username, errLookup := uc.lookupUsernameByEmail(ctx, member.Email)
		switch {
		case errLookup != nil:
			if emailChanged {
				slog.WarnContext(ctx, "failed to lookup username by email during update; clearing username after email change",
					"error", errLookup,
					"email", redaction.RedactEmail(member.Email),
					"stored_username", redaction.Redact(existing.Username),
				)
				member.Username = ""
			} else {
				slog.WarnContext(ctx, "failed to lookup username by email during update; keeping stored username",
					"error", errLookup,
					"email", redaction.RedactEmail(member.Email),
					"stored_username", redaction.Redact(existing.Username),
				)
				member.Username = existing.Username
			}
		case username != "":
			member.Username = username
			slog.DebugContext(ctx, "username resolved from email during update",
				"email", redaction.RedactEmail(member.Email),
				"username", redaction.Redact(member.Username),
			)
		default:
			if emailChanged {
				slog.WarnContext(ctx, "username lookup returned empty during update; clearing username after email change",
					"email", redaction.RedactEmail(member.Email),
					"stored_username", redaction.Redact(existing.Username),
				)
				member.Username = ""
			} else {
				member.Username = existing.Username
			}
		}
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

	// Step 7b: Reconcile the organization→member secondary index (Org Lens, LFXV2-1865) when the
	// holding org id changes. The committee→member index is immutable on update (committee_uid can't
	// change), but organization.id can — so write the new entry (tracked in newKeys for rollback if
	// the update fails) and mark the old entry stale for cleanup after a successful update.
	oldOrgSFID := utils.NormalizeAccountSFID(existing.Organization.ID)
	newOrgSFID := utils.NormalizeAccountSFID(member.Organization.ID)
	if oldOrgSFID != newOrgSFID {
		if newOrgSFID != "" {
			newOrgIndexKey, errOrgIndex := uc.committeeWriter.IndexMemberByOrganization(ctx, member)
			if newOrgIndexKey != "" {
				newKeys = append(newKeys, newOrgIndexKey)
			}
			if errOrgIndex != nil {
				slog.ErrorContext(ctx, "failed to write organization index during member update",
					"error", errOrgIndex,
					"organization_id", member.Organization.ID,
					"member_uid", member.UID,
				)
				rollbackRequired = true
				return nil, errOrgIndex
			}
		}
		if oldOrgSFID != "" {
			staleKeys = append(staleKeys, fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, oldOrgSFID, existing.UID))
		}
	}

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
	updateEventData := &model.CommitteeMemberMessageData{
		Member:    member,
		OldMember: existing,
	}
	if errPublish := uc.publishMemberMessages(ctx, model.ActionUpdated, updateEventData, sync); errPublish != nil {
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
func (uc *committeeWriterOrchestrator) DeleteMember(ctx context.Context, uid string, revision uint64, sync bool, skipNotification bool) error {
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

	// Build member lookup index key (committee_uid + email hash) for uniqueness guard.
	memberIndexKey := fmt.Sprintf(constants.KVLookupMemberPrefix, existing.BuildIndexKey(ctx))
	indicesToDelete = append(indicesToDelete, memberIndexKey)

	// Build committee→member secondary index key so it is cleaned up on delete.
	membersByCommitteeKey := fmt.Sprintf(constants.KVLookupMembersByCommitteePrefix, existing.CommitteeUID, existing.UID)
	indicesToDelete = append(indicesToDelete, membersByCommitteeKey)

	// Build organization→member secondary index key (Org Lens, LFXV2-1865) so it is cleaned up on
	// delete. Only present when the member had an organization.id; normalized to match the write side.
	if orgSFID := utils.NormalizeAccountSFID(existing.Organization.ID); orgSFID != "" {
		indicesToDelete = append(indicesToDelete, fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, orgSFID, existing.UID))
	}

	// Build email→member secondary index key so it is cleaned up on delete.
	if emailHash := existing.BuildEmailIndexKey(ctx); emailHash != "" {
		indicesToDelete = append(indicesToDelete, fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, emailHash, existing.UID))
	}

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
	deleteEventData := &model.CommitteeMemberMessageData{
		Member:           existing,
		SkipNotification: skipNotification,
	}
	if errPublish := uc.publishMemberMessages(ctx, model.ActionDeleted, deleteEventData, sync); errPublish != nil {
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

// ReassignMember atomically replaces the holder of an existing seat for the Org Lens reassign flow
// (LFXV2-1865): it creates newMember (which runs the full create pipeline — validation, secondary
// indices by committee + organization, FGA, indexer publish) and then deletes the old member at
// oldRevision. NATS KV has no multi-key transaction, so atomicity is approximated with a rollback
// pattern keyed on the old seat's confirmed state after a failed delete:
//   - old seat already gone (NotFound) → the delete effectively succeeded; the new seat is retained.
//   - old seat still present → the new seat is rolled back (deleted) so no duplicate remains; if the
//     rollback ALSO fails, an error is returned so the caller knows manual recovery is required.
//   - old seat state unconfirmed (the confirming read itself failed) → the new seat is retained and an
//     error is returned, because rolling back when the delete may have committed would leave the seat
//     with no holder at all (silent data loss). A visible duplicate is preferable and reconcilable.
//
// Returns the created member on success.
func (uc *committeeWriterOrchestrator) ReassignMember(ctx context.Context, oldMemberUID string, oldRevision uint64, newMember *model.CommitteeMember, sync bool) (*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "executing reassign committee member use case",
		"old_member_uid", oldMemberUID,
		"committee_uid", newMember.CommitteeUID,
		"new_member_email", redaction.RedactEmail(newMember.Email),
	)

	// Create the replacement holder first. CreateMember owns its own internal rollback for partial
	// create failures, so on error nothing is left behind and the old seat is untouched.
	created, errCreate := uc.CreateMember(ctx, newMember, sync, false)
	if errCreate != nil {
		return nil, errCreate
	}

	// Delete the old holder. On success the reassign is complete.
	if errDelete := uc.DeleteMember(ctx, oldMemberUID, oldRevision, sync, false); errDelete != nil {
		// DeleteMember can fail after the old seat is already gone (e.g. a post-commit indexer publish
		// failed). Re-read the old seat to decide what to do: we only roll back the new seat when we can
		// POSITIVELY confirm the old holder still exists. If the re-read itself fails we cannot tell
		// whether the delete committed, so rolling back would risk leaving the seat with no holder at
		// all — we keep the new seat and surface an error for manual reconciliation instead.
		_, _, errGetOld := uc.committeeReader.GetMember(ctx, oldMemberUID)
		if errGetOld != nil {
			var notFound errs.NotFound
			if errors.As(errGetOld, &notFound) {
				// Old seat is already gone → the reassign effectively succeeded; keep the new seat.
				slog.WarnContext(ctx, "reassign delete reported failure but old seat is already gone; new seat retained",
					"committee_uid", newMember.CommitteeUID,
					"old_member_uid", oldMemberUID,
					"new_member_uid", created.UID,
					"error", errDelete,
				)
				return created, nil
			}
			// Old seat state is unconfirmed (transient read failure). Do NOT roll back: if the delete
			// actually committed, a rollback would lose the seat's holder entirely. Retain the new seat
			// and return an error so the at-worst duplicate can be reconciled manually.
			slog.ErrorContext(ctx, "reassign delete failed and old seat state is unconfirmed; new seat retained for manual reconciliation",
				"committee_uid", newMember.CommitteeUID,
				"old_member_uid", oldMemberUID,
				"new_member_uid", created.UID,
				"delete_error", errDelete,
				"read_error", errGetOld,
			)
			return nil, errs.NewUnexpected("reassign delete failed and the old seat could not be confirmed; new seat retained for manual reconciliation", errDelete, errGetOld)
		}

		// Old holder positively still exists — roll back the newly-created seat so no duplicate remains.
		var errRollback error
		if created != nil && created.UID != "" {
			if _, createdRev, errGet := uc.committeeReader.GetMember(ctx, created.UID); errGet == nil {
				errRollback = uc.DeleteMember(ctx, created.UID, createdRev, sync, false)
			} else {
				errRollback = errGet
			}
		}
		if errRollback != nil {
			slog.ErrorContext(ctx, "reassign rollback failed; duplicate committee seat may remain",
				"committee_uid", newMember.CommitteeUID,
				"old_member_uid", oldMemberUID,
				"new_member_uid", created.UID,
				"delete_error", errDelete,
				"rollback_error", errRollback,
			)
			return nil, errs.NewUnexpected("reassign failed and rollback of the new seat also failed; manual recovery required", errDelete, errRollback)
		}
		slog.WarnContext(ctx, "reassign delete failed; rolled back the new seat",
			"committee_uid", newMember.CommitteeUID,
			"old_member_uid", oldMemberUID,
			"new_member_uid", created.UID,
			"error", errDelete,
		)
		return nil, errDelete
	}

	slog.DebugContext(ctx, "committee member reassigned successfully",
		"old_member_uid", oldMemberUID,
		"new_member_uid", created.UID,
		"committee_uid", newMember.CommitteeUID,
	)
	return created, nil
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

// lookupUsernameByEmail looks up a user's LFID username by their email address.
func (uc *committeeWriterOrchestrator) lookupUsernameByEmail(ctx context.Context, email string) (string, error) {
	if uc.userReader == nil {
		slog.DebugContext(ctx, "user reader not configured, skipping username lookup",
			"email", redaction.RedactEmail(email),
		)
		return "", nil
	}

	slog.DebugContext(ctx, "looking up username by email",
		"email", redaction.RedactEmail(email),
	)

	username, err := uc.userReader.UsernameByEmail(ctx, email)
	if err != nil {
		return "", err
	}

	if username == "" {
		slog.DebugContext(ctx, "username lookup returned empty",
			"email", redaction.RedactEmail(email),
		)
		return "", nil
	}

	slog.DebugContext(ctx, "successfully looked up username by email",
		"email", redaction.RedactEmail(email),
		"username", redaction.Redact(username),
	)

	return username, nil
}

// buildMemberAccessControlMessage builds a GenericFGAMessage for a committee member operation.
// For create/update, it sends member_put to add the user to the "member" relation.
// For delete, it sends member_remove with empty relations to remove all tuples for the user.
// FGA subjects use LFX usernames (not Auth0 subs).
func (uc *committeeWriterOrchestrator) buildMemberAccessControlMessage(ctx context.Context, member *model.CommitteeMember, action model.MessageAction) fgatypes.GenericFGAMessage {
	slog.DebugContext(ctx, "building member access control message",
		"username", redaction.Redact(member.Username),
		"committee_uid", member.CommitteeUID,
		"action", action,
	)

	if action == model.ActionDeleted {
		return fgatypes.GenericFGAMessage{
			ObjectType: "committee",
			Operation:  "member_remove",
			Data: fgatypes.GenericMemberData{
				UID:       member.CommitteeUID,
				Username:  member.Username,
				Relations: []string{},
			},
		}
	}

	return fgatypes.GenericFGAMessage{
		ObjectType: "committee",
		Operation:  "member_put",
		Data: fgatypes.GenericMemberData{
			UID:       member.CommitteeUID,
			Username:  member.Username,
			Relations: []string{constants.RelationMember},
		},
	}
}

// publishMemberMessages publishes indexer and access control messages for committee member operations
func (uc *committeeWriterOrchestrator) publishMemberMessages(ctx context.Context, action model.MessageAction, data *model.CommitteeMemberMessageData, sync bool) error {
	slog.DebugContext(ctx, "publishing member messages",
		"action", action,
	)

	// Build indexer message for the member
	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
	}

	// Customize the indexer message based on the action
	var memberData any
	switch action {
	case model.ActionCreated, model.ActionUpdated:
		// Add tags for create/update operations (when we have the full member data)
		indexerMessage.Tags = data.Member.Tags()

		var nameAndAliases []string
		for _, v := range []string{data.Member.CommitteeName, data.Member.FirstName, data.Member.LastName, data.Member.Username} {
			if v != "" {
				nameAndAliases = append(nameAndAliases, v)
			}
		}
		if data.Member.FirstName != "" && data.Member.LastName != "" {
			nameAndAliases = append(nameAndAliases, fmt.Sprintf("%s %s", data.Member.FirstName, data.Member.LastName))
		}
		indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
			ObjectID:             data.Member.UID,
			AccessCheckObject:    fmt.Sprintf("committee:%s", data.Member.CommitteeUID),
			AccessCheckRelation:  "viewer",
			HistoryCheckObject:   fmt.Sprintf("committee:%s", data.Member.CommitteeUID),
			HistoryCheckRelation: "auditor",
			SortName:             data.Member.FirstName,
			NameAndAliases:       nameAndAliases,
			ParentRefs:           []string{fmt.Sprintf("committee:%s", data.Member.CommitteeUID)},
			Tags:                 data.Member.Tags(),
			Fulltext:             fmt.Sprintf("%s %s %s %s", data.Member.FirstName, data.Member.LastName, data.Member.Email, data.Member.Organization.Name),
		}
		memberData = data.Member
	case model.ActionDeleted:
		// Indexer message only expects the UID for deleted operations
		memberData = data.Member.UID
	}

	indexerMessageBuild, errBuildIndexerMessage := indexerMessage.Build(ctx, memberData)
	if errBuildIndexerMessage != nil {
		slog.ErrorContext(ctx, "failed to build member indexer message",
			"error", errBuildIndexerMessage,
			"action", action,
		)
		return errs.NewUnexpected("failed to build member indexer message", errBuildIndexerMessage)
	}

	// Build event message for the member
	var eventInput any
	switch action {
	case model.ActionUpdated:
		// For updates, create the structured event data
		eventInput = &model.CommitteeMemberUpdateEventData{
			MemberUID: data.Member.UID,
			OldMember: data.OldMember,
			Member:    data.Member,
		}
	case model.ActionCreated:
		// For create, carry the request-scoped skip-notification flag alongside the member.
		eventInput = &model.CommitteeMemberCreatedEventData{
			CommitteeMember:  data.Member,
			SkipNotification: data.SkipNotification,
		}
	case model.ActionDeleted:
		// For delete, carry the request-scoped skip-notification flag alongside the member.
		eventInput = &model.CommitteeMemberDeletedEventData{
			CommitteeMember:  data.Member,
			SkipNotification: data.SkipNotification,
		}
	}

	eventMessage := model.CommitteeEvent{}
	eventMessageBuild, errBuildEventMessage := eventMessage.Build(ctx, model.ResourceCommitteeMember, action, eventInput)
	if errBuildEventMessage != nil {
		slog.ErrorContext(ctx, "failed to build member event message",
			"error", errBuildEventMessage,
			"action", action,
		)
		return errs.NewUnexpected("failed to build member event message", errBuildEventMessage)
	}

	// Build access control message for the member
	accessControlMessage := uc.buildMemberAccessControlMessage(ctx, data.Member, action)

	// Publish messages concurrently
	messages := []func() error{
		func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeMemberSubject, indexerMessageBuild, sync)
		},
		func() error {
			return uc.committeePublisher.Event(ctx, eventMessageBuild.Subject, eventMessageBuild, false)
		},
		func() error {
			// Only publish access message if username is present
			// Without a username, there's no user identity to grant access to in FGA
			if data.Member.Username == "" {
				slog.DebugContext(ctx, "skipping access message for member without username",
					"member_uid", data.Member.UID,
					"action", action,
				)
				return nil
			}
			// On update, remove old username tuple if identity changed to avoid stale FGA tuples.
			if action == model.ActionUpdated &&
				data.OldMember != nil &&
				data.OldMember.Username != "" &&
				data.OldMember.Username != data.Member.Username {
				oldAccessMsg := uc.buildMemberAccessControlMessage(ctx, data.OldMember, model.ActionDeleted)
				if err := uc.committeePublisher.Access(ctx, fgaconstants.GenericMemberRemoveSubject, oldAccessMsg, sync); err != nil {
					return err
				}
			}

			subject := fgaconstants.GenericMemberPutSubject
			if action == model.ActionDeleted {
				subject = fgaconstants.GenericMemberRemoveSubject
			}
			return uc.committeePublisher.Access(ctx, subject, accessControlMessage, sync)
		},
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
