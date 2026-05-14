// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	emailsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service/email"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"golang.org/x/sync/errgroup"
)

// messageHandlerOrchestrator orchestrates the message handling process
type messageHandlerOrchestrator struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	committeeWriter             port.CommitteeWriter
	committeePublisher          port.CommitteePublisher
	emailSender                 port.EmailSender
	userReader                  port.UserReader
	lfxSelfServeBaseURL         string
}

// messageHandlerOrchestratorOption defines a function type for setting options
type messageHandlerOrchestratorOption func(*messageHandlerOrchestrator)

// WithCommitteeReaderForMessageHandler sets the committee reader for message handler
func WithCommitteeReaderForMessageHandler(reader CommitteeReader) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.committeeReader = reader
	}
}

// WithCommitteeWriterForMessageHandler sets the committee writer for message handler
func WithCommitteeWriterForMessageHandler(writer port.CommitteeWriter) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.committeeWriter = writer
	}
}

// WithCommitteePublisherForMessageHandler sets the committee publisher for message handler
func WithCommitteePublisherForMessageHandler(publisher port.CommitteePublisher) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.committeePublisher = publisher
	}
}

// WithCommitteeWriterOrchestratorForMessageHandler sets the service-level committee writer for member sync
func WithCommitteeWriterOrchestratorForMessageHandler(writer CommitteeWriter) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.committeeWriterOrchestrator = writer
	}
}

// WithEmailSenderForMessageHandler sets the email sender for notification emails.
func WithEmailSenderForMessageHandler(sender port.EmailSender) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.emailSender = sender
	}
}

// WithLFXSelfServeBaseURLForMessageHandler sets the base URL used to build links in notification emails.
func WithLFXSelfServeBaseURLForMessageHandler(baseURL string) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.lfxSelfServeBaseURL = baseURL
	}
}

// WithUserReaderForMessageHandler sets the user reader used to resolve display names for notification emails.
func WithUserReaderForMessageHandler(reader port.UserReader) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.userReader = reader
	}
}

// HandleCommitteeGetAttribute handles the retrieval of a specific attribute from the committee
func (m *messageHandlerOrchestrator) HandleCommitteeGetAttribute(ctx context.Context, msg port.TransportMessenger, attribute string) ([]byte, error) {

	// Parse message data to extract committee UID
	uid := string(msg.Data())

	slog.DebugContext(ctx, "committee get name request",
		"committee_uid", uid,
		"attribute", attribute,
	)

	// Validate that the committee ID is a valid UUID.
	_, err := uuid.Parse(uid)
	if err != nil {
		slog.ErrorContext(ctx, "error parsing committee ID", "error", err)
		return nil, err
	}

	// Use the committee reader to get the committee base information
	committee, _, err := m.committeeReader.GetBase(ctx, uid)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee base",
			"error", err,
			"committee_uid", uid,
		)
		return nil, err
	}

	value, ok := fields.LookupByTag(committee, "json", attribute)
	if !ok {
		slog.ErrorContext(ctx, "attribute not found in committee",
			"attribute", attribute,
			"committee_uid", uid,
		)
		return nil, errors.NewNotFound(fmt.Sprintf("attribute %s not found in committee %s", attribute, uid))
	}

	strValue, ok := value.(string)
	if !ok {
		slog.ErrorContext(ctx, "attribute value is not a string",
			"attribute", attribute,
			"committee_uid", uid,
			"value_type", fmt.Sprintf("%T", value),
		)
		return nil, errors.NewValidation(fmt.Sprintf("attribute %s value is not a string", attribute))
	}

	return []byte(strValue), nil
}

