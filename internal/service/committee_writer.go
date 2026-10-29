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
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// sanitizeCommitteeBaseForPublish returns a copy of base with the charter's updated_by email
// stripped. Used both for CommitteeIndexerMessage.Build, which marshals its input verbatim into
// Data alongside IndexingConfig.Public, and for the committee.updated NATS event payload -- in
// both cases an unsanitized copy would put the charter editor's email into a public-facing
// document (same treatment as ChatWebhookURL on CommitteeSettings below).
func sanitizeCommitteeBaseForPublish(base model.CommitteeBase) model.CommitteeBase {
	if base.Charter == nil || base.Charter.UpdatedBy == nil || base.Charter.UpdatedBy.Email == "" {
		return base
	}
	charter := *base.Charter
	updatedBy := *base.Charter.UpdatedBy
	updatedBy.Email = ""
	charter.UpdatedBy = &updatedBy
	base.Charter = &charter
	return base
}

// stampCharter builds a Charter with UpdatedBy resolved from the request's principal.
func (uc *committeeWriterOrchestrator) stampCharter(ctx context.Context, url string, version int, at time.Time) *model.Charter {
	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	principal = strings.TrimSpace(principal)
	var updatedBy *model.CommitteeUser
	if principal != "" {
		updatedBy = ResolveAuditUserProfile(ctx, uc.userReader, principal)
	}
	return &model.Charter{
		URL:       url,
		Version:   version,
		UpdatedAt: at,
		UpdatedBy: updatedBy,
	}
}

// buildCommitteeIndexingConfig constructs an IndexingConfig for a CommitteeBase document.
func buildCommitteeIndexingConfig(committee *model.Committee) *indexerTypes.IndexingConfig {
	var nameAndAliases []string
	if committee.Name != "" {
		nameAndAliases = append(nameAndAliases, committee.Name)
	}
	if committee.DisplayName != "" && committee.DisplayName != committee.Name {
		nameAndAliases = append(nameAndAliases, committee.DisplayName)
	}

	var parentRefs []string
	if committee.ProjectUID != "" {
		parentRefs = append(parentRefs, fmt.Sprintf("project:%s", committee.ProjectUID))
	}
	if committee.ParentUID != nil && *committee.ParentUID != "" {
		parentRefs = append(parentRefs, fmt.Sprintf("committee:%s", *committee.ParentUID))
	}

	var fulltextParts []string
	seen := make(map[string]bool)
	for _, part := range nameAndAliases {
		if !seen[part] {
			fulltextParts = append(fulltextParts, part)
			seen[part] = true
		}
	}
	if committee.Description != "" && !seen[committee.Description] {
		fulltextParts = append(fulltextParts, committee.Description)
	}

	public := committee.Public
	return &indexerTypes.IndexingConfig{
		ObjectID:             committee.CommitteeBase.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", committee.CommitteeBase.UID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", committee.CommitteeBase.UID),
		HistoryCheckRelation: "auditor",
		SortName:             committee.Name,
		NameAndAliases:       nameAndAliases,
		ParentRefs:           parentRefs,
		Tags:                 committee.Tags(),
		Fulltext:             strings.Join(fulltextParts, " "),
		Public:               &public,
	}
}

// CommitteeWriter defines the interface for committee write operations
type CommitteeWriter interface {
	CommitteeDataWriter
	CommitteeMemberDataWriter
}

// CommitteeDataWriter defines the interface for committee-specific write operations
type CommitteeDataWriter interface {
	// Create inserts a new committee into the storage, along with its settings, when applicable
	Create(ctx context.Context, committee *model.Committee, sync bool) (*model.Committee, error)
	// Update modifies an existing committee in the storage
	Update(ctx context.Context, committee *model.Committee, revision uint64, sync bool) (*model.Committee, error)
	// UpdateSettings modifies the settings of an existing committee in the storage
	UpdateSettings(ctx context.Context, settings *model.CommitteeSettings, revision uint64, sync bool) (*model.CommitteeSettings, error)
	// Delete removes a committee and all its associated data (secondary indices, settings)
	Delete(ctx context.Context, uid string, revision uint64, sync bool) error
}

