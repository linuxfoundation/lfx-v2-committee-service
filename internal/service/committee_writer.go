// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/concurrent"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
)

// CommitteeWriter defines the interface for committee write operations
type CommitteeWriter interface {
	// Create inserts a new committee into the storage, along with its settings, when applicable
	Create(ctx context.Context, committee *model.Committee) (*model.Committee, error)
	// Update modifies an existing committee in the storage
	Update(ctx context.Context, committee *model.Committee, revision uint64) (*model.Committee, error)
	// UpdateSettings modifies the settings of an existing committee in the storage
	UpdateSettings(ctx context.Context, settings *model.CommitteeSettings, revision uint64) (*model.CommitteeSettings, error)
	// Delete removes a committee and all its associated data (secondary indices, settings)
	Delete(ctx context.Context, uid string, revision uint64) error
}

// committeeWriterOrchestratorOption defines a function type for setting options
type committeeWriterOrchestratorOption func(*committeeWriterOrchestrator)

// WithCommitteeWriter sets the committee writer
func WithCommitteeWriter(writer port.CommitteeWriter) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.committeeWriter = writer
	}
}

// WithCommitteeRetriever sets the committee retriever
func WithCommitteeRetriever(retriever port.CommitteeReader) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.committeeReader = retriever
	}
}

// WithProjectRetriever sets the project retriever
func WithProjectRetriever(retriever port.ProjectReader) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.projectRetriever = retriever
	}
}

// WithCommitteePublisher sets the committee publisher
func WithCommitteePublisher(publisher port.CommitteePublisher) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.committeePublisher = publisher
	}
}

// committeeWriterOrchestrator orchestrates the committee creation process
type committeeWriterOrchestrator struct {
	projectRetriever   port.ProjectReader
	committeeReader    port.CommitteeReader
	committeeWriter    port.CommitteeWriter
	committeePublisher port.CommitteePublisher
}

// deleteKeys removes keys by getting their revision and deleting them
// This is used both for rollback scenarios and cleanup of stale keys
func (uc *committeeWriterOrchestrator) deleteKeys(ctx context.Context, keys []string, isRollback bool) {
	if len(keys) == 0 {
		return
	}

	slog.DebugContext(ctx, "deleting keys",
		"keys", keys,
		"is_rollback", isRollback,
	)

	for _, key := range keys {
		rev, errGet := uc.committeeReader.GetRevision(ctx, key)
		if errGet != nil {
			slog.ErrorContext(ctx, "failed to get revision for key deletion",
				"key", key,
				"error", errGet,
				"is_rollback", isRollback,
			)
			continue
		}

		err := uc.committeeWriter.Delete(ctx, key, rev)
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete key",
				"key", key,
				"error", err,
				"is_rollback", isRollback,
				// This is critical because if we don't delete them,
				// names and SSO groups would be locked for reuse for a long time.
				log.PriorityCritical(),
			)
		}
		slog.DebugContext(ctx, "successfully deleted key",
			"key", key,
			"is_rollback", isRollback,
		)

	}

	slog.DebugContext(ctx, "key deletion completed",
		"keys_count", len(keys),
		"is_rollback", isRollback,
	)
}