// HandleCommitteeListMembers handles the retrieval of all members for a committee
func (m *messageHandlerOrchestrator) HandleCommitteeListMembers(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {

	// Parse message data to extract committee UID
	uid := string(msg.Data())

	slog.DebugContext(ctx, "committee list members request",
		"committee_uid", uid,
	)

	// Validate that the committee ID is a valid UUID.
	_, err := uuid.Parse(uid)
	if err != nil {
		slog.ErrorContext(ctx, "error parsing committee ID", "error", err)
		return nil, err
	}

	// Check if the committee exists first
	_, _, err = m.committeeReader.GetBase(ctx, uid)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee base",
			"error", err,
			"committee_uid", uid,
		)
		return nil, err
	}

	// Get all members for the committee
	members, err := m.committeeReader.ListMembers(ctx, uid)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list committee members",
			"error", err,
			"committee_uid", uid,
		)
		return nil, err
	}

	// Marshal the members to JSON
	membersJSON, err := json.Marshal(members)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal committee members",
			"error", err,
			"committee_uid", uid,
		)
		return nil, errors.NewUnexpected("failed to marshal committee members", err)
	}

	slog.DebugContext(ctx, "committee list members response",
		"committee_uid", uid,
		"member_count", len(members),
	)

	return membersJSON, nil
}

// HandleCommitteeMailingListChanged processes a CommitteeMailingListChangedEvent from mailing-list-api.
// It updates the committee's has_mailing_list flag in KV and re-indexes the committee if the flag changed.
func (m *messageHandlerOrchestrator) HandleCommitteeMailingListChanged(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeMailingListChangedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal CommitteeMailingListChangedEvent", "error", err)
		return nil, err
	}

	if event.CommitteeUID == "" {
		slog.WarnContext(ctx, "CommitteeMailingListChangedEvent received with empty committee_uid — discarding")
		return nil, nil
	}

	slog.InfoContext(ctx, "processing committee mailing list change",
		"committee_uid", event.CommitteeUID,
		"has_mailing_list", event.HasMailingList,
	)

	committee, changed, err := m.committeeWriter.UpdateHasMailingList(ctx, event.CommitteeUID, event.HasMailingList)
	if err != nil {
		slog.ErrorContext(ctx, "failed to update has_mailing_list",
			"committee_uid", event.CommitteeUID, "error", err)
		return nil, err
	}
	if !changed {
		slog.DebugContext(ctx, "has_mailing_list already matches — skipping re-index",
			"committee_uid", event.CommitteeUID,
			"has_mailing_list", event.HasMailingList,
		)
		return nil, nil
	}

	fullCommittee := &model.Committee{CommitteeBase: *committee}
	if settings, _, errSettings := m.committeeReader.GetSettings(ctx, event.CommitteeUID); errSettings == nil {
		fullCommittee.CommitteeSettings = settings
	}

	indexerMsg, err := buildIndexerMessage(ctx, model.ActionUpdated, committee, fullCommittee.Tags())
	if err != nil {
		slog.ErrorContext(ctx, "failed to build indexer message",
			"committee_uid", event.CommitteeUID, "error", err)
		return nil, err
	}
	indexerMsg.IndexingConfig = buildCommitteeIndexingConfig(fullCommittee)

	if err := m.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, indexerMsg, false); err != nil {
		slog.ErrorContext(ctx, "failed to publish committee indexer update",
			"committee_uid", event.CommitteeUID, "error", err)
		return nil, err
	}

	return nil, nil
}