// CommitteeMemberDataWriter defines the interface for committee member write operations
type CommitteeMemberDataWriter interface {
	// CreateMember inserts a new committee member into the storage
	CreateMember(ctx context.Context, member *model.CommitteeMember, sync bool, skipEnrichment bool) (*model.CommitteeMember, error)
	// UpdateMember modifies an existing committee member in the storage
	UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64, sync bool, skipEnrichment bool) (*model.CommitteeMember, error)
	// DeleteMember removes a committee member
	DeleteMember(ctx context.Context, committeeUID, uid string, revision uint64, sync bool, skipNotification bool) error
	// ReassignMember atomically replaces the holder of an existing seat: it creates newMember (the
	// replacement holder) and deletes the old member (oldMemberUID at oldRevision), rolling back the
	// newly-created member if the delete fails so no duplicate seat is left. It returns the created
	// member. Used by the Org Lens reassign flow (LFXV2-1865).
	ReassignMember(ctx context.Context, oldMemberUID string, oldRevision uint64, newMember *model.CommitteeMember, sync bool) (*model.CommitteeMember, error)
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

// WithUserReader sets the user reader
func WithUserReader(reader port.UserReader) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.userReader = reader
	}
}

// WithB2BOrgResolver sets the b2b_org resolver used to validate organization.id values.
func WithB2BOrgResolver(resolver port.B2BOrgResolver) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.b2bOrgResolver = resolver
	}
}

// WithB2BOrgFallbackResolver sets the name/website b2b_org resolver consulted
// only for legacy, non-SFID-shaped organization.id values, before B2BOrgResolver
// is attempted.
func WithB2BOrgFallbackResolver(resolver port.B2BOrgFallbackResolver) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.b2bOrgFallbackResolver = resolver
	}
}

// committeeWriterOrchestrator orchestrates the committee creation process
type committeeWriterOrchestrator struct {
	projectRetriever       port.ProjectReader
	committeeReader        port.CommitteeReader
	committeeWriter        port.CommitteeWriter
	committeePublisher     port.CommitteePublisher
	userReader             port.UserReader
	b2bOrgResolver         port.B2BOrgResolver
	b2bOrgFallbackResolver port.B2BOrgFallbackResolver
}

// maxSSOGroupNameAttempts bounds how many generated SSO group names are tried before giving up.
const maxSSOGroupNameAttempts = 100

// writeWithSSORetry runs write, which persists the committee, and when it fails because the SSO group
// name is already in use, generates the next candidate name from the project slug and runs it again.
// Storage enforces the uniqueness, so the retry happens only on that specific conflict; any other
// error, including a project and name conflict, is returned as is.
func (uc *committeeWriterOrchestrator) writeWithSSORetry(ctx context.Context, committee *model.Committee, slug string, write func() error) error {
	for attempt := 1; ; attempt++ {
		errWrite := write()
		if !errors.Is(errWrite, errs.ErrSSOGroupNameTaken) {
			return errWrite
		}

		if attempt >= maxSSOGroupNameAttempts {
			return errs.NewUnexpected("exceeded maximum retries for SSO name generation")
		}

		slog.WarnContext(ctx, "SSO group name already exists, retrying with a new name",
			"sso_group_name", committee.SSOGroupName,
			"attempt", attempt,
		)
		if errBuild := committee.SSOGroupNameBuild(ctx, slug); errBuild != nil {
			slog.ErrorContext(ctx, "failed to build SSO group name",
				"error", errBuild,
				"project_slug", slug,
				"committee_name", committee.Name,
			)
			return errs.NewUnexpected("failed to build SSO group name", errBuild)
		}
	}
}

// rollbackCreatedCommittee removes a committee that was created earlier in the same request, which
// also releases its name and SSO group reservations. It is best-effort and only logs failures.
func (uc *committeeWriterOrchestrator) rollbackCreatedCommittee(ctx context.Context, uid string) {
	_, rev, errGet := uc.committeeReader.GetBase(ctx, uid)
	if errGet != nil {
		slog.ErrorContext(ctx, "failed to read committee for rollback",
			"error", errGet,
			"committee_uid", uid,
			log.PriorityCritical(),
		)
		return
	}
	if errDelete := uc.committeeWriter.Delete(ctx, uid, rev); errDelete != nil {
		slog.ErrorContext(ctx, "failed to roll back committee",
			"error", errDelete,
			"committee_uid", uid,
			log.PriorityCritical(),
		)
	}
}

