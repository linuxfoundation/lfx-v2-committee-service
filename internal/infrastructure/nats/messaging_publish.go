// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	fgaconstants "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/constants"
	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const defaultRequestTimeout = 10 * time.Second

type messagePublisherClient interface {
	IsReady(ctx context.Context) error
	publishWithSpan(ctx context.Context, subject string, data []byte) (context.Context, error)
	requestWithSpan(ctx context.Context, subject string, data []byte) (context.Context, *natsgo.Msg, error)
}

type messagePublisher struct {
	client messagePublisherClient
}

// publishMessage handles asynchronous NATS message publishing with an OTel producer span.
func (m *messagePublisher) publishMessage(ctx context.Context, subject string, data []byte, messageType string) error {
	ctx, err := m.client.publishWithSpan(ctx, subject, data)
	if err != nil {
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

// requestMessage handles synchronous NATS request/reply pattern with an OTel client span.
func (m *messagePublisher) requestMessage(ctx context.Context, subject string, data []byte, messageType string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()

	ctx, msg, err := m.client.requestWithSpan(ctx, subject, data)
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

func (m *messagePublisher) prepareMessage(ctx context.Context, subject string, message any, messageType string) ([]byte, error) {
	if err := m.client.IsReady(ctx); err != nil {
		slog.ErrorContext(ctx, "NATS client is not ready for publishing",
			"error", err,
			"subject", subject,
			"message_type", messageType,
		)
		return nil, errors.NewServiceUnavailable("NATS client is not ready", err)
	}

	if str, ok := message.(string); ok {
		return []byte(str), nil
	}

	data, err := json.Marshal(message)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal message to JSON",
			"error", err,
			"subject", subject,
			"message_type", messageType,
		)
		return nil, errors.NewUnexpected("failed to marshal message", err)
	}

	return data, nil
}

// publish is a generic function that handles NATS message publishing.
func (m *messagePublisher) publish(ctx context.Context, subject string, message any, messageType string, sync bool) error {
	data, err := m.prepareMessage(ctx, subject, message, messageType)
	if err != nil {
		return err
	}

	if sync {
		return m.requestMessage(ctx, subject, data, messageType)
	}

	return m.publishMessage(ctx, subject, data, messageType)
}

// Indexer publishes an indexer message to the given NATS subject for search index updates.
func (m *messagePublisher) Indexer(ctx context.Context, subject string, message any, sync bool) error {
	return m.publish(ctx, subject, message, "indexer", sync)
}

// Access publishes an access-control message to the given NATS subject for permission updates.
//
// GenericUpdateAccessSubject is guarded here regardless of sync: callers must use
// UpdateAccess for that subject so it can never reach requestMessage.
func (m *messagePublisher) Access(ctx context.Context, subject string, message any, sync bool) error {
	if subject == fgaconstants.GenericUpdateAccessSubject {
		return m.UpdateAccess(ctx, message)
	}
	return m.publish(ctx, subject, message, "access", sync)
}

// UpdateAccess publishes an update_access message asynchronously.
func (m *messagePublisher) UpdateAccess(ctx context.Context, message any) error {
	const messageType = "access"

	data, err := m.prepareMessage(ctx, fgaconstants.GenericUpdateAccessSubject, message, messageType)
	if err != nil {
		return err
	}

	return m.publishMessage(ctx, fgaconstants.GenericUpdateAccessSubject, data, messageType)
}

// Event publishes a domain event to the given NATS subject for downstream consumers.
func (m *messagePublisher) Event(ctx context.Context, subject string, event any, sync bool) error {
	return m.publish(ctx, subject, event, "event", sync)
}

// NewMessagePublish creates a new message publish service
func NewMessagePublisher(client *NATSClient) port.CommitteePublisher {
	return &messagePublisher{
		client: client,
	}
}
