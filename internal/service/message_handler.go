// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
)

// messageHandlerOrchestrator orchestrates the message handling process
type messageHandlerOrchestrator struct {
	committeeReader    CommitteeReader
	committeeWriter    port.CommitteeWriter
	committeePublisher port.CommitteePublisher
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

// NewMessageHandlerOrchestrator creates a new message handler orchestrator using the option pattern
func NewMessageHandlerOrchestrator(opts ...messageHandlerOrchestratorOption) port.MessageHandler {
	m := &messageHandlerOrchestrator{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