func (uc *committeeWriterOrchestrator) buildIndexerMessage(ctx context.Context, action model.MessageAction, committee any, tags []string) (*model.CommitteeIndexerMessage, error) {
	return buildIndexerMessage(ctx, action, committee, tags)
}

// buildIndexerMessage is a package-level helper so other handlers in this package
// (e.g. MailingListEventHandler) can build indexer messages without going through
// the orchestrator struct.
func buildIndexerMessage(ctx context.Context, action model.MessageAction, committee any, tags []string) (*model.CommitteeIndexerMessage, error) {
	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
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

func (uc *committeeWriterOrchestrator) buildAccessControlMessage(ctx context.Context, committee *model.Committee) fgatypes.GenericFGAMessage {
	relations := map[string][]string{}

	if committee.CommitteeSettings != nil && len(committee.Writers) > 0 {
		relations[constants.RelationWriter] = extractUsernames(committee.Writers)
	}

	if committee.CommitteeSettings != nil && len(committee.Auditors) > 0 {
		relations[constants.RelationAuditor] = extractUsernames(committee.Auditors)
	}

	data := fgatypes.GenericAccessData{
		UID:    committee.CommitteeBase.UID,
		Public: committee.Public,
		References: map[string][]string{
			constants.RelationProject: {committee.ProjectUID},
		},
		// member relations are managed separately via member_put and must not be overwritten here
		ExcludeRelations: []string{constants.RelationMember},
	}

	if len(relations) > 0 {
		data.Relations = relations
	}

	slog.DebugContext(ctx, "building access control message",
		"committee_uid", committee.CommitteeBase.UID,
	)

	return fgatypes.GenericFGAMessage{
		ObjectType: "committee",
		Operation:  "update_access",
		Data:       data,
	}
}

// extractUsernames extracts the Username field from a slice of CommitteeUser for use in access control messages.
func extractUsernames(users []model.CommitteeUser) []string {
	result := make([]string, 0, len(users))
	for _, u := range users {
		if u.Username != "" {
			result = append(result, u.Username)
		}
	}
	return result
}

// mergeCommitteeData merges existing committee data with updated fields
func (uc *committeeWriterOrchestrator) mergeCommitteeData(ctx context.Context, existing *model.CommitteeBase, updated *model.Committee) {
	// Preserve immutable fields
	updated.CommitteeBase.UID = existing.UID
	updated.CommitteeBase.CreatedAt = existing.CreatedAt
	ssoGroupName := existing.SSOGroupName

	// Preserve readonly computed fields — these are never part of the update
	// payload (see CommitteeBaseAttributes in cmd/committee-api/design/type.go),
	// so updated always carries their zero value. Without this, every base
	// update silently resets total_members, total_voting_repos, and
	// has_mailing_list to zero. Computed-field syncs (total_members drift
	// correction, has_mailing_list toggling) go through their own dedicated
	// storage writers (UpdateTotalMembers, UpdateHasMailingList) instead of
	// this generic path, so unconditional preservation here is safe.
	updated.TotalMembers = existing.TotalMembers
	updated.TotalVotingRepos = existing.TotalVotingRepos
	updated.HasMailingList = existing.HasMailingList

	// Update timestamp
	updated.CommitteeBase.UpdatedAt = time.Now()

	// Accept new SSO group name when name changed and SSO/public active,
	// or when SSO/public just enabled on an existing committee without an SSO name.
	if existing.Name != updated.Name && (updated.SSOGroupEnabled || updated.Public) {
		slog.DebugContext(ctx, "SSO group name updated",
			"old_sso_name", existing.SSOGroupName,
			"new_sso_name", updated.SSOGroupName,
		)
		ssoGroupName = updated.SSOGroupName
	} else if (updated.SSOGroupEnabled || updated.Public) && existing.SSOGroupName == "" {
		ssoGroupName = updated.SSOGroupName
	}
	updated.SSOGroupName = ssoGroupName

	// Charter: stamp version/updated_at/updated_by only when the URL actually changes
	// (including a change to/from ""), never on a same-value echo or on an absent payload
	// key. An absent key (updated.Charter == nil) means "caller didn't touch this field" and
	// must carry the existing charter forward untouched -- it is NOT the same as an explicit
	// {url: ""} clear, which is a real, stamped change. Once a charter has ever existed,
	// clearing it stamps url: "" like any other change rather than nilling the field back
	// out, so a removed charter still carries its own version/updated_at/updated_by for
	// "removed by X on Y" display, and Version never resets across a clear -> re-set cycle.
	existingCharterURL, existingCharterVersion := "", 0
	if existing.Charter != nil {
		existingCharterURL = existing.Charter.URL
		existingCharterVersion = existing.Charter.Version
	}
	switch {
	case updated.Charter == nil:
		updated.Charter = existing.Charter
	case updated.Charter.URL == existingCharterURL:
		updated.Charter = existing.Charter
	default:
		updated.Charter = uc.stampCharter(ctx, updated.Charter.URL, existingCharterVersion+1, time.Now())
	}
}

// Execute orchestrates the committee creation process
func (uc *committeeWriterOrchestrator) Create(ctx context.Context, committee *model.Committee, sync bool) (*model.Committee, error) {

	slog.DebugContext(ctx, "executing create committee use case",
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
		"sync", sync,
	)

	// Set committee identifiers and timestamps
	now := time.Now()
	committee.CommitteeBase.UID = uuid.New().String()
	committee.CommitteeBase.CreatedAt = now
	committee.CommitteeBase.UpdatedAt = now

	// Set timestamps and derived fields for committee settings if they exist
	if committee.CommitteeSettings != nil {
		committee.CommitteeSettings.CreatedAt = now
		committee.CommitteeSettings.UpdatedAt = now
		cs := committee.CommitteeSettings
		cs.HasChatWebhook = cs.ChatWebhookURL != nil && *cs.ChatWebhookURL != ""
	}

	// Charter: stamp version/updated_at/updated_by only when a non-empty URL was supplied
	// at creation time -- an empty/absent charter on a brand-new committee stays nil rather
	// than becoming a stamped empty object (mirrors the no-op case in mergeCommitteeData).
	if committee.Charter != nil && committee.Charter.URL != "" {
		committee.Charter = uc.stampCharter(ctx, committee.Charter.URL, 1, now)
	} else {
		committee.Charter = nil
	}

	// created marks that the committee was persisted, so an unexpected panic afterwards removes it again
	var created bool
	defer func() {
		if err := recover(); err != nil && created {
			uc.rollbackCreatedCommittee(ctx, committee.CommitteeBase.UID)
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

	// Generate the SSO group name when SSO is enabled or the committee is public (used as URL slug).
	// Storage enforces that the project and name and the SSO group name are unique when the committee
	// is created, and the SSO name is regenerated if it is already taken.
	if committee.SSOGroupEnabled || committee.Public {
		if errSSOGroupNameBuild := committee.SSOGroupNameBuild(ctx, slug); errSSOGroupNameBuild != nil {
			slog.ErrorContext(ctx, "failed to build SSO group name",
				"error", errSSOGroupNameBuild,
				"project_slug", slug,
				"committee_name", committee.Name,
			)
			return nil, errs.NewUnexpected("failed to build SSO group name", errSSOGroupNameBuild)
		}
	}

	// Create the committee and settings (if applicable)
	errCreate := uc.writeWithSSORetry(ctx, committee, slug, func() error {
		return uc.committeeWriter.Create(ctx, committee)
	})
	if errCreate != nil {
		slog.ErrorContext(ctx, "failed to create committee",
			"error", errCreate,
			"committee_uid", committee.CommitteeBase.UID,
		)
		return nil, errCreate
	}
	created = true

	slog.DebugContext(ctx, "committee created successfully",
		"committee_uid", committee.CommitteeBase.UID,
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	// Publish indexer messages for the committee and settings
	messages := []func() error{}

	committeeMsg, errBuildCommitteeMsg := uc.buildIndexerMessage(ctx, model.ActionCreated, sanitizeCommitteeBaseForPublish(committee.CommitteeBase), committee.Tags())
	if errBuildCommitteeMsg != nil {
		return nil, errs.NewUnexpected("failed to build indexer message", errBuildCommitteeMsg)
	}
	committeeMsg.IndexingConfig = buildCommitteeIndexingConfig(committee)
	messages = append(messages, func() error {
		return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, committeeMsg, sync)
	})

	if committee.CommitteeSettings != nil {
		indexSettings := *committee.CommitteeSettings
		indexSettings.ChatWebhookURL = nil // bearer credential — must not enter the search index
		settingsMsg, errBuildSettingsMsg := uc.buildIndexerMessage(ctx, model.ActionCreated, &indexSettings, committee.Tags())
		if errBuildSettingsMsg != nil {
			return nil, errs.NewUnexpected("failed to build indexer message", errBuildSettingsMsg)
		}
		settingsMsg.IndexingConfig = indexSettings.IndexingConfig(committee.Tags())
		messages = append(messages, func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeSettingsSubject, settingsMsg, sync)
		})
	}

	// Publish access control message for the committee
	accessControlMessage := uc.buildAccessControlMessage(ctx, committee)
	messages = append(messages, func() error {
		return uc.committeePublisher.UpdateAccess(ctx, accessControlMessage)
	})

	// all messages are executed concurrently
	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish committee messages",
			"error", errPublishingMessage,
			"committee_uid", committee.CommitteeBase.UID,
		)
	} else {
		slog.DebugContext(ctx, "indexer and access control messages published successfully",
			"committee_uid", committee.CommitteeBase.UID,
		)
	}

	return committee, nil
}

