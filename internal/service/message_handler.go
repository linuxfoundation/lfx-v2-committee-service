// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	emailsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service/email"
	committeeapi "github.com/linuxfoundation/lfx-v2-committee-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	fgaconstants "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/constants"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
	"golang.org/x/sync/errgroup"
)

// messageHandlerOrchestrator orchestrates the message handling process
type messageHandlerOrchestrator struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	committeeWriter             port.CommitteeWriter
	committeePublisher          port.CommitteePublisher
	emailSender                 port.EmailSender
	inviteSender                port.InviteSender
	userReader                  port.UserReader
	projectReader               port.ProjectReader
	linkReader                  port.CommitteeLinkReader
	lfxSelfServeBaseURL         string
	weeklyBriefGenerator        GroupWeeklyBriefGenerator
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

// WithInviteSenderForMessageHandler sets the invite sender for non-LFID users.
func WithInviteSenderForMessageHandler(sender port.InviteSender) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.inviteSender = sender
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

// WithProjectReaderForMessageHandler sets the project reader used for the project-writers fallback
// in application submitted notifications.
func WithProjectReaderForMessageHandler(reader port.ProjectReader) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.projectReader = reader
	}
}

// WithLinkReaderForMessageHandler sets the link reader used to resolve folder names in document/link notifications.
func WithLinkReaderForMessageHandler(reader port.CommitteeLinkReader) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.linkReader = reader
	}
}

// WithGroupWeeklyBriefGeneratorForMessageHandler sets the generator used to
// fulfill async weekly-brief generation requests.
func WithGroupWeeklyBriefGeneratorForMessageHandler(generator GroupWeeklyBriefGenerator) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.weeklyBriefGenerator = generator
	}
}

// HandleGenerateWeeklyBriefRequested reacts to generate-requested stream events.
// It decodes the request and runs the async Fulfill phase (source gather → LLM →
// finalize). The caller (infrastructure layer) owns ACK/NAK: a nil return ACKs,
// a non-nil return NAKs for retry with backoff.
func (m *messageHandlerOrchestrator) HandleGenerateWeeklyBriefRequested(ctx context.Context, msg port.StreamMessenger) error {
	if m.weeklyBriefGenerator == nil {
		return errors.NewValidation("weekly brief generator is required for handling generate-requested events")
	}

	subject := msg.Subject()
	if subject != constants.GenerateWeeklyBriefRequestedSubject {
		slog.DebugContext(ctx, "stream message subject not relevant for weekly-brief generate — skipping",
			"subject", subject,
		)
		return nil
	}

	var event GenerateWeeklyBriefRequestedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		// Undecodable payload — discarding (ACK) rather than retrying forever.
		slog.ErrorContext(ctx, "failed to unmarshal GenerateWeeklyBriefRequestedEvent — discarding", "error", err)
		return nil
	}
	if event.CommitteeUID == "" {
		slog.WarnContext(ctx, "generate-requested event missing committee_uid — discarding")
		return nil
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")
	slog.DebugContext(ctx, "fulfilling weekly-brief generation",
		"committee_uid", event.CommitteeUID,
		"force", event.Force,
	)

	return m.weeklyBriefGenerator.Fulfill(ctx, GroupWeeklyBriefGenerateInput{
		CommitteeUID:  event.CommitteeUID,
		CommitteeName: event.CommitteeName,
		ProjectName:   event.ProjectName,
		Force:         event.Force,
		Now:           event.RequestedAt,
	})
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

// HandleCommitteeGetProject resolves a committee UID to its owning project UID.
// The request payload must be a JSON-encoded GetCommitteeProjectRequest.
// On success it returns a JSON-encoded GetCommitteeProjectResponse with ProjectUID set.
// When the committee does not exist it returns a JSON-encoded GetCommitteeProjectResponse
// with Error set to "not found" (successful reply, not a Go error) so the NATS router
// sends the structured payload back to the caller rather than the generic error envelope.
func (m *messageHandlerOrchestrator) HandleCommitteeGetProject(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var req committeeapi.GetCommitteeProjectRequest
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal get_project request", "error", err)
		return nil, errors.NewValidation("invalid get_project request payload")
	}

	slog.DebugContext(ctx, "committee get project request", "committee_uid", req.CommitteeUID)

	if _, err := uuid.Parse(req.CommitteeUID); err != nil {
		slog.ErrorContext(ctx, "invalid committee UID in get_project request", "error", err, "committee_uid", req.CommitteeUID)
		return nil, errors.NewValidation("invalid committee UID", err)
	}

	committee, _, err := m.committeeReader.GetBase(ctx, req.CommitteeUID)
	if err != nil {
		var nf errors.NotFound
		if stderrors.As(err, &nf) {
			slog.DebugContext(ctx, "committee not found for get_project request", "committee_uid", req.CommitteeUID)
			return json.Marshal(committeeapi.GetCommitteeProjectResponse{Error: "not found"})
		}
		slog.ErrorContext(ctx, "failed to get committee base for get_project request",
			"error", err,
			"committee_uid", req.CommitteeUID,
		)
		return nil, err
	}

	slog.DebugContext(ctx, "committee get project response",
		"committee_uid", req.CommitteeUID,
		"project_uid", committee.ProjectUID,
	)

	return json.Marshal(committeeapi.GetCommitteeProjectResponse{ProjectUID: committee.ProjectUID})
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
	members, err := m.committeeReader.ListMembersByCommittee(ctx, uid)
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

	members, err := m.committeeReader.ListMembersByCommittee(ctx, data.CommitteeUID)
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

		if _, errUpdate := m.committeeWriterOrchestrator.UpdateMember(ctx, member, revision, false, false); errUpdate != nil {
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

	members, err := m.committeeReader.ListMembersByCommittee(ctx, committeeUID)
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

const committeeNotificationTimeout = 5 * time.Second

// HandleCommitteeMemberCreated handles committee_member.created events and notifies
// the newly added member. Users with an LFID (Username present) receive a direct
// notification email. Users without an LFID receive an invite via the invite service.
// Best-effort: send errors are logged, not returned.
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

	// Decode the created-event payload once into the typed wrapper so the
	// request-scoped skip_notification flag is parsed alongside the member. On a
	// malformed payload we fail safe by suppressing the notification rather than
	// defaulting to send.
	var created model.CommitteeMemberCreatedEventData
	if err := json.Unmarshal(raw, &created); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeMemberCreatedEventData from event data — suppressing notification", "error", err)
		return nil, nil
	}
	if created.CommitteeMember == nil {
		slog.WarnContext(ctx, "committee_member.created event missing member payload — suppressing notification")
		return nil, nil
	}
	member := *created.CommitteeMember

	// Request-scoped opt-out: when the member was added with skip_notification set,
	// suppress both the invite and the direct notification email.
	if created.SkipNotification {
		slog.DebugContext(ctx, "skipping member notification — skip_notification flag set",
			"committee_uid", member.CommitteeUID)
		return nil, nil
	}

	if member.Email == "" {
		slog.WarnContext(ctx, "skipping member notification — no email address",
			"committee_uid", member.CommitteeUID, "username", redaction.Redact(member.Username))
		return nil, nil
	}

	recipientName := strings.TrimSpace(member.FirstName + " " + member.LastName)
	if recipientName == "" {
		recipientName = member.Username
	}
	if recipientName == "" {
		recipientName = member.Email
	}

	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, member.CommitteeUID)

	if member.Username == "" {
		// No LFID — route through the invite service so the user must create an
		// account before gaining committee access.
		_ = m.sendMemberInvite(ctx, &member, recipientName, committeeURL)
		return nil, nil
	}

	// LFID present — send a direct notification email.
	if m.emailSender == nil {
		slog.DebugContext(ctx, "email sender not configured — skipping member notification")
		return nil, nil
	}

	subject, html, text, err := emailsvc.RenderCommitteeRoleNotification(emailsvc.CommitteeRoleNotificationData{
		RecipientName: recipientName,
		CommitteeName: member.CommitteeName,
		Role:          "Member",
		CommitteeURL:  committeeURL,
		InviterName:   "A committee administrator",
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to render member notification email template",
			"error", err, "committee_uid", member.CommitteeUID)
		return nil, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
		To:      member.Email,
		Subject: subject,
		HTML:    html,
		Text:    text,
	}); sendErr != nil {
		slog.WarnContext(ctx, "failed to send member notification email",
			"error", sendErr, "committee_uid", member.CommitteeUID)
	} else {
		slog.DebugContext(ctx, "sent member notification email",
			"committee_uid", member.CommitteeUID)
	}

	return nil, nil
}