// checkReserveSSOName checks if the SSO group name is unique and reserves it if it is
// It retries until it finds a unique name or returns an error
// This is used to ensure that the SSO group name is unique across all committees
// It builds the SSO group name based on the committee name and project slug
// It returns the unique key corresponding to the SSO group name or an error if it fails to find a unique name
func (uc *committeeWriterOrchestrator) checkReserveSSOName(ctx context.Context, committee *model.Committee, slug string) (string, error) {

	slog.DebugContext(ctx, "checking SSO group name uniqueness",
		"project_slug", slug,
		"committee_name", committee.Name,
	)

	const maxRetries = 100
	attempts := 0

	for {
		attempts++
		if attempts > maxRetries {
			return "", errs.NewUnexpected("exceeded maximum retries for SSO name generation")
		}

		errSSOGroupNameBuild := committee.SSOGroupNameBuild(ctx, slug)
		if errSSOGroupNameBuild != nil {
			slog.ErrorContext(ctx, "failed to build SSO group name",
				"error", errSSOGroupNameBuild,
				"project_slug", slug,
				"committee_name", committee.Name,
			)
			return "", errs.NewUnexpected("failed to build SSO group name", errSSOGroupNameBuild)
		}

		slog.DebugContext(ctx, "checking if SSO group exists",
			"sso_group_name", committee.SSOGroupName,
		)

		key, errBySSOGroupName := uc.committeeWriter.UniqueSSOGroupName(ctx, committee)
		if errors.As(errBySSOGroupName, &errs.Conflict{}) {
			slog.WarnContext(ctx, "SSO group name already exists, retrying with a new name",
				"sso_group_name", committee.SSOGroupName,
				"existing_uid", key,
			)
			continue
		}

		if errBySSOGroupName != nil {
			return "", errBySSOGroupName
		}

		slog.DebugContext(ctx, "SSO group name is unique, proceeding with creation",
			"sso_group_name", committee.SSOGroupName,
		)
		return key, nil
	}

}

func (uc *committeeWriterOrchestrator) buildIndexerMessage(ctx context.Context, committee any, tags []string) (*model.CommitteeIndexerMessage, error) {

	indexerMessage := model.CommitteeIndexerMessage{
		Action: model.ActionCreated,
		Tags:   tags,
	}

	messageIndexer, errIndexerMessageBuild := indexerMessage.Build(ctx, committee)
	if errIndexerMessageBuild != nil {
		slog.ErrorContext(ctx, "failed to build indexer message",
			"error", errIndexerMessageBuild,
		)
		return nil, errs.NewUnexpected("failed to build indexer message", errIndexerMessageBuild)
	}

	return messageIndexer, nil
}

func (uc *committeeWriterOrchestrator) buildAccessControlMessage(ctx context.Context, committee *model.Committee) *model.CommitteeAccessMessage {

	message := &model.CommitteeAccessMessage{
		UID:        committee.CommitteeBase.UID,
		ObjectType: "committee",
		Public:     committee.Public,
		Relations:  map[string][]string{},
		References: map[string]string{
			// project is required in the flow
			constants.RelationProject: committee.ProjectUID,
		},
	}

	slog.DebugContext(ctx, "building access control message",
		"message", message,
	)

	return message
}

func (uc *committeeWriterOrchestrator) rebuildCommitteeNameIndex(ctx context.Context, newNameKey string, existing *model.CommitteeBase) string {
	lastSlash := strings.LastIndex(newNameKey, "/")
	if lastSlash == -1 {
		return ""
	}
	prefix := newNameKey[:lastSlash+1]

	oldKeyName := &model.Committee{CommitteeBase: *existing}

	oldKeyNameIndex := prefix + oldKeyName.BuildIndexKey(ctx)

	slog.DebugContext(ctx, "rebuilding old name index",
		"existing_name_index", oldKeyNameIndex,
	)

	return oldKeyNameIndex
}

func (uc *committeeWriterOrchestrator) rebuildOldSSOIndexName(ctx context.Context, newSSOKey string, existing *model.CommitteeBase) string {
	lastSlash := strings.LastIndex(newSSOKey, "/")
	if lastSlash == -1 {
		return ""
	}
	prefix := newSSOKey[:lastSlash+1]

	slog.DebugContext(ctx, "rebuilding old SSO index name",
		"existing_sso_group_name_index", prefix+existing.SSOGroupName,
	)

	return prefix + existing.SSOGroupName
}

// mergeCommitteeData merges existing committee data with updated fields
func (uc *committeeWriterOrchestrator) mergeCommitteeData(ctx context.Context, existing *model.CommitteeBase, updated *model.Committee) {
	// Preserve immutable fields
	updated.CommitteeBase.UID = existing.UID
	updated.CommitteeBase.CreatedAt = existing.CreatedAt
	ssoGroupName := existing.SSOGroupName

	// Update timestamp
	updated.CommitteeBase.UpdatedAt = time.Now()

	// Log SSO group name update if applicable
	if existing.Name != updated.Name && updated.SSOGroupEnabled {
		slog.DebugContext(ctx, "SSO group name updated",
			"old_sso_name", existing.SSOGroupName,
			"new_sso_name", updated.SSOGroupName,
		)
		ssoGroupName = updated.SSOGroupName
	}
	updated.SSOGroupName = ssoGroupName
}

