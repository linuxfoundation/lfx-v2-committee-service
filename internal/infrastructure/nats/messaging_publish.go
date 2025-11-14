// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const defaultRequestTimeout = 10 * time.Second

type messagePublisher struct {
	client *NATSClient
}

// publishMessage handles asynchronous NATS message publishing
func (m *messagePublisher) publishMessage(ctx context.Context, subject string, data []byte, messageType string) error {
	if err := m.client.conn.Publish(subject, data); err != nil {
		slog.ErrorContext(ctx, "failed to publish message to NATS",
			"error", err,
			"subject", subject,
			"message_type", messageType,
		)
		return errors.NewServiceUnavailable("failed to publish message", err)
	}

	slog.DebugContext(ctx, "asynchronous message published successfully",
		"subject", subject,
		"message_type", messageType,
		"message_size", len(data),
	)

	return nil
}

// requestMessage handles synchronous NATS request/reply pattern
func (m *messagePublisher) requestMessage(ctx context.Context, subject string, data []byte, messageType string) error {
	msg, err := m.client.conn.Request(subject, data, defaultRequestTimeout)
	if err != nil {
		slog.ErrorContext(ctx, "failed to send synchronous request to NATS",
			"error", err,
			"subject", subject,
			"message_type", messageType,
			"timeout", defaultRequestTimeout,
		)
		return errors.NewServiceUnavailable("failed to send synchronous request", err)
	}

	slog.DebugContext(ctx, "synchronous message sent successfully",
		"subject", subject,
		"message_type", messageType,
		"message_size", len(data),
		"response_size", len(msg.Data),
	)

	return nil
}

// publish is a generic function that handles NATS message publishing
func (m *messagePublisher) publish(ctx context.Context, subject string, message any, messageType string, sync bool) error {
	// Check if client is ready
	if err := m.client.IsReady(ctx); err != nil {
		slog.ErrorContext(ctx, "NATS client is not ready for publishing",
			"error", err,
			"subject", subject,
			"message_type", messageType,
			"sync", sync,
		)
		return errors.NewServiceUnavailable("NATS client is not ready", err)
	}

	var data []byte
	// If message is a string, send it as plain text without JSON encoding
	if str, ok := message.(string); ok {
		data = []byte(str)
	} else {
		// Marshal message to JSON for complex types
		var err error
		data, err = json.Marshal(message)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal message to JSON",
				"error", err,
				"subject", subject,
				"message_type", messageType,
				"sync", sync,
			)
			return errors.NewUnexpected("failed to marshal message", err)
		}
	}

	// Publish message based on sync flag
	if sync {
		return m.requestMessage(ctx, subject, data, messageType)
	}

	return m.publishMessage(ctx, subject, data, messageType)
}

func (m *messagePublisher) Indexer(ctx context.Context, subject string, message any, sync bool) error {
	return m.publish(ctx, subject, message, "indexer", sync)
}

func (m *messagePublisher) Access(ctx context.Context, subject string, message any, sync bool) error {
	return m.publish(ctx, subject, message, "access", sync)
}

func (m *messagePublisher) Event(ctx context.Context, subject string, event any, sync bool) error {
	return m.publish(ctx, subject, event, "event", sync)
}

// NewMessagePublish creates a new message publish service
func NewMessagePublisher(client *NATSClient) port.CommitteePublisher {
	return &messagePublisher{
		client: client,
	}
}