// sendMemberInvite sends an invite request for a new committee member who does not
// yet have an LFID. Best-effort: logs failures internally; callers may ignore the returned error.
func (m *messageHandlerOrchestrator) sendMemberInvite(ctx context.Context, member *model.CommitteeMember, recipientName, deepLinkURL string) error {
	if m.inviteSender == nil {
		slog.DebugContext(ctx, "invite sender not configured — skipping member invite",
			"committee_uid", member.CommitteeUID)
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	result, err := m.inviteSender.SendInvite(sendCtx, inviteapi.SendInviteRequest{
		Recipient: &inviteapi.Recipient{
			Email: strings.TrimSpace(member.Email),
			Name:  recipientName,
		},
		Inviter: &inviteapi.Inviter{
			Name: "A committee administrator",
		},
		Resource: &inviteapi.Resource{
			UID:  member.CommitteeUID,
			Name: member.CommitteeName,
			Type: "group",
		},
		Role:      string(inviteapi.InviteRoleMember),
		ReturnURL: deepLinkURL,
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to send member invite request",
			"error", err, "committee_uid", member.CommitteeUID)
		return err
	}

	slog.DebugContext(ctx, "sent member invite request",
		"committee_uid", member.CommitteeUID, "invite_uid", result.InviteUID)
	return nil
}

// HandleCommitteeSettingsUpdated handles committee_settings.updated events and notifies
// Writers or Auditors newly added in the updated settings. Users with an LFID (Username
// present) receive a direct notification email. Users without an LFID receive an invite
// via the invite service. Best-effort: send errors are logged, not returned.
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

	// Classify every user that appears in old or new settings into one of:
	// added (newly appeared), updated (role-set changed), removed (fully gone).
	changes := classifyCommitteeUsers(data.OldSettings, data.Settings)
	if len(changes) == 0 {
		slog.DebugContext(ctx, "no writer/auditor changes — skipping settings notification",
			"committee_uid", data.CommitteeUID)
		return nil, nil
	}

	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, data.CommitteeUID)

	resolveCtx, resolveCancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	inviterName := m.resolveDisplayName(resolveCtx, data.UpdatedBy)
	resolveCancel()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, c := range changes {
		g.Go(func() error {
			u, kind, oldRoles, newRoles := c.user, c.kind, c.oldRoles, c.newRoles

			// Removed non-LF users get no notification.
			if kind == roleChangeKindRemoved && u.Username == "" {
				slog.DebugContext(gctx, "skipping removal notification — non-LF user",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			if u.Email == "" {
				slog.WarnContext(gctx, "skipping settings notification — user has no email address",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			recipientName := u.Name
			if recipientName == "" {
				recipientName = u.Username
			}
			if recipientName == "" {
				recipientName = u.Email
			}

			// Skip "added" notification if this user was previously an invited (email-only) entry in
			// the old settings — they're being promoted from non-LFID to LFID via invite acceptance,
			// not freshly added. They already received the invite email; a second email would be
			// confusing and redundant.
			if kind == roleChangeKindAdded && u.Email != "" && wasInvitedInOldSettings(u.Email, data.OldSettings) {
				slog.DebugContext(gctx, "skipping notification — user promoted from invite to LFID",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			if u.Username == "" {
				// No LFID — added/updated paths go through the invite service; removed was handled above.
				if m.inviteSender == nil {
					slog.DebugContext(gctx, "invite sender not configured — skipping settings invite",
						"committee_uid", data.CommitteeUID)
					return nil
				}
				// Skip re-invite when effective access is unchanged (e.g. gaining Auditor on top of Writer).
				if kind == roleChangeKindUpdated && effectiveRoleUnchanged(oldRoles, newRoles) {
					slog.DebugContext(gctx, "skipping non-LF invite — effective role unchanged",
						"committee_uid", data.CommitteeUID)
					return nil
				}
				// Use the highest new role for the invite (Writer > Auditor for access level).
				inviteRole := mapRoleToInviteRole(highestRole(newRoles))
				inviteCtx, inviteCancel := context.WithTimeout(gctx, committeeNotificationTimeout)
				result, inviteErr := m.inviteSender.SendInvite(inviteCtx, inviteapi.SendInviteRequest{
					Recipient: &inviteapi.Recipient{
						Email: strings.TrimSpace(u.Email),
						Name:  recipientName,
					},
					Inviter: &inviteapi.Inviter{
						Name: inviterName,
					},
					Resource: &inviteapi.Resource{
						UID:  data.CommitteeUID,
						Name: data.CommitteeName,
						Type: "group",
					},
					Role:      inviteRole,
					ReturnURL: committeeURL,
				})
				inviteCancel()
				if inviteErr != nil {
					slog.WarnContext(gctx, "failed to send settings invite request",
						"error", inviteErr, "committee_uid", data.CommitteeUID)
					return nil
				}

				slog.DebugContext(gctx, "sent settings invite request",
					"committee_uid", data.CommitteeUID, "invite_uid", result.InviteUID)

				return nil
			}

			// LFID present — send a direct notification email.
			if m.emailSender == nil {
				slog.DebugContext(gctx, "email sender not configured — skipping settings notification",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			var emailSubject, emailHTML, emailText string
			var renderErr error

			switch kind {
			case roleChangeKindAdded:
				// Newly added — use the original "added you as a <role>" email.
				// newRoles may contain multiple roles; display only the highest-privilege one.
				roleDisplay := emailsvc.CommitteeRoleDisplayName(highestRole(newRoles))
				emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeRoleNotification(emailsvc.CommitteeRoleNotificationData{
					RecipientName: recipientName,
					CommitteeName: data.CommitteeName,
					Role:          roleDisplay,
					CommitteeURL:  committeeURL,
					InviterName:   inviterName,
				})
			case roleChangeKindUpdated:
				// Skip email when effective display role is unchanged (e.g. Auditor gained on top of Writer).
				if effectiveRoleUnchanged(oldRoles, newRoles) {
					slog.DebugContext(gctx, "skipping role-updated email — effective role unchanged",
						"committee_uid", data.CommitteeUID)
					return nil
				}
				emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeRoleUpdated(emailsvc.CommitteeRoleUpdatedData{
					RecipientName: recipientName,
					CommitteeName: data.CommitteeName,
					OldRoles:      oldRoles,
					NewRoles:      newRoles,
					CommitteeURL:  committeeURL,
					InviterName:   inviterName,
				})
			case roleChangeKindRemoved:
				emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeRoleRemoved(emailsvc.CommitteeRoleRemovedData{
					RecipientName: recipientName,
					CommitteeName: data.CommitteeName,
					OldRoles:      oldRoles,
					InviterName:   inviterName,
				})
			}

			if renderErr != nil {
				slog.WarnContext(gctx, "failed to render settings notification email",
					"error", renderErr, "committee_uid", data.CommitteeUID, "kind", kind)
				return nil
			}

			sendCtx, cancel := context.WithTimeout(gctx, committeeNotificationTimeout)
			defer cancel()
			if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      u.Email,
				Subject: emailSubject,
				HTML:    emailHTML,
				Text:    emailText,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send settings notification email",
					"error", sendErr, "committee_uid", data.CommitteeUID, "kind", kind)
			} else {
				slog.DebugContext(gctx, "sent settings notification email",
					"committee_uid", data.CommitteeUID, "kind", kind)
			}
			return nil
		})
	}
	_ = g.Wait()

	return nil, nil
}

// HandleInviteAccepted processes an invite acceptance event published by the invite service.
// It scans all committees for email-only records matching the recipient email and enriches
// them with the accepted user's LFID (username set). Writers, Auditors, and Members are all
// enriched regardless of which invite role triggered acceptance.
//
// TODO(LFXV2-2238): replace the full-scan with an email → [committee_uid] index lookup so we avoid
// loading every committee's settings and listing all members on each acceptance event.
func (m *messageHandlerOrchestrator) HandleInviteAccepted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event inviteapi.InviteServiceAcceptedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal invite_accepted event", "error", err)
		return nil, nil
	}

	if event.UID == "" || event.AcceptedBy == "" || strings.TrimSpace(event.Recipient.Email) == "" {
		slog.WarnContext(ctx, "invite_accepted event missing required fields — discarding",
			"invite_uid", event.UID, "accepted_by", redaction.Redact(event.AcceptedBy),
			"recipient_email", redaction.RedactEmail(event.Recipient.Email))
		return nil, nil
	}

	if m.committeeWriterOrchestrator == nil {
		slog.WarnContext(ctx, "committee writer orchestrator not available — cannot persist invite enrichment",
			"invite_uid", event.UID)
		return nil, nil
	}

	// NATS event handlers have no inbound HTTP request and therefore no JWT in ctx.
	// Inject a service-identity bearer so UpdateSettings' downstream calls (FGA, indexer)
	// carry a recognized auth token. The header is propagated into context by
	// internal/middleware/authorization.go (no allow-listing — it copies whatever header
	// is present; trust is enforced by the downstream FGA/indexer services).
	writeCtx := context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	normalizedEmail := strings.ToLower(strings.TrimSpace(event.Recipient.Email))

	// Full scan (LFXV2-2238): per committee, load settings and list members to reconcile email-only records.
	allUIDs, listErr := m.committeeReader.ListAllUIDs(ctx)
	if listErr != nil {
		slog.WarnContext(ctx, "failed to list committee UIDs for invite reconciliation",
			"error", listErr, "invite_uid", event.UID)
		return nil, nil
	}

	// Resolve the invitee's name once — avoids one round-trip per committee in the full scan.
	// Precedence: auth-service user_metadata.read (meta.Name, or GivenName+FamilyName) → Recipient.Name from payload.
	resolveCtx, resolveCancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	firstName, lastName, fullName := m.resolveInvitedName(resolveCtx, event.AcceptedBy, event.Recipient.Name)
	resolveCancel()

	// Resolve the committee UID this LFID invite was issued for (carried in the Resource field).
	inviteResourceUID := event.Resource.UID

	// Pre-fetch all invites once and filter to the accepting email. This avoids an O(committees × invites)
	// full-bucket scan that would result from calling ListInvites per committee inside the loop.
	// The result is partitioned per committee inside publishInviteeFGAForCommittee.
	// Guard: skip the scan entirely when FGA publishing is disabled (committeePublisher == nil).
	var invitesByCommittee map[string][]*model.CommitteeInvite
	if m.committeePublisher != nil {
		invitesByCommittee = m.fetchInvitesByEmail(ctx, normalizedEmail)
	}

	// Accept the pending committee invite for the specific committee this LFID invite was issued for.
	// We only accept the one the user explicitly acted on — other pending committee invites remain
	// pending because we don't know whether the user wants to accept them.
	// FGA invitee tuples are still published for all committees so the user can see all their invites.
	m.acceptPendingCommitteeInvites(ctx, writeCtx, event.AcceptedBy, inviteResourceUID, invitesByCommittee)

	var g errgroup.Group
	g.SetLimit(10)
	for _, committeeUID := range allUIDs {
		uid := committeeUID
		g.Go(func() error {
			m.enrichInvitedUserInCommittee(ctx, inviteAcceptedEnrichment{
				writeCtx:        writeCtx,
				committeeUID:    uid,
				normalizedEmail: normalizedEmail,
				username:        event.AcceptedBy,
				inviteUID:       event.UID,
				firstName:       firstName,
				lastName:        lastName,
				fullName:        fullName,
			})
			return nil
		})
	}
	_ = g.Wait()

	return nil, nil
}

// inviteAcceptedEnrichment holds the per-event context threaded through the invite-accepted
// enrichment chain. committeeUID is set per-iteration of the full-committee scan.
// invitesByCommittee is keyed by committeeUID and contains only invites for the accepting email,
// pre-fetched once before the loop to avoid repeated full-bucket scans.
type inviteAcceptedEnrichment struct {
	writeCtx           context.Context
	committeeUID       string
	normalizedEmail    string
	username           string
	inviteUID          string
	firstName          string
	lastName           string
	fullName           string
	invitesByCommittee map[string][]*model.CommitteeInvite
}

// enrichInvitedUserInCommittee enriches every email-only Writers, Auditors, and Members record
// for e.normalizedEmail in the given committee. Invite role is ignored — acceptance always
// reconciles all resource data for the recipient email. FGA invitee tuples are published
// upfront in HandleInviteAccepted before this scan runs, so they are not repeated here.
func (m *messageHandlerOrchestrator) enrichInvitedUserInCommittee(ctx context.Context, e inviteAcceptedEnrichment) {
	m.enrichInvitedUserInCommitteeSettings(ctx, e)
	m.enrichInvitedUserInCommitteeMembers(ctx, e)
}

// fetchInvitesByEmail calls ListAllInvites once and returns a map of committeeUID → invites
// for invites whose InviteeEmail matches normalizedEmail. When the storage layer returns a
// partial result alongside a non-nil error (e.g. some individual reads failed), the successfully
// loaded invites are still used — only a total failure (nil slice) causes an early return.
func (m *messageHandlerOrchestrator) fetchInvitesByEmail(ctx context.Context, normalizedEmail string) map[string][]*model.CommitteeInvite {
	all, err := m.committeeReader.ListAllInvites(ctx)
	if err != nil {
		slog.WarnContext(ctx, "partial or total failure listing committee invites for FGA invitee grant",
			"error", err)
		if len(all) == 0 {
			return nil
		}
	}
	result := make(map[string][]*model.CommitteeInvite)
	for _, invite := range all {
		if invite == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(invite.InviteeEmail)) != normalizedEmail {
			continue
		}
		result[invite.CommitteeUID] = append(result[invite.CommitteeUID], invite)
	}
	return result
}

// publishInviteeFGAForCommittee publishes an FGA update_access message for every committee_invite
// for the accepting email in the given committee, regardless of invite status. Uses the pre-fetched
// invitesByCommittee map to avoid per-committee full-bucket scans.
func (m *messageHandlerOrchestrator) publishInviteeFGAForCommittee(ctx context.Context, e inviteAcceptedEnrichment) {
	if m.committeePublisher == nil {
		return
	}
	for _, invite := range e.invitesByCommittee[e.committeeUID] {
		if invite == nil {
			continue
		}
		msg := fgatypes.GenericFGAMessage{
			ObjectType: "committee_invite",
			Operation:  "update_access",
			Data: fgatypes.GenericAccessData{
				UID: invite.UID,
				References: map[string][]string{
					constants.RelationCommittee: {invite.CommitteeUID},
				},
				Relations: map[string][]string{
					constants.RelationInvitee: {e.username},
				},
			},
		}
		if pubErr := m.committeePublisher.Access(ctx, fgaconstants.GenericUpdateAccessSubject, msg, false); pubErr != nil {
			slog.WarnContext(ctx, "failed to publish FGA invitee grant for committee invite",
				"invite_uid", invite.UID, "committee_uid", e.committeeUID,
				"username", redaction.Redact(e.username), "error", pubErr)
		} else {
			slog.DebugContext(ctx, "published FGA invitee grant for committee invite",
				"invite_uid", invite.UID, "committee_uid", e.committeeUID,
				"username", redaction.Redact(e.username))
		}
	}
}

// acceptPendingCommitteeInvites publishes FGA invitee tuples for every committee this user has
// been invited to, and additionally accepts (status → "accepted") the pending invite for
// targetCommitteeUID — the specific committee the accepted LFID invite was issued for.
// Invite acceptance is scoped to one committee because accepting an LFID invite is consent
// for that resource only; invites to other committees remain pending for the user to decide.
// The FGA grant is issued for all committees so the user can see all their pending invites.
func (m *messageHandlerOrchestrator) acceptPendingCommitteeInvites(ctx, writeCtx context.Context, username, targetCommitteeUID string, invitesByCommittee map[string][]*model.CommitteeInvite) {
	if m.committeePublisher == nil {
		return
	}
	for committeeUID, invites := range invitesByCommittee {
		for _, cached := range invites {
			if cached == nil {
				continue
			}
			// Accept the invite only for the specific committee the LFID invite was issued for.
			// For all other committees, only the FGA tuple is published (once per committee, below).
			if m.committeeWriter != nil && m.committeeWriterOrchestrator != nil && committeeUID == targetCommitteeUID {
				invite, revision, err := m.committeeReader.GetInvite(ctx, cached.UID)
				switch {
				case err != nil:
					slog.WarnContext(ctx, "failed to get committee invite for acceptance — granting FGA only",
						"invite_uid", cached.UID, "committee_uid", committeeUID, "error", err)
				case invite.Status == "pending":
					// Create the committee member first so that if it fails the invite stays
					// pending and the user can retry. Mirrors the AcceptInvite HTTP endpoint flow.
					member := &model.CommitteeMember{
						CommitteeMemberBase: model.CommitteeMemberBase{
							CommitteeUID: invite.CommitteeUID,
							Email:        invite.InviteeEmail,
							Username:     username,
							Role:         model.CommitteeMemberRole{Name: invite.Role},
							Status:       "Active",
						},
					}
					if _, createErr := m.committeeWriterOrchestrator.CreateMember(writeCtx, member, false, false); createErr != nil {
						var conflictErr errors.Conflict
						if !stderrors.As(createErr, &conflictErr) {
							slog.WarnContext(ctx, "failed to create committee member during invite acceptance",
								"invite_uid", invite.UID, "committee_uid", committeeUID,
								"username", redaction.Redact(username), "error", createErr)
						} else {
							slog.DebugContext(ctx, "committee member already exists during invite acceptance — skipping create",
								"invite_uid", invite.UID, "committee_uid", committeeUID)
						}
					}
					// Mark the invite accepted regardless of whether member creation succeeded (it may
					// already exist as a placeholder that was enriched by the enrichment scan).
					invite.Status = "accepted"
					if updateErr := m.committeeWriter.UpdateInvite(writeCtx, invite, revision); updateErr != nil {
						slog.WarnContext(ctx, "failed to accept committee invite",
							"invite_uid", invite.UID, "committee_uid", committeeUID,
							"username", redaction.Redact(username), "error", updateErr)
					} else {
						slog.InfoContext(ctx, "accepted committee invite via LFID acceptance",
							"invite_uid", invite.UID, "committee_uid", committeeUID,
							"username", redaction.Redact(username))
						m.publishInviteIndexerForHandler(writeCtx, invite)
					}
				default:
					slog.DebugContext(ctx, "committee invite already processed — granting FGA only",
						"invite_uid", invite.UID, "committee_uid", committeeUID, "status", invite.Status)
				}
			}
		}
		// Publish the FGA invitee tuple once per committee so the user can see the invite.
		m.publishInviteeFGAForCommittee(ctx, inviteAcceptedEnrichment{
			writeCtx: writeCtx, committeeUID: committeeUID, username: username,
			invitesByCommittee: invitesByCommittee,
		})
	}
}

// publishInviteIndexerForHandler builds and publishes an indexer updated message for a committee
// invite accepted via the NATS invite-accepted handler. It mirrors the service-layer
// publishInviteIndexerMessage but is scoped to the message handler where no HTTP request context exists.
func (m *messageHandlerOrchestrator) publishInviteIndexerForHandler(ctx context.Context, invite *model.CommitteeInvite) {
	tags := invite.Tags()
	public := false
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             invite.UID,
		AccessCheckObject:    fmt.Sprintf("committee_invite:%s", invite.UID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", invite.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", invite.CommitteeUID)},
		SortName:             invite.InviteeEmail,
		NameAndAliases:       []string{invite.InviteeEmail},
		Fulltext:             invite.InviteeEmail,
		Tags:                 tags,
		Public:               &public,
	}
	msg := model.CommitteeIndexerMessage{
		Action:         model.ActionUpdated,
		Tags:           tags,
		IndexingConfig: indexingConfig,
	}
	built, err := msg.Build(ctx, invite)
	if err != nil {
		slog.WarnContext(ctx, "failed to build invite indexer message for handler",
			"error", err, "invite_uid", invite.UID)
		return
	}
	if pubErr := m.committeePublisher.Indexer(ctx, constants.IndexCommitteeInviteSubject, built, false); pubErr != nil {
		slog.WarnContext(ctx, "failed to publish invite indexer message from handler",
			"error", pubErr, "invite_uid", invite.UID)
	}
}

