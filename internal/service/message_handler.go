// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
)

// messageHandlerOrchestrator orchestrates the message handling process
type messageHandlerOrchestrator struct {
	committeeReader CommitteeReader
}

// messageHandlerOrchestratorOption defines a function type for setting options
type messageHandlerOrchestratorOption func(*messageHandlerOrchestrator)

// WithCommitteeReaderForMessageHandler sets the committee reader for message handler
func WithCommitteeReaderForMessageHandler(reader CommitteeReader) messageHandlerOrchestratorOption {
	return func(m *messageHandlerOrchestrator) {
		m.committeeReader = reader
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

// NewMessageHandlerOrchestrator creates a new message handler orchestrator using the option pattern
func NewMessageHandlerOrchestrator(opts ...messageHandlerOrchestratorOption) port.MessageHandler {
	m := &messageHandlerOrchestrator{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