// HandleCommitteeUpdated reacts to a committee.updated event. It delegates
// re-sync decisions to the domain model and re-syncs member documents when needed.
// All members are processed regardless of individual failures (best-effort).
// A combined error is returned at the end if any member failed.
func (m *messageHandlerOrchestrator) HandleCommitteeUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {

	if m.committeeWriterOrchestrator == nil {
		return nil, errors.NewValidation("committee writer orchestrator is required for handling committee updated events")
	}

	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal CommitteeEvent", "error", err)
		return nil, err
	}

	// event.Data is map[string]interface{} after JSON round-trip; re-marshal to decode into the concrete type.
	rawData, errMarshal := json.Marshal(event.Data)
	if errMarshal != nil {
		slog.WarnContext(ctx, "CommitteeUpdated event has unexpected data shape — discarding", "error", errMarshal)
		return nil, nil
	}
	var data model.CommitteeUpdateEventData
	if err := json.Unmarshal(rawData, &data); err != nil {
		slog.WarnContext(ctx, "CommitteeUpdated event data cannot be decoded — discarding", "error", err)
		return nil, nil
	}

	if !data.RequiresMemberSync() {
		slog.DebugContext(ctx, "no denormalized fields changed — skipping member sync",
			"committee_uid", data.CommitteeUID)
		return nil, nil
	}

	if data.CommitteeUID == "" {
		slog.WarnContext(ctx, "CommitteeUpdated event missing committee_uid — discarding")
		return nil, nil
	}

	// Inject service-account identity so downstream indexer calls include a valid
	// authorization header. Pattern follows lfx-v2-meeting-service:
	// internal/infrastructure/eventing/nats_publisher.go — static "Bearer <service-name>"
	// is used for background operations that have no originating HTTP request context.
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	slog.InfoContext(ctx, "denormalized fields changed — syncing members",
		"committee_uid", data.CommitteeUID)

	members, err := m.committeeReader.ListMembers(ctx, data.CommitteeUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list members for sync",
			"committee_uid", data.CommitteeUID, "error", err)
		return nil, err
	}

	var syncErrors []error

	for _, member := range members {
		if !member.NeedsSyncWith(data.Committee) {
			slog.DebugContext(ctx, "member already up to date — skipping",
				"member_uid", member.UID, "committee_uid", data.CommitteeUID)
			continue
		}

		member.CommitteeName = data.Committee.Name
		member.CommitteeCategory = data.Committee.Category
		member.ProjectUID = data.Committee.ProjectUID
		member.ProjectSlug = data.Committee.ProjectSlug

		revision, errRev := m.committeeReader.GetMemberRevision(ctx, member.UID)
		if errRev != nil {
			slog.ErrorContext(ctx, "failed to get member revision during sync",
				"member_uid", member.UID, "committee_uid", data.CommitteeUID, "error", errRev)
			syncErrors = append(syncErrors, errRev)
			continue
		}

		if _, errUpdate := m.committeeWriterOrchestrator.UpdateMember(ctx, member, revision, false); errUpdate != nil {
			slog.ErrorContext(ctx, "failed to update member during sync",
				"member_uid", member.UID, "committee_uid", data.CommitteeUID, "error", errUpdate)
			syncErrors = append(syncErrors, errUpdate)
		}
	}

	slog.InfoContext(ctx, "member sync completed",
		"committee_uid", data.CommitteeUID,
		"members_processed", len(members),
		"failures", len(syncErrors))

	if len(syncErrors) > 0 {
		return nil, stderrors.Join(syncErrors...)
	}

	return nil, nil
}

// HandleCommitteeTotalMembersSync reacts to committee_member.created and committee_member.deleted
// stream events. It recounts the active members for the committee and delegates to the service
// layer Update so that KV write and re-indexing are handled consistently in one place.
// The caller (infrastructure layer) owns ACK/NAK.
func (m *messageHandlerOrchestrator) HandleCommitteeTotalMembersSync(ctx context.Context, msg port.StreamMessenger) error {
	if m.committeeWriterOrchestrator == nil {
		return errors.NewValidation("committee writer orchestrator is required for handling total_members sync events")
	}

	subject := msg.Subject()

	if subject != constants.CommitteeMemberCreatedSubject && subject != constants.CommitteeMemberDeletedSubject {
		slog.DebugContext(ctx, "stream message subject not relevant for total_members sync — skipping",
			"subject", subject,
		)
		return nil
	}

	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal CommitteeEvent for total_members sync", "error", err)
		return err
	}

	rawData, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "total_members sync event has unexpected data shape — discarding", "error", err)
		return nil
	}

	var member model.CommitteeMember
	if err := json.Unmarshal(rawData, &member); err != nil {
		slog.WarnContext(ctx, "total_members sync event data cannot be decoded — discarding", "error", err)
		return nil
	}

	if member.CommitteeUID == "" {
		slog.WarnContext(ctx, "total_members sync event missing committee_uid — discarding")
		return nil
	}

	committeeUID := member.CommitteeUID

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	slog.DebugContext(ctx, "starting total_members sync",
		"committee_uid", committeeUID,
		"subject", subject,
	)

	members, err := m.committeeReader.ListMembers(ctx, committeeUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list members for total_members sync",
			"committee_uid", committeeUID, "error", err)
		return err
	}
	actualCount := len(members)

	committee, revision, err := m.committeeReader.GetBase(ctx, committeeUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee base for total_members sync",
			"committee_uid", committeeUID, "error", err)
		return err
	}

	if committee.TotalMembers == actualCount {
		slog.DebugContext(ctx, "total_members already correct — skipping update",
			"committee_uid", committeeUID,
			"total_members", actualCount,
		)
		return nil
	}

	slog.DebugContext(ctx, "updating total_members counter",
		"committee_uid", committeeUID,
		"previous", committee.TotalMembers,
		"actual", actualCount,
	)

	committee.TotalMembers = actualCount

	if _, err := m.committeeWriterOrchestrator.Update(ctx, &model.Committee{CommitteeBase: *committee}, revision, false); err != nil {
		slog.ErrorContext(ctx, "failed to update committee total_members",
			"committee_uid", committeeUID, "error", err)
		return err
	}

	return nil
}