// enrichInvitedUserInCommitteeSettings enriches all email-only Writers and Auditors matching
// e.normalizedEmail in the given committee's settings with the accepted LFID and display name.
// It retries on revision conflicts.
func (m *messageHandlerOrchestrator) enrichInvitedUserInCommitteeSettings(ctx context.Context, e inviteAcceptedEnrichment) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		settings, revision, err := m.committeeReader.GetSettings(ctx, e.committeeUID)
		if err != nil {
			slog.WarnContext(ctx, "failed to get settings for invite enrichment",
				"error", err, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
			return
		}

		enriched := false
		for i := range settings.Writers {
			if enrichCommitteeUserIfEmailOnly(&settings.Writers[i], e.normalizedEmail, e.username, e.fullName) {
				enriched = true
			}
		}
		for i := range settings.Auditors {
			if enrichCommitteeUserIfEmailOnly(&settings.Auditors[i], e.normalizedEmail, e.username, e.fullName) {
				enriched = true
			}
		}

		if !enriched {
			return
		}

		if _, writeErr := m.committeeWriterOrchestrator.UpdateSettings(e.writeCtx, settings, revision, false); writeErr != nil {
			var conflictErr errors.Conflict
			if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(ctx, "revision conflict enriching invite — retrying",
					"attempt", attempt+1, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
				continue
			}
			slog.ErrorContext(ctx, "failed to update settings after invite acceptance",
				"error", writeErr, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
			return
		}

		slog.DebugContext(ctx, "invite accepted — enriched email-only Writers/Auditors with LFID",
			"committee_uid", e.committeeUID, "invite_uid", e.inviteUID, "username", redaction.Redact(e.username))
		return
	}
}