// Execute orchestrates the committee creation process
func (uc *committeeWriterOrchestrator) Create(ctx context.Context, committee *model.Committee) (*model.Committee, error) {

	slog.DebugContext(ctx, "executing create committee use case",
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	// Set committee identifiers and timestamps
	now := time.Now()
	committee.CommitteeBase.UID = uuid.New().String()
	committee.CommitteeBase.CreatedAt = now
	committee.CommitteeBase.UpdatedAt = now

	// Set timestamps for committee settings if they exist
	if committee.CommitteeSettings != nil {
		committee.CommitteeSettings.CreatedAt = now
		committee.CommitteeSettings.UpdatedAt = now
	}

	// for rollback purposes
	var (
		keys             []string
		rollbackRequired bool
	)
	defer func() {
		if err := recover(); err != nil || rollbackRequired {
			uc.deleteKeys(ctx, keys, true)
		}
	}()

	// Check project exists
	slug, errSlug := uc.projectRetriever.Slug(ctx, committee.ProjectUID)
	if errSlug != nil {
		slog.ErrorContext(ctx, "failed to retrieve project slug",
			"error", errSlug,
			"project_uid", committee.ProjectUID,
		)
		return nil, errSlug
	}
	committee.ProjectSlug = slug
	projectName, errProjectName := uc.projectRetriever.Name(ctx, committee.ProjectUID)
	if errProjectName != nil {
		slog.ErrorContext(ctx, "failed to retrieve project name",
			"error", errProjectName,
			"project_uid", committee.ProjectUID,
		)
		return nil, errProjectName
	}
	committee.ProjectName = projectName

	slog.DebugContext(ctx, "project found",
		"project_uid", committee.ProjectUID,
		"project_slug", slug,
		"project_name", projectName,
	)

	// Check parent committee exists (if specified)
	if committee.ParentUID != nil && *committee.ParentUID != "" {
		parent, revision, errParent := uc.committeeReader.GetBase(ctx, *committee.ParentUID)
		if errParent != nil {
			slog.ErrorContext(ctx, "parent committee not found",
				"error", errParent,
				"parent_uid", *committee.ParentUID,
			)
			return nil, errParent
		}
		slog.DebugContext(ctx, "parent committee found",
			"parent_uid", parent.UID,
			"parent_name", parent.Name,
			"revision", revision,
			"parent_project_uid", parent.ProjectUID,
		)
	}

	// Check if the project and committee name already exist
	uniqueNameProjectKey, errUniqueName := uc.committeeWriter.UniqueNameProject(ctx, committee)
	if errUniqueName != nil {
		return nil, errUniqueName
	}
	keys = append(keys, uniqueNameProjectKey)

	// Check SSO group exists (if specified)
	if committee.SSOGroupEnabled {
		uniqueSSOName, errCheckReserveSSOName := uc.checkReserveSSOName(ctx, committee, slug)
		if errCheckReserveSSOName != nil {
			return nil, errCheckReserveSSOName
		}
		keys = append(keys, uniqueSSOName)
	}

	// Create the committee and settings (if applicable)
	errCreate := uc.committeeWriter.Create(ctx, committee)
	if errCreate != nil {
		slog.ErrorContext(ctx, "failed to create committee",
			"error", errCreate,
			"committee_uid", committee.CommitteeBase.UID,
		)
		rollbackRequired = true
		return nil, errCreate
	}
	keys = append(keys, committee.CommitteeBase.UID)

	slog.DebugContext(ctx, "committee created successfully",
		"committee_uid", committee.CommitteeBase.UID,
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	// Publish indexer messages for the committee and settings
	messages := []func() error{}
	for subject, data := range map[string]any{
		constants.IndexCommitteeSubject:         committee.CommitteeBase,
		constants.IndexCommitteeSettingsSubject: committee.CommitteeSettings,
	} {
		message, errBuildIndexerMessage := uc.buildIndexerMessage(ctx, data, committee.Tags())
		if errBuildIndexerMessage != nil {
			return nil, errs.NewUnexpected("failed to build indexer message", errBuildIndexerMessage)
		}

		localSubject := subject
		localMessage := message

		messages = append(messages, func() error {
			return uc.committeePublisher.Indexer(ctx, localSubject, localMessage)
		})
	}

	// Publish access control message for the committee
	accessControlMessage := uc.buildAccessControlMessage(ctx, committee)
	messages = append(messages, func() error {
		return uc.committeePublisher.Access(ctx, constants.UpdateAccessCommitteeSubject, accessControlMessage)
	})

	// all messages are executed concurrently
	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish indexer message",
			"error", errPublishingMessage,
			"committee_uid", committee.CommitteeBase.UID,
		)
	}

	slog.DebugContext(ctx, "indexer and access control messages published successfully",
		"committee_uid", committee.CommitteeBase.UID,
	)

	return committee, nil
}