// NewMessageHandlerOrchestrator creates a new message handler orchestrator using the option pattern
func NewMessageHandlerOrchestrator(opts ...messageHandlerOrchestratorOption) port.MessageHandler {
	m := &messageHandlerOrchestrator{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

const committeeEmailSendTimeout = 5 * time.Second

// HandleCommitteeMemberCreated handles committee_member.created events and sends
// a notification email to the newly added member. Best-effort: send errors are logged, not returned.
func (m *messageHandlerOrchestrator) HandleCommitteeMemberCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_member.created event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_member.created event has unexpected data shape", "error", err)
		return nil, nil
	}

	var member model.CommitteeMember
	if err := json.Unmarshal(raw, &member); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeMember from event data", "error", err)
		return nil, nil
	}

	if member.Email == "" {
		slog.WarnContext(ctx, "skipping member notification — no email address",
			"committee_uid", member.CommitteeUID, "username", member.Username)
		return nil, nil
	}

	if m.emailSender == nil {
		slog.WarnContext(ctx, "email sender not configured — skipping member notification")
		return nil, nil
	}

	recipientName := strings.TrimSpace(member.FirstName + " " + member.LastName)
	if recipientName == "" {
		recipientName = member.Username
	}
	if recipientName == "" {
		recipientName = member.Email
	}

	roleDisplay := member.Role.Name
	if roleDisplay == "" {
		roleDisplay = "Member"
	}

	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, member.CommitteeUID)

	subject, html, text, err := emailsvc.RenderCommitteeRoleNotification(emailsvc.CommitteeRoleNotificationData{
		RecipientName: recipientName,
		CommitteeName: member.CommitteeName,
		Role:          roleDisplay,
		CommitteeURL:  committeeURL,
		InviterName:   "A committee administrator",
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to render member notification email template",
			"error", err, "committee_uid", member.CommitteeUID)
		return nil, nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)
	g.Go(func() error {
		sendCtx, cancel := context.WithTimeout(gctx, committeeEmailSendTimeout)
		defer cancel()
		if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
			To:      member.Email,
			Subject: subject,
			HTML:    html,
			Text:    text,
		}); sendErr != nil {
			slog.WarnContext(gctx, "failed to send member notification email",
				"error", sendErr, "committee_uid", member.CommitteeUID, "to", member.Email)
		} else {
			slog.DebugContext(gctx, "sent member notification email",
				"committee_uid", member.CommitteeUID, "to", member.Email)
		}
		return nil
	})
	_ = g.Wait()

	return nil, nil
}