// enrichInvitedUserInCommitteeMembers enriches all email-only committee members matching
// e.normalizedEmail in the given committee with the accepted LFID and name.
func (m *messageHandlerOrchestrator) enrichInvitedUserInCommitteeMembers(ctx context.Context, e inviteAcceptedEnrichment) {
	members, err := m.committeeReader.ListMembersByCommittee(ctx, e.committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to list members for invite enrichment",
			"error", err, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
		return
	}

	for _, member := range members {
		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != e.normalizedEmail {
			continue
		}
		m.enrichInvitedCommitteeMember(ctx, e, member.UID)
	}
}

// enrichInvitedCommitteeMember enriches a single email-only member with the accepted LFID and name.
// Revision-conflict retries are handled here, not in enrichInvitedUserInCommitteeMembers.
// memberUID identifies the specific member within e.committeeUID.
func (m *messageHandlerOrchestrator) enrichInvitedCommitteeMember(ctx context.Context, e inviteAcceptedEnrichment, memberUID string) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		member, revision, err := m.committeeReader.GetMember(ctx, e.committeeUID, memberUID)
		if err != nil {
			slog.WarnContext(ctx, "failed to get member for invite enrichment",
				"error", err, "committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID)
			return
		}

		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != e.normalizedEmail {
			return
		}

		member.Username = e.username
		// Only fill name fields that are not already set — preserves any name stored at invite creation.
		if member.FirstName == "" && e.firstName != "" {
			member.FirstName = e.firstName
		}
		if member.LastName == "" && e.lastName != "" {
			member.LastName = e.lastName
		}

		updated, writeErr := m.committeeWriterOrchestrator.UpdateMember(e.writeCtx, member, revision, false, true)
		if writeErr != nil {
			var conflictErr errors.Conflict
			if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(ctx, "revision conflict enriching member — retrying",
					"attempt", attempt+1, "committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID)
				continue
			}
			slog.ErrorContext(ctx, "failed to update member after invite acceptance",
				"error", writeErr, "committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID)
			return
		}

		persistedUsername := e.username
		if updated != nil {
			persistedUsername = updated.Username
		}
		slog.DebugContext(ctx, "invite accepted — enriched email-only member with LFID",
			"committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID,
			"username", redaction.Redact(persistedUsername))
		return
	}
}

