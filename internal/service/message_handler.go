// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
)

// messageHandlerOrchestrator orchestrates the message handling process
type messageHandlerOrchestrator struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	committeeWriter             port.CommitteeWriter
	committeePublisher          port.CommitteePublisher
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

// HandleCommitteeGetAttribute handles the retrieval of a specific attribute from the committee
func (m *messageHandlerOrchestrator) HandleCommitteeGetAttribute(ctx context.Context, msg port.TransportMessenger, attribute string) ([]byte, error) {

	// Parse message data to extract committee UID
	uid := string(msg.Data())

	ctx = log.AppendCtx(ctx, slog.String("committee_uid", uid))
	ctx = log.AppendCtx(ctx, slog.String("attribute", attribute))
	slog.DebugContext(ctx, "committee get name request")

	// Validate that the committee ID is a valid UUID.
	_, err := uuid.Parse(uid)
	if err != nil {
		return nil, err
	}

	// Use the committee reader to get the committee base information
	committee, _, err := m.committeeReader.GetBase(ctx, uid)
	if err != nil {
		return nil, err
	}

	value, ok := fields.LookupByTag(committee, "json", attribute)
	if !ok {
		return nil, errors.NewNotFound(fmt.Sprintf("attribute %s not found in committee %s", attribute, uid))
	}

	strValue, ok := value.(string)
	if !ok {
		return nil, errors.NewValidation(fmt.Sprintf("attribute %s value is not a string", attribute))
	}

	return []byte(strValue), nil
}

// HandleCommitteeListMembers handles the retrieval of all members for a committee
func (m *messageHandlerOrchestrator) HandleCommitteeListMembers(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {

	// Parse message data to extract committee UID
	uid := string(msg.Data())

	ctx = log.AppendCtx(ctx, slog.String("committee_uid", uid))
	slog.DebugContext(ctx, "committee list members request")

	// Validate that the committee ID is a valid UUID.
	_, err := uuid.Parse(uid)
	if err != nil {
		return nil, err
	}

	// Check if the committee exists first
	_, _, err = m.committeeReader.GetBase(ctx, uid)
	if err != nil {
		return nil, err
	}

	// Get all members for the committee
	members, err := m.committeeReader.ListMembers(ctx, uid)
	if err != nil {
		return nil, err
	}

	// Marshal the members to JSON
	membersJSON, err := json.Marshal(members)
	if err != nil {
		return nil, errors.NewUnexpected("failed to marshal committee members", err)
	}

	slog.DebugContext(ctx, "committee list members response", "member_count", len(members))

	return membersJSON, nil
}

// HandleCommitteeMailingListChanged processes a CommitteeMailingListChangedEvent from mailing-list-api.
// It updates the committee's has_mailing_list flag in KV and re-indexes the committee if the flag changed.
func (m *messageHandlerOrchestrator) HandleCommitteeMailingListChanged(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeMailingListChangedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
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
		return nil, err
	}
	indexerMsg.IndexingConfig = buildCommitteeIndexingConfig(fullCommittee)

	if err := m.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, indexerMsg, false); err != nil {
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
	ctx = log.AppendCtx(ctx, slog.String("committee_uid", committeeUID))

	slog.DebugContext(ctx, "starting total_members sync", "subject", subject)

	members, err := m.committeeReader.ListMembers(ctx, committeeUID)
	if err != nil {
		return err
	}
	actualCount := len(members)

	committee, revision, err := m.committeeReader.GetBase(ctx, committeeUID)
	if err != nil {
		return err
	}

	if committee.TotalMembers == actualCount {
		slog.DebugContext(ctx, "total_members already correct — skipping update", "total_members", actualCount)
		return nil
	}

	slog.DebugContext(ctx, "updating total_members counter",
		"previous", committee.TotalMembers,
		"actual", actualCount,
	)

	committee.TotalMembers = actualCount

	if _, err := m.committeeWriterOrchestrator.Update(ctx, &model.Committee{CommitteeBase: *committee}, revision, false); err != nil {
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