// HandleCommitteeSettingsUpdated handles committee_settings.updated events and sends
// notification emails to Writers or Auditors newly added in the updated settings.
// Best-effort: send errors are logged, not returned.
func (m *messageHandlerOrchestrator) HandleCommitteeSettingsUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_settings.updated event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_settings.updated event has unexpected data shape", "error", err)
		return nil, nil
	}

	var data model.CommitteeSettingsUpdateEventData
	if err := json.Unmarshal(raw, &data); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeSettingsUpdateEventData from event", "error", err)
		return nil, nil
	}

	if m.emailSender == nil {
		slog.WarnContext(ctx, "email sender not configured — skipping settings notification")
		return nil, nil
	}

	// Build a deduplicated list of (user, role) pairs. Writers take precedence
	// if a user appears in both lists — they get a single email with the higher role.
	type notification struct {
		user model.CommitteeUser
		role string
	}
	seen := make(map[string]bool)
	var notifs []notification
	for _, u := range diffNewCommitteeUsers(data.OldSettings.GetWriters(), data.Settings.GetWriters()) {
		if !seen[u.Username] {
			seen[u.Username] = true
			notifs = append(notifs, notification{user: u, role: "Writer"})
		}
	}
	for _, u := range diffNewCommitteeUsers(data.OldSettings.GetAuditors(), data.Settings.GetAuditors()) {
		if !seen[u.Username] {
			seen[u.Username] = true
			notifs = append(notifs, notification{user: u, role: "Auditor"})
		}
	}

	if len(notifs) == 0 {
		slog.DebugContext(ctx, "no new writers/auditors — skipping settings notification",
			"committee_uid", data.CommitteeUID)
		return nil, nil
	}

	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, data.CommitteeUID)

	resolveCtx, resolveCancel := context.WithTimeout(ctx, committeeEmailSendTimeout)
	inviterName := m.resolveDisplayName(resolveCtx, data.UpdatedBy)
	resolveCancel()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, n := range notifs {
		if n.user.Email == "" && m.userReader != nil && n.user.Username != "" {
			lookupCtx, lookupCancel := context.WithTimeout(ctx, committeeEmailSendTimeout)
			emails, lookupErr := m.userReader.EmailsByPrincipal(lookupCtx, n.user.Username)
			lookupCancel()
			if lookupErr == nil && emails != nil && emails.PrimaryEmail != "" {
				n.user.Email = emails.PrimaryEmail
			}
		}
		if n.user.Email == "" {
			slog.WarnContext(ctx, "skipping settings notification — user has no email address",
				"committee_uid", data.CommitteeUID, "username", n.user.Username)
			continue
		}

		g.Go(func() error {
			u, role := n.user, n.role
			recipientName := u.Name
			if recipientName == "" {
				recipientName = u.Username
			}
			if recipientName == "" {
				recipientName = u.Email
			}

			subject, html, text, renderErr := emailsvc.RenderCommitteeRoleNotification(emailsvc.CommitteeRoleNotificationData{
				RecipientName: recipientName,
				CommitteeName: data.CommitteeName,
				Role:          role,
				CommitteeURL:  committeeURL,
				InviterName:   inviterName,
			})
			if renderErr != nil {
				slog.WarnContext(gctx, "failed to render settings notification email",
					"error", renderErr, "committee_uid", data.CommitteeUID)
				return nil
			}

			sendCtx, cancel := context.WithTimeout(gctx, committeeEmailSendTimeout)
			defer cancel()
			if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      u.Email,
				Subject: subject,
				HTML:    html,
				Text:    text,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send settings notification email",
					"error", sendErr, "committee_uid", data.CommitteeUID, "to", u.Email)
			} else {
				slog.DebugContext(gctx, "sent settings notification email",
					"committee_uid", data.CommitteeUID, "to", u.Email)
			}
			return nil
		})
	}
	_ = g.Wait()

	return nil, nil
}

// diffNewCommitteeUsers returns users in newList whose username is absent from oldList.
func diffNewCommitteeUsers(oldList, newList []model.CommitteeUser) []model.CommitteeUser {
	oldKeys := make(map[string]bool, len(oldList))
	for _, u := range oldList {
		if u.Username != "" {
			oldKeys[u.Username] = true
		}
	}
	var added []model.CommitteeUser
	for _, u := range newList {
		if !oldKeys[u.Username] {
			added = append(added, u)
		}
	}
	return added
}

// buildCommitteeURL returns a deep link directly to the committee page.
func buildCommitteeURL(baseURL, committeeUID string) string {
	return strings.TrimRight(baseURL, "/") + "/project/groups/" + committeeUID
}

// resolveDisplayName looks up the display name for the given principal via the user reader.
// Returns "A committee administrator" if the lookup fails or the metadata has no name.
func (m *messageHandlerOrchestrator) resolveDisplayName(ctx context.Context, principal string) string {
	if principal != "" && m.userReader != nil {
		if meta, err := m.userReader.UserMetadataByPrincipal(ctx, principal); err == nil && meta != nil {
			if meta.Name != "" {
				return meta.Name
			}
			if full := strings.TrimSpace(meta.GivenName + " " + meta.FamilyName); full != "" {
				return full
			}
		}
	}
	return "A committee administrator"
}