// enrichCommitteeUserIfEmailOnly sets username (and name when not already set) on a settings
// user when the entry is email-only and matches normalizedEmail. Returns true when the user
// was enriched.
func enrichCommitteeUserIfEmailOnly(user *model.CommitteeUser, normalizedEmail, username, fullName string) bool {
	if user.Username != "" || strings.ToLower(strings.TrimSpace(user.Email)) != normalizedEmail {
		return false
	}
	user.Username = username
	// Only fill the name when the record has none — preserves any name stored at invite creation.
	if user.Name == "" && fullName != "" {
		user.Name = fullName
	}
	return true
}

// resolveInvitedName returns (firstName, lastName, fullName) for the accepted invitee.
// Precedence: auth-service UserMetadataByPrincipal result (when it supplies any name) →
// payload Recipient.Name fallback. The payload is also used when metadata is non-nil but
// carries no name fields. All lookups are best-effort: a transport error logs a warning
// and triggers the fallback so the caller is never blocked.
func (m *messageHandlerOrchestrator) resolveInvitedName(ctx context.Context, principal, payloadName string) (firstName, lastName, fullName string) {
	if m.userReader != nil && principal != "" {
		meta, err := m.userReader.UserMetadataByPrincipal(ctx, principal)
		if err != nil {
			slog.WarnContext(ctx, "user metadata lookup failed during invite acceptance — falling back to payload name",
				"principal", redaction.Redact(principal), "error", err)
		} else if meta != nil {
			firstName = meta.GivenName
			lastName = meta.FamilyName
			fullName = meta.Name
			if fullName == "" {
				fullName = strings.TrimSpace(meta.GivenName + " " + meta.FamilyName)
			}
			if firstName == "" && lastName == "" && fullName != "" {
				firstName, lastName = splitName(fullName)
			}
			// Only return metadata-derived names when metadata actually supplied
			// at least one — a non-nil record with all empty fields falls through
			// to the payload fallback below.
			if firstName != "" || lastName != "" || fullName != "" {
				return firstName, lastName, fullName
			}
		}
	}

	// Metadata unavailable (lookup failed or returned nil) — fall back to payload.
	firstName, lastName = splitName(payloadName)
	fullName = strings.TrimSpace(payloadName)
	return firstName, lastName, fullName
}