// Update orchestrates the committee update process
func (uc *committeeWriterOrchestrator) Update(ctx context.Context, committee *model.Committee, revision uint64, sync bool) (*model.Committee, error) {

	slog.DebugContext(ctx, "executing update committee use case",
		"committee_uid", committee.CommitteeBase.UID,
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
		"revision", revision,
		"sync", sync,
	)

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

	// Step 3: Generate the next SSO group name when the committee name changed and SSO or public is
	// active, or when SSO or public was just enabled on a committee without an SSO group name. Storage
	// enforces the new name and SSO group name are unique when the committee is updated.
	if (committee.SSOGroupEnabled || committee.Public) && (existing.Name != committee.Name || existing.SSOGroupName == "") {
		if errSSOChange := committee.SSOGroupNameBuild(ctx, slug); errSSOChange != nil {
			slog.ErrorContext(ctx, "failed to build SSO group name",
				"error", errSSOChange,
				"project_slug", slug,
				"committee_name", committee.Name,
			)
			return nil, errs.NewUnexpected("failed to build SSO group name", errSSOChange)
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
	errUpdate := uc.writeWithSSORetry(ctx, committee, slug, func() error {
		return uc.committeeWriter.UpdateBase(ctx, committee, revision)
	})
	if errUpdate != nil {
		slog.ErrorContext(ctx, "failed to update committee",
			"error", errUpdate,
			"committee_uid", committee.CommitteeBase.UID,
		)
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
	messageIndexer, errBuildIndexerMessage := uc.buildIndexerMessage(ctx, model.ActionUpdated, sanitizeCommitteeBaseForPublish(committee.CommitteeBase), committee.Tags())
	if errBuildIndexerMessage != nil {
		slog.WarnContext(ctx, "failed to build indexer message for update",
			"error", errBuildIndexerMessage,
			"committee_uid", committee.CommitteeBase.UID,
		)
		return nil, errBuildIndexerMessage
	}
	messageIndexer.IndexingConfig = buildCommitteeIndexingConfig(committee)

	settings, _, errGetSettings := uc.committeeReader.GetSettings(ctx, committee.CommitteeBase.UID)
	if errGetSettings != nil && !errors.As(errGetSettings, &errs.NotFound{}) {
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

	// Both before/after snapshots are marshaled verbatim into the committee.updated NATS
	// event; sanitize the charter editor's email out of each the same way the indexer
	// messages are sanitized above -- the sole in-repo consumer only reads
	// name/category/project fields, and this event has external subscribers too.
	sanitizedOldCommittee := sanitizeCommitteeBaseForPublish(*existing)
	sanitizedCommittee := sanitizeCommitteeBaseForPublish(committee.CommitteeBase)
	updateEventData := &model.CommitteeUpdateEventData{
		CommitteeUID: committee.CommitteeBase.UID,
		OldCommittee: &sanitizedOldCommittee,
		Committee:    &sanitizedCommittee,
	}

	messages := []func() error{
		func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, messageIndexer, sync)
		},
		func() error {
			return uc.committeePublisher.UpdateAccess(ctx, accessControlMessage)
		},
		func() error {
			committeeEvent := model.CommitteeEvent{}
			builtEvent, errBuild := committeeEvent.Build(ctx, model.ResourceCommittee, model.ActionUpdated, updateEventData)
			if errBuild != nil {
				slog.WarnContext(ctx, "failed to build committee updated event",
					"error", errBuild,
					"committee_uid", committee.CommitteeBase.UID,
				)
				return nil
			}
			return uc.committeePublisher.Event(ctx, constants.CommitteeUpdatedSubject, builtEvent, false)
		},
	}

	// all messages are executed concurrently
	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish committee update messages",
			"error", errPublishingMessage,
			"committee_uid", committee.CommitteeBase.UID,
		)
	} else {
		slog.DebugContext(ctx, "committee update messages published successfully",
			"committee_uid", committee.CommitteeBase.UID,
		)
	}
	// ******************************************************

	return committee, nil
}

