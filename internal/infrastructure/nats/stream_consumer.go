// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"log/slog"
	"math"
	"math/rand"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/nats-io/nats.go/jetstream"
)

// streamMessengerAdapter wraps a jetstream.Msg and implements port.StreamMessenger so the
// domain layer never depends on JetStream types directly.
type streamMessengerAdapter struct {
	msg jetstream.Msg
}

func (a *streamMessengerAdapter) Subject() string { return a.msg.Subject() }
func (a *streamMessengerAdapter) Data() []byte    { return a.msg.Data() }

// ConsumeWithJetStream creates or binds a durable JetStream consumer on streamName and starts
// delivering messages to handler. ACK and NAK-with-backoff are handled here so the domain
// handler only needs to return an error. The returned ConsumeContext must be stopped by the
// caller (typically via defer consumeCtx.Stop()) to release the consumer goroutine.
func (c *NATSClient) ConsumeWithJetStream(
	ctx context.Context,
	streamName string,
	cfg jetstream.ConsumerConfig,
	handler func(ctx context.Context, msg port.StreamMessenger) error,
) (jetstream.ConsumeContext, error) {
	js, err := jetstream.New(c.conn)
	if err != nil {
		slog.ErrorContext(ctx, "error creating JetStream client for consumer",
			"error", err,
			"stream", streamName,
			"consumer", cfg.Name,
		)
		return nil, err
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, streamName, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "error creating JetStream durable consumer",
			"error", err,
			"stream", streamName,
			"consumer", cfg.Name,
		)
		return nil, err
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		if err := handler(ctx, &streamMessengerAdapter{msg: msg}); err != nil {
			slog.ErrorContext(ctx, "stream message handler returned error — NAKing with backoff",
				"error", err,
				"subject", msg.Subject(),
				"consumer", cfg.Name,
			)
			if nakErr := msg.NakWithDelay(nakDelay(msg)); nakErr != nil {
				slog.ErrorContext(ctx, "failed to NAK stream message",
					"error", nakErr,
					"subject", msg.Subject(),
				)
			}
			return
		}
		if ackErr := msg.Ack(); ackErr != nil {
			slog.ErrorContext(ctx, "failed to ACK stream message",
				"error", ackErr,
				"subject", msg.Subject(),
			)
		}
	})
	if err != nil {
		slog.ErrorContext(ctx, "error starting JetStream consume loop",
			"error", err,
			"stream", streamName,
			"consumer", cfg.Name,
		)
		return nil, err
	}

	slog.InfoContext(ctx, "JetStream durable consumer started",
		"stream", streamName,
		"consumer", cfg.Name,
		"filter_subjects", cfg.FilterSubjects,
	)

	return consumeCtx, nil
}

// nakDelay returns an exponential backoff duration with full jitter based on the message
// delivery attempt count. Full jitter (random in [0, cap]) prevents correlated retries
// across concurrent service replicas.
//
// Attempt 1 → rand(0, 1s)
// Attempt 2 → rand(0, 2s)
func nakDelay(msg jetstream.Msg) time.Duration {
	meta, err := msg.Metadata()
	if err != nil || meta == nil {
		return time.Second
	}
	cap := time.Second * time.Duration(math.Pow(2, float64(meta.NumDelivered-1)))
	return time.Duration(rand.Int63n(int64(cap) + 1))
}