// splitName splits a combined display name on the first space, returning (first, rest).
// Leading/trailing whitespace is trimmed from both parts.
func splitName(name string) (first, last string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}
	parts := strings.SplitN(name, " ", 2)
	first = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		last = strings.TrimSpace(parts[1])
	}
	return first, last
}

// mapRoleToInviteRole converts a committee settings role string to the invite
// service's role vocabulary.
//
// Mapping:
//   - Writer → Manage
//   - Auditor → View
//   - All other roles (unknown/future) → Manage (intentional: err on the side
//     of access rather than silently blocking a legitimate user).
func mapRoleToInviteRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "writer":
		return string(inviteapi.InviteRoleManage)
	case "auditor":
		return string(inviteapi.InviteRoleView)
	default:
		// Unknown roles fall through to Manage. Log so any new role added later
		// is immediately visible rather than silently inheriting elevated access.
		slog.Warn("mapRoleToInviteRole: unrecognised role, defaulting to Manage", "role", role)
		return string(inviteapi.InviteRoleManage)
	}
}

// committeeUserKey returns a stable, normalized identity key for a CommitteeUser.
// LFID users are keyed by Username; non-LFID users fall back to a normalized email.
// Returns "" when both fields are empty (user cannot be identified).
func committeeUserKey(u model.CommitteeUser) string {
	if username := strings.TrimSpace(u.Username); username != "" {
		return "username:" + strings.ToLower(username)
	}
	if email := strings.ToLower(strings.TrimSpace(u.Email)); email != "" {
		return "email:" + email
	}
	return ""
}