// UpdateSettings orchestrates the committee settings update process
func (uc *committeeWriterOrchestrator) UpdateSettings(ctx context.Context, settings *model.CommitteeSettings, revision uint64, sync bool) (*model.CommitteeSettings, error) {
	slog.DebugContext(ctx, "executing update committee settings use case",
		"committee_uid", settings.UID,
		"revision", revision,
		"sync", sync,
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

	// Preserve writers/auditors from existing settings when the caller omits
	// them (nil = field not sent). An explicit empty slice (non-nil, len 0)
	// intentionally clears the list and is passed through as-is.
	if settings.Writers == nil {
		settings.Writers = existingSettings.Writers
	}
	if settings.Auditors == nil {
		settings.Auditors = existingSettings.Auditors
	}

	// nil (absent/null) → preserve existing; "" → explicit clear; non-empty URL → replace.
	// Because chat_webhook_url is write-only (not returned from GET), a GET→PUT
	// round-trip always sends nil — preserve prevents silently wiping the credential.
	if settings.ChatWebhookURL == nil {
		settings.ChatWebhookURL = existingSettings.ChatWebhookURL
	} else if *settings.ChatWebhookURL == "" {
		settings.ChatWebhookURL = nil
	}
	settings.HasChatWebhook = settings.ChatWebhookURL != nil && *settings.ChatWebhookURL != ""

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
	indexSettings := *settings
	indexSettings.ChatWebhookURL = nil // bearer credential — must not enter the search index
	messageIndexer, errBuildIndexerMessage := uc.buildIndexerMessage(ctx, model.ActionUpdated, &indexSettings, committee.Tags())
	if errBuildIndexerMessage != nil {
		slog.ErrorContext(ctx, "failed to build indexer message",
			"error", errBuildIndexerMessage,
		)
		return nil, errs.NewUnexpected("failed to build indexer message", errBuildIndexerMessage)
	}
	messageIndexer.IndexingConfig = indexSettings.IndexingConfig(committee.Tags())

	// Build and publish access control message
	accessControlMessage := uc.buildAccessControlMessage(ctx, committee)
	messages := []func() error{
		func() error {
			return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeSettingsSubject, messageIndexer, sync)
		},
		func() error {
			return uc.committeePublisher.UpdateAccess(ctx, accessControlMessage)
		},
	}

	errPublishingMessage := concurrent.NewWorkerPool(len(messages)).Run(ctx, messages...)
	if errPublishingMessage != nil {
		slog.ErrorContext(ctx, "failed to publish committee settings messages",
			"error", errPublishingMessage,
			"committee_uid", settings.UID,
		)
	}

	// Publish a domain event carrying old+new settings so subscribers can
	// detect newly added Writers/Auditors and send notification emails.
	updatedBy, _ := ctx.Value(constants.PrincipalContextID).(string)
	sanitizedOldSettings := *existingSettings
	sanitizedOldSettings.ChatWebhookURL = nil // bearer credential — must not enter NATS events
	sanitizedNewSettings := *settings
	sanitizedNewSettings.ChatWebhookURL = nil // bearer credential — must not enter NATS events
	settingsEvent, errBuildEvent := (&model.CommitteeEvent{}).Build(ctx, model.ResourceCommitteeSettings, model.ActionUpdated, &model.CommitteeSettingsUpdateEventData{
		CommitteeUID:  settings.UID,
		OldSettings:   &sanitizedOldSettings,
		Settings:      &sanitizedNewSettings,
		CommitteeName: committeeBase.Name,
		UpdatedBy:     updatedBy,
	})
	if errBuildEvent != nil {
		slog.WarnContext(ctx, "failed to build committee_settings.updated event — skipping",
			"error", errBuildEvent,
			"committee_uid", settings.UID,
		)
	} else if errEvent := uc.committeePublisher.Event(ctx, constants.CommitteeSettingsUpdatedSubject, settingsEvent, false); errEvent != nil {
		slog.WarnContext(ctx, "failed to publish committee_settings.updated event — skipping",
			"error", errEvent,
			"committee_uid", settings.UID,
		)
	}

	return settings, nil
}