// Update orchestrates the committee update process
func (uc *committeeWriterOrchestrator) Update(ctx context.Context, committee *model.Committee, revision uint64) (*model.Committee, error) {

	slog.DebugContext(ctx, "executing update committee use case",
		"committee_uid", committee.CommitteeBase.UID,
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
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
			uc.deleteKeys(ctx, newKeys, true)
		}
		if updateSucceeded && len(staleKeys) > 0 {
			slog.DebugContext(ctx, "cleaning up stale keys",
				"keys_count", len(staleKeys),
			)
			go func() {
				// Cleanup stale keys in a separate goroutine
				// new context to avoid blocking the main flow
				ctxCleanup, cancel := context.WithTimeout(context.Background(), time.Second*10)
				defer cancel()
				uc.deleteKeys(ctxCleanup, staleKeys, false)
			}()
		}
	}()

	// Step 1: Retrieve existing data from the repository
	existing, existingRevision, errGet := uc.committeeReader.GetBase(ctx, committee.CommitteeBase.UID)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to retrieve existing committee",
			"error", errGet,
			"committee_uid", committee.CommitteeBase.UID,
		)
		return nil, errGet
	}

	// Verify revision matches to ensure optimistic locking
	// We will check again during the update process, but this is for fail-fast
	if existingRevision != revision {
		slog.WarnContext(ctx, "revision mismatch during update",
			"expected_revision", revision,
			"current_revision", existingRevision,
			"committee_uid", committee.CommitteeBase.UID,
		)
		return nil, errs.NewConflict("committee has been modified by another process")
	}

	slog.DebugContext(ctx, "existing committee retrieved",
		"committee_uid", existing.UID,
		"existing_name", existing.Name,
		"existing_project_uid", existing.ProjectUID,
	)

	// Step 2: Validate project change
	// Validate new project exists
	slug, errSlug := uc.projectRetriever.Slug(ctx, committee.ProjectUID)
	if errSlug != nil {
		slog.ErrorContext(ctx, "new project not found",
			"error", errSlug,
			"project_uid", committee.ProjectUID,
		)
		return nil, errSlug
	}
	committee.ProjectSlug = slug
	projectName, errProjectName := uc.projectRetriever.Name(ctx, committee.ProjectUID)
	if errProjectName != nil {
		slog.ErrorContext(ctx, "failed to retrieve new project name",
			"error", errProjectName,
			"project_uid", committee.ProjectUID,
		)
		return nil, errProjectName
	}
	committee.ProjectName = projectName

	// Step 3: Validate name change
	if existing.Name != committee.Name {
		newNameKey, errNameChange := uc.committeeWriter.UniqueNameProject(ctx, committee)
		if errNameChange != nil {
			return nil, errNameChange
		}
		if newNameKey != "" {
			newKeys = append(newKeys, newNameKey)
			// Save old name key for cleanup
			oldNameKey := uc.rebuildCommitteeNameIndex(ctx, newNameKey, existing)
			staleKeys = append(staleKeys, oldNameKey)
		}
		// Step 3.1: Handle SSO Group Name changes (if name changed)
		if committee.SSOGroupEnabled {
			newSSOKey, errSSOChange := uc.checkReserveSSOName(ctx, committee, slug)
			if errSSOChange != nil {
				rollbackRequired = true
				return nil, errSSOChange
			}
			if newSSOKey != "" {
				newKeys = append(newKeys, newSSOKey)
				// Add old SSO key for cleanup if it exists
				if existing.SSOGroupName != "" {
					oldSSOKey := uc.rebuildOldSSOIndexName(ctx, newSSOKey, existing)
					if oldSSOKey != "" {
						staleKeys = append(staleKeys, oldSSOKey)
					}
				}
			}
		}

	}

	// Step 4: Validate parent change
	if (existing.ParentUID == nil && committee.ParentUID != nil) ||
		(existing.ParentUID != nil && committee.ParentUID == nil) ||
		(existing.ParentUID != nil && committee.ParentUID != nil && *existing.ParentUID != *committee.ParentUID) {

		if committee.ParentUID != nil && *committee.ParentUID != "" {
			parent, parentRevision, errParent := uc.committeeReader.GetBase(ctx, *committee.ParentUID)
			if errParent != nil {
				slog.ErrorContext(ctx, "new parent committee not found",
					"error", errParent,
					"parent_uid", *committee.ParentUID,
				)
				rollbackRequired = true
				return nil, errParent
			}
			slog.DebugContext(ctx, "new parent committee found",
				"parent_uid", parent.UID,
				"parent_name", parent.Name,
				"revision", parentRevision,
			)
		}
	}

	// Step 5: Merge existing data with updated fields
	uc.mergeCommitteeData(ctx, existing, committee)

	// Step 6: Update the committee in storage
	errUpdate := uc.committeeWriter.UpdateBase(ctx, committee, revision)
	if errUpdate != nil {
		slog.ErrorContext(ctx, "failed to update committee",
			"error", errUpdate,
			"committee_uid", committee.CommitteeBase.UID,
		)
		rollbackRequired = true
		return nil, errUpdate
	}

	slog.DebugContext(ctx, "committee updated successfully",
		"committee_uid", committee.CommitteeBase.UID,
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	// ******************************************************
	// Step 7: Publish messages

	// Build and publish indexer message
	messageIndexer, errBuildIndexerMessage := uc.buildIndexerMessage(ctx, committee.CommitteeBase, committee.Tags())
	if errBuildIndexerMessage != nil {
		slog.WarnContext(ctx, "failed to build indexer message for update",
			"error", errBuildIndexerMessage,
			"committee_uid", committee.CommitteeBase.UID,
		)
		return nil, errBuildIndexerMessage
	}

	settings, _, errGetSettings := uc.committeeReader.GetSettings(ctx, committee.CommitteeBase.UID)
	if errGetSettings != nil && !errors.Is(errGetSettings, errs.NotFound{}) {
		slog.ErrorContext(ctx, "failed to retrieve committee settings",
			"error", errGetSettings,
			"committee_uid", committee.CommitteeBase.UID,
		)
		return nil, errGetSettings
	}
	// send message with empty settings if not found
	if settings == nil {
		settings = &model.CommitteeSettings{}
	}
	// Build access control message
	fullCommittee := &model.Committee{
		CommitteeBase:     committee.CommitteeBase,
		CommitteeSettings: settings,
	}
	accessControlMessage := uc.buildAccessControlMessage(ctx, fullCommittee)
	// Publish both messages
	messages := []func() error{
		func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, messageIndexer)
		},
		func() error {
			return uc.committeePublisher.Access(ctx, constants.UpdateAccessCommitteeSubject, accessControlMessage)
		},
	}

	// all messages are executed concurrently
	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish indexer message",
			"error", errPublishingMessage,
			"committee_uid", committee.CommitteeBase.UID,
		)
	}
	// ******************************************************

	slog.DebugContext(ctx, "committee update completed successfully",
		"committee_uid", committee.CommitteeBase.UID,
		"stale_keys_count", len(staleKeys),
	)

	// Mark update as successful for defer cleanup
	updateSucceeded = true
	return committee, nil
}