// diffNewCommitteeUsers returns users in newList that were not in oldList.
// LFID users are matched by Username; non-LFID users are matched by normalized Email.
func diffNewCommitteeUsers(oldList, newList []model.CommitteeUser) []model.CommitteeUser {
	oldKeys := make(map[string]bool, len(oldList))
	for _, u := range oldList {
		if key := committeeUserKey(u); key != "" {
			oldKeys[key] = true
		}
	}
	var added []model.CommitteeUser
	for _, u := range newList {
		key := committeeUserKey(u)
		if key == "" || oldKeys[key] {
			continue
		}
		added = append(added, u)
	}
	return added
}

// wasInvitedInOldSettings returns true if the given email was already present in old settings
// as an email-only (non-LFID, Username == "") entry — meaning the user was previously invited
// and is now being promoted. Used to suppress duplicate notification emails on LFID promotion.
func wasInvitedInOldSettings(email string, old *model.CommitteeSettings) bool {
	if old == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	for _, u := range old.GetWriters() {
		if u.Username == "" && strings.ToLower(strings.TrimSpace(u.Email)) == normalized {
			return true
		}
	}
	for _, u := range old.GetAuditors() {
		if u.Username == "" && strings.ToLower(strings.TrimSpace(u.Email)) == normalized {
			return true
		}
	}
	return false
}

// buildCommitteeURL returns a deep link directly to the committee page.
func buildCommitteeURL(baseURL, committeeUID string) string {
	return strings.TrimRight(baseURL, "/") + "/project/groups/" + committeeUID
}

// resolveDisplayName looks up the display name for the given principal via the user reader.
// Returns "A committee administrator" if the lookup fails or the metadata has no name.
func (m *messageHandlerOrchestrator) resolveDisplayName(ctx context.Context, principal string) string {
	if principal != "" && m.userReader != nil {
		meta, err := m.userReader.UserMetadataByPrincipal(ctx, principal)
		if err != nil {
			slog.WarnContext(ctx, "failed to look up inviter display name — using default",
				"error", err)
		} else if meta != nil {
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

// HandleCommitteeMemberDeleted sends a removal notification email when an LF committee
// member is deleted. Non-LF members (Username == "") receive nothing. Best-effort: send
// errors are logged, not returned.
func (m *messageHandlerOrchestrator) HandleCommitteeMemberDeleted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_member.deleted event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_member.deleted event has unexpected data shape", "error", err)
		return nil, nil
	}

	// Decode the deleted-event payload into the typed wrapper so the
	// request-scoped skip_notification flag is parsed alongside the member. On a
	// malformed payload we fail safe by suppressing the notification rather than
	// defaulting to send.
	var deleted model.CommitteeMemberDeletedEventData
	if err := json.Unmarshal(raw, &deleted); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeMemberDeletedEventData from committee_member.deleted event — suppressing notification", "error", err)
		return nil, nil
	}
	if deleted.CommitteeMember == nil {
		slog.WarnContext(ctx, "committee_member.deleted event missing member payload — suppressing notification")
		return nil, nil
	}
	if deleted.SkipNotification {
		slog.DebugContext(ctx, "skipping member-deleted notification — skip_notification flag set",
			"committee_uid", deleted.CommitteeUID)
		return nil, nil
	}
	member := *deleted.CommitteeMember

	if member.Username == "" {
		slog.DebugContext(ctx, "skipping member-deleted notification — non-LF user",
			"committee_uid", member.CommitteeUID)
		return nil, nil
	}

	if member.Email == "" {
		slog.WarnContext(ctx, "skipping member-deleted notification — no email address",
			"committee_uid", member.CommitteeUID, "username", redaction.Redact(member.Username))
		return nil, nil
	}

	if m.emailSender == nil {
		slog.DebugContext(ctx, "email sender not configured — skipping member-deleted notification")
		return nil, nil
	}

	recipientName := strings.TrimSpace(member.FirstName + " " + member.LastName)
	if recipientName == "" {
		recipientName = member.Username
	}
	if recipientName == "" {
		recipientName = member.Email
	}

	var oldRoleNames []string
	if member.Role.Name != "" {
		oldRoleNames = []string{member.Role.Name}
	}
	subject, html, text, renderErr := emailsvc.RenderCommitteeRoleRemoved(emailsvc.CommitteeRoleRemovedData{
		RecipientName: recipientName,
		CommitteeName: member.CommitteeName,
		OldRoles:      oldRoleNames,
		InviterName:   "A committee administrator",
	})
	if renderErr != nil {
		slog.WarnContext(ctx, "failed to render member-deleted notification email",
			"error", renderErr, "committee_uid", member.CommitteeUID)
		return nil, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
		To:      member.Email,
		Subject: subject,
		HTML:    html,
		Text:    text,
	}); sendErr != nil {
		slog.WarnContext(ctx, "failed to send member-deleted notification email",
			"error", sendErr, "committee_uid", member.CommitteeUID)
	} else {
		slog.DebugContext(ctx, "sent member-deleted notification email",
			"committee_uid", member.CommitteeUID)
	}

	return nil, nil
}

