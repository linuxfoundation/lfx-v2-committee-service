// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type messagePublisher struct {
	client *NATSClient
}

// publish is a generic function that handles NATS message publishing
func (m *messagePublisher) publish(ctx context.Context, subject string, message any, messageType string) error {
	// Check if client is ready
	if err := m.client.IsReady(ctx); err != nil {
		slog.ErrorContext(ctx, "NATS client is not ready for publishing",
			"error", err,
			"subject", subject,
			"message_type", messageType,
		)
		return errors.NewServiceUnavailable("NATS client is not ready", err)
	}

	// Marshal message to JSON
	data, err := json.Marshal(message)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal message to JSON",
			"error", err,
			"subject", subject,
			"message_type", messageType,
		)
		return errors.NewUnexpected("failed to marshal message", err)
	}

	// Publish message
	if err := m.client.conn.Publish(subject, data); err != nil {
		slog.ErrorContext(ctx, "failed to publish message to NATS",
			"error", err,
			"subject", subject,
			"message_type", messageType,
		)
		return errors.NewServiceUnavailable("failed to publish message", err)
	}

	slog.DebugContext(ctx, "message published successfully",
		"subject", subject,
		"message_type", messageType,
		"message_size", len(data),
	)

	return nil
}

func (m *messagePublisher) Indexer(ctx context.Context, subject string, message any) error {
	return m.publish(ctx, subject, message, "indexer")
}

func (m *messagePublisher) Access(ctx context.Context, subject string, message any) error {
	return m.publish(ctx, subject, message, "access")
}

// NewMessagePublish creates a new message publish service
func NewMessagePublisher(client *NATSClient) port.CommitteePublisher {
	return &messagePublisher{
		client: client,
	}
}