// UpdateSettings orchestrates the committee settings update process
func (uc *committeeWriterOrchestrator) UpdateSettings(ctx context.Context, settings *model.CommitteeSettings, revision uint64) (*model.CommitteeSettings, error) {
	slog.DebugContext(ctx, "executing update committee settings use case",
		"committee_uid", settings.UID,
		"revision", revision,
	)

	// Step 1: Retrieve existing settings from the repository to verify they exist
	existingSettings, existingRevision, errGet := uc.committeeReader.GetSettings(ctx, settings.UID)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to retrieve existing committee settings",
			"error", errGet,
			"committee_uid", settings.UID,
		)
		return nil, errGet
	}

	// Verify revision matches to ensure optimistic locking
	if existingRevision != revision {
		slog.WarnContext(ctx, "revision mismatch during settings update",
			"expected_revision", revision,
			"current_revision", existingRevision,
			"committee_uid", settings.UID,
		)
		return nil, errs.NewConflict("committee settings have been modified by another process")
	}

	slog.DebugContext(ctx, "existing committee settings retrieved",
		"committee_uid", existingSettings.UID,
		"business_email_required", existingSettings.BusinessEmailRequired,
	)

	// Step 2: Merge existing data with updated fields
	// Preserve readonly fields
	settings.UID = existingSettings.UID
	settings.CreatedAt = existingSettings.CreatedAt
	settings.UpdatedAt = time.Now().UTC()

	// Step 3: Update the committee settings in storage
	errUpdate := uc.committeeWriter.UpdateSetting(ctx, settings, revision)
	if errUpdate != nil {
		slog.ErrorContext(ctx, "failed to update committee settings",
			"error", errUpdate,
			"committee_uid", settings.UID,
		)
		return nil, errUpdate
	}

	slog.DebugContext(ctx, "committee settings updated successfully",
		"committee_uid", settings.UID,
		"business_email_required", settings.BusinessEmailRequired,
	)

	// ******************************************************
	committeeBase, _, errGet := uc.committeeReader.GetBase(ctx, settings.UID)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to retrieve committee",
			"error", errGet,
			"committee_uid", settings.UID,
		)
		return nil, errGet
	}

	committee := &model.Committee{CommitteeBase: *committeeBase, CommitteeSettings: settings}
	// Build and publish indexer message
	messageIndexer, errBuildIndexerMessage := uc.buildIndexerMessage(ctx, settings, committee.Tags())
	if errBuildIndexerMessage != nil {
		slog.ErrorContext(ctx, "failed to build indexer message",
			"error", errBuildIndexerMessage,
		)
		return nil, errs.NewUnexpected("failed to build indexer message", errBuildIndexerMessage)
	}

	// Build and publish access control message
	accessControlMessage := uc.buildAccessControlMessage(ctx, committee)
	messages := []func() error{
		func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeSettingsSubject, messageIndexer)
		},
		func() error {
			return uc.committeePublisher.Access(ctx, constants.UpdateAccessCommitteeSubject, accessControlMessage)
		},
	}

	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish access control message",
			"error", errPublishingMessage,
			"committee_uid", settings.UID,
		)
	}

	// ******************************************************

	return settings, nil
}