// roleChangeKind describes what changed for a user between old and new settings.
type roleChangeKind string

const (
	roleChangeKindAdded   roleChangeKind = "added"
	roleChangeKindUpdated roleChangeKind = "updated"
	roleChangeKindRemoved roleChangeKind = "removed"
)

// committeeUserRoleChange describes a single user whose role-set changed.
type committeeUserRoleChange struct {
	user     model.CommitteeUser
	kind     roleChangeKind
	oldRoles []string // sorted; empty when kind == added
	newRoles []string // sorted; empty when kind == removed
}

// classifyCommitteeUsers compares old and new settings and returns one entry per user
// whose role-set changed. Role-sets are compared as sorted slices so the caller can
// render current roles in alphabetical order.
func classifyCommitteeUsers(old, new *model.CommitteeSettings) []committeeUserRoleChange {
	buildRoleSet := func(s *model.CommitteeSettings) map[string]map[string]model.CommitteeUser {
		out := make(map[string]map[string]model.CommitteeUser)
		if s == nil {
			return out
		}
		for _, u := range s.GetWriters() {
			if key := committeeUserKey(u); key != "" {
				if out[key] == nil {
					out[key] = make(map[string]model.CommitteeUser)
				}
				out[key]["Writer"] = u
			}
		}
		for _, u := range s.GetAuditors() {
			if key := committeeUserKey(u); key != "" {
				if out[key] == nil {
					out[key] = make(map[string]model.CommitteeUser)
				}
				out[key]["Auditor"] = u
			}
		}
		return out
	}

	oldSet := buildRoleSet(old)
	newSet := buildRoleSet(new)

	// Collect all user keys that appear in either old or new.
	allKeys := make(map[string]bool)
	for k := range oldSet {
		allKeys[k] = true
	}
	for k := range newSet {
		allKeys[k] = true
	}

	sortedKeys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var changes []committeeUserRoleChange
	for _, key := range sortedKeys {
		oldRoles := oldSet[key]
		newRoleMap := newSet[key]

		hadRoles := len(oldRoles) > 0
		hasRoles := len(newRoleMap) > 0

		newRoleSlice := sortedRoles(newRoleMap)

		switch {
		case !hadRoles && hasRoles:
			u := anyUser(newRoleMap)
			changes = append(changes, committeeUserRoleChange{user: u, kind: roleChangeKindAdded, newRoles: newRoleSlice})
		case hadRoles && !hasRoles:
			oldRoleSlice := sortedRoles(oldRoles)
			u := anyUser(oldRoles)
			changes = append(changes, committeeUserRoleChange{user: u, kind: roleChangeKindRemoved, oldRoles: oldRoleSlice})
		case hadRoles && hasRoles:
			oldRoleSlice := sortedRoles(oldRoles)
			if !roleSlicesEqual(oldRoleSlice, newRoleSlice) {
				u := anyUser(newRoleMap)
				changes = append(changes, committeeUserRoleChange{user: u, kind: roleChangeKindUpdated, oldRoles: oldRoleSlice, newRoles: newRoleSlice})
			}
		}
	}
	return changes
}

// sortedRoles returns the role names from a map sorted alphabetically.
func sortedRoles(m map[string]model.CommitteeUser) []string {
	roles := make([]string, 0, len(m))
	for r := range m {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return roles
}

// anyUser returns the CommitteeUser from the first entry of a role map.
// Preference is given to the Writer entry so the caller gets the richer struct
// when both Writer and Auditor are present.
func anyUser(m map[string]model.CommitteeUser) model.CommitteeUser {
	if u, ok := m["Writer"]; ok {
		return u
	}
	for _, u := range m {
		return u
	}
	return model.CommitteeUser{}
}

// roleSlicesEqual returns true when two sorted string slices are identical.
func roleSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// effectiveRoleUnchanged returns true when the user-facing display roles derived from
// oldRoles and newRoles are identical — for example, gaining Auditor while already holding
// Writer produces no visible change (both collapse to ["Manage"]), so no email is needed.
func effectiveRoleUnchanged(oldRoles, newRoles []string) bool {
	oldDisplay := emailsvc.CommitteeRolesForDisplay(oldRoles)
	newDisplay := emailsvc.CommitteeRolesForDisplay(newRoles)
	sort.Strings(oldDisplay)
	sort.Strings(newDisplay)
	return roleSlicesEqual(oldDisplay, newDisplay)
}

// highestRole returns the single highest-privilege role from a slice.
// "Writer" is considered higher than "Auditor" (maps to InviteRoleManage).
// Returns the first element if no known role is found.
func highestRole(roles []string) string {
	for _, r := range roles {
		if strings.EqualFold(r, "Writer") {
			return r
		}
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return ""
}