// Delete orchestrates the committee deletion process
func (uc *committeeWriterOrchestrator) Delete(ctx context.Context, uid string, revision uint64, sync bool) error {
	slog.DebugContext(ctx, "executing delete committee use case",
		"committee_uid", uid,
		"revision", revision,
		"sync", sync,
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

	// Step 2: Delete the main committee record and settings; storage also releases the committee's
	// name and SSO group reservations
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

	// List applications for cascade cleanup; best-effort — proceed even if listing fails.
	applications, errListApps := uc.committeeReader.ListApplications(ctx, uid)
	if errListApps != nil {
		slog.WarnContext(ctx, "failed to list applications for cascade delete — skipping cascade",
			"error", errListApps,
			"committee_uid", uid,
			log.PriorityCritical(),
		)
		applications = nil
	}

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
			return uc.committeePublisher.Indexer(ctx, localSubject, localMessage, sync)
		})
	}

	// Build access control deletion message
	deleteMsg := fgatypes.GenericFGAMessage{
		ObjectType: "committee",
		Operation:  "delete_access",
		Data:       fgatypes.GenericDeleteData{UID: uid},
	}
	messages = append(messages, func() error {
		return uc.committeePublisher.DeleteAccess(ctx, deleteMsg)
	})

	// Cascade: emit indexer ActionDeleted and delete_access for each child application.
	for _, app := range applications {
		localApp := app

		appIndexerMsg := model.CommitteeIndexerMessage{
			Action: model.ActionDeleted,
			Tags:   localApp.Tags(),
		}
		built, errBuild := appIndexerMsg.Build(ctx, localApp.UID)
		if errBuild != nil {
			slog.WarnContext(ctx, "failed to build application indexer delete message — skipping",
				"error", errBuild,
				"application_uid", localApp.UID,
				log.PriorityCritical(),
			)
		} else {
			localBuilt := built
			messages = append(messages, func() error {
				return uc.committeePublisher.Indexer(ctx, constants.IndexCommitteeApplicationSubject, localBuilt, false)
			})
		}

		appDeleteMsg := fgatypes.GenericFGAMessage{
			ObjectType: "committee_application",
			Operation:  "delete_access",
			Data:       fgatypes.GenericDeleteData{UID: localApp.UID},
		}
		messages = append(messages, func() error {
			return uc.committeePublisher.DeleteAccess(ctx, appDeleteMsg)
		})

		messages = append(messages, func() error {
			if errKV := uc.committeeWriter.DeleteApplication(ctx, localApp); errKV != nil {
				slog.WarnContext(ctx, "failed to delete application KV record during cascade — skipping",
					"error", errKV,
					"application_uid", localApp.UID,
					log.PriorityCritical(),
				)
			}
			return nil
		})
	}

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