// Delete orchestrates the committee deletion process
func (uc *committeeWriterOrchestrator) Delete(ctx context.Context, uid string, revision uint64) error {
	slog.DebugContext(ctx, "executing delete committee use case",
		"committee_uid", uid,
		"revision", revision,
	)

	// Step 1: Retrieve existing committee data to get all the information needed for cleanup
	existing, existingRevision, errGet := uc.committeeReader.GetBase(ctx, uid)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to retrieve existing committee for deletion",
			"error", errGet,
			"committee_uid", uid,
		)
		return errGet
	}

	// Verify revision matches to ensure optimistic locking
	if existingRevision != revision {
		slog.WarnContext(ctx, "revision mismatch during deletion",
			"expected_revision", revision,
			"current_revision", existingRevision,
			"committee_uid", uid,
		)
		return errs.NewConflict("committee has been modified by another process")
	}

	slog.DebugContext(ctx, "existing committee retrieved for deletion",
		"committee_uid", existing.UID,
		"committee_name", existing.Name,
		"project_uid", existing.ProjectUID,
		"sso_group_name", existing.SSOGroupName,
	)

	// Step 2: Build list of secondary indices to delete
	var indicesToDelete []string

	// Build project+name index key
	committee := &model.Committee{CommitteeBase: *existing}
	nameIndexKey := fmt.Sprintf(constants.KVLookupPrefix, committee.BuildIndexKey(ctx))
	indicesToDelete = append(indicesToDelete, nameIndexKey)

	// Build SSO group name index key if it exists
	if existing.SSOGroupEnabled && existing.SSOGroupName != "" {
		ssoIndexKey := fmt.Sprintf(constants.KVLookupSSOGroupNamePrefix, existing.SSOGroupName)
		indicesToDelete = append(indicesToDelete, ssoIndexKey)
	}

	slog.DebugContext(ctx, "secondary indices identified for deletion",
		"committee_uid", uid,
		"indices_count", len(indicesToDelete),
		"indices", indicesToDelete,
	)

	// Step 3: Delete the main committee record and settings
	errDelete := uc.committeeWriter.Delete(ctx, uid, revision)
	if errDelete != nil {
		slog.ErrorContext(ctx, "failed to delete committee",
			"error", errDelete,
			"committee_uid", uid,
		)
		return errDelete
	}

	slog.DebugContext(ctx, "committee main record and settings deleted successfully",
		"committee_uid", uid,
	)

	// Step 4: Delete secondary indices
	// We use the deleteKeys method which handles errors gracefully and logs them
	// We don't abort here - secondary indices have a minor impact during deletion compared to the main index
	// and access control, which must be executed successfully to avoid data inconsistency in the following steps
	uc.deleteKeys(ctx, indicesToDelete, false)

	// Prepare messages for publishing
	messages := []func() error{}

	// Build and publish indexer messages for committee and settings deletion
	for subject, data := range map[string]any{
		constants.IndexCommitteeSubject:         uid,
		constants.IndexCommitteeSettingsSubject: uid,
	} {
		indexerMessage := model.CommitteeIndexerMessage{
			Action: model.ActionDeleted,
		}

		message, errBuildIndexerMessage := indexerMessage.Build(ctx, data)
		if errBuildIndexerMessage != nil {
			slog.WarnContext(ctx, "failed to build indexer deletion message",
				"error", errBuildIndexerMessage,
				"subject", subject,
				"committee_uid", uid,
				log.PriorityCritical(),
			)
			continue
		}

		localSubject := subject
		localMessage := message

		messages = append(messages, func() error {
			return uc.committeePublisher.Indexer(ctx, localSubject, localMessage)
		})
	}

	// Build access control deletion message
	messages = append(messages, func() error {
		return uc.committeePublisher.Access(ctx, constants.DeleteAllAccessCommitteeSubject, uid)
	})

	// Execute all messages concurrently
	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish deletion messages",
			"error", errPublishingMessage,
			"committee_uid", uid,
		)
		return errPublishingMessage
	}

	slog.DebugContext(ctx, "committee deletion completed successfully",
		"committee_uid", uid,
		"indices_deleted", len(indicesToDelete),
	)

	return nil
}

// NewcommitteeWriterOrchestrator creates a new create committee use case using the option pattern
func NewCommitteeWriterOrchestrator(opts ...committeeWriterOrchestratorOption) CommitteeWriter {
	uc := &committeeWriterOrchestrator{}
	for _, opt := range opts {
		opt(uc)
	}
	return uc
}
