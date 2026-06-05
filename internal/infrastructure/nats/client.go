// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// NATSClient wraps the NATS connection and provides access control operations
type NATSClient struct {
	conn     *nats.Conn
	config   Config
	kvStore  map[string]jetstream.KeyValue
	objStore map[string]jetstream.ObjectStore
	timeout  time.Duration
}

// NATSClientInterface defines the interface for NATS operations
// This allows for easy mocking and testing
type NATSClientInterface interface {
	Close() error
	IsReady(ctx context.Context) error
}

// Close gracefully closes the NATS connection
func (c *NATSClient) Close() error {
	if c.conn != nil {
		c.conn.Close()
	}
	return nil
}

// IsReady checks if the NATS client is ready
func (c *NATSClient) IsReady(ctx context.Context) error {
	if c.conn == nil {
		return errors.NewServiceUnavailable("NATS client is not initialized or not connected")
	}
	if !c.conn.IsConnected() || c.conn.IsDraining() {
		return errors.NewServiceUnavailable("NATS client is not ready, connection is not established or is draining")
	}
	return nil
}

// KeyValueStore creates a JetStream client and gets the key-value store for projects.
func (c *NATSClient) KeyValueStore(ctx context.Context, bucketName string) error {
	js, err := jetstream.New(c.conn)
	if err != nil {
		slog.ErrorContext(ctx, "error creating NATS JetStream client",
			"error", err,
			"nats_url", c.conn.ConnectedUrl(),
		)
		return err
	}
	kvStore, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		slog.ErrorContext(ctx, "error getting NATS JetStream key-value store",
			"error", err,
			"nats_url", c.conn.ConnectedUrl(),
			"bucket", bucketName,
		)
		return err
	}

	if c.kvStore == nil {
		c.kvStore = make(map[string]jetstream.KeyValue)
	}
	c.kvStore[bucketName] = kvStore
	return nil
}

// ObjectStore creates a JetStream client and gets the object store by name.
func (c *NATSClient) ObjectStore(ctx context.Context, storeName string) error {
	js, err := jetstream.New(c.conn)
	if err != nil {
		slog.ErrorContext(ctx, "error creating NATS JetStream client for object store",
			"error", err,
			"nats_url", c.conn.ConnectedUrl(),
		)
		return err
	}
	objStore, err := js.ObjectStore(ctx, storeName)
	if err != nil {
		slog.ErrorContext(ctx, "error getting NATS JetStream object store",
			"error", err,
			"nats_url", c.conn.ConnectedUrl(),
			"store", storeName,
		)
		return err
	}

	if c.objStore == nil {
		c.objStore = make(map[string]jetstream.ObjectStore)
	}
	c.objStore[storeName] = objStore
	return nil
}

// publishWithSpan wraps conn.PublishMsg with an OTel producer span and injects
// trace context into the NATS message headers.
func (c *NATSClient) publishWithSpan(ctx context.Context, subject string, data []byte) error {
	ctx, span := tracer.Start(ctx, "nats.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.Int("messaging.message.body.size", len(data)),
		),
	)
	defer span.End()

	msg := nats.NewMsg(subject)
	msg.Header = make(nats.Header)
	msg.Data = data
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(msg.Header))

	if err := c.conn.PublishMsg(msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// requestWithSpan wraps conn.RequestMsgWithContext with an OTel client span and
// injects trace context into the NATS message headers.
func (c *NATSClient) requestWithSpan(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	ctx, span := tracer.Start(ctx, "nats.request",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.Int("messaging.message.body.size", len(data)),
		),
	)
	defer span.End()

	msg := nats.NewMsg(subject)
	msg.Header = make(nats.Header)
	msg.Data = data
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(msg.Header))

	reply, err := c.conn.RequestMsgWithContext(ctx, msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return reply, nil
}

// SubscribeWithTransportMessenger subscribes to a subject with proper TransportMessenger handling
func (c *NATSClient) SubscribeWithTransportMessenger(ctx context.Context, subject string, queueName string, handler func(context.Context, port.TransportMessenger)) (*nats.Subscription, error) {
	return c.conn.QueueSubscribe(subject, queueName, func(msg *nats.Msg) {
		// Extract trace context from incoming message headers and start a consumer span.
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Header))
		msgCtx, span := tracer.Start(msgCtx, "nats.process",
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination.name", subject),
				attribute.Int("messaging.message.body.size", len(msg.Data)),
			),
		)
		defer span.End()

		transportMsg := NewTransportMessenger(msg)

		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(msgCtx, "panic in NATS handler",
					"subject", subject,
					"queue", queueName,
					"panic", r,
				)
				span.RecordError(fmt.Errorf("panic in NATS handler: %v", r))
				span.SetStatus(codes.Error, "panic in NATS handler")
			}
		}()

		handler(msgCtx, transportMsg)
		span.SetStatus(codes.Ok, "")
	})
}

// NewClient creates a new NATS client with the given configuration
func NewClient(ctx context.Context, config Config) (*NATSClient, error) {
	slog.InfoContext(ctx, "creating NATS client",
		"url", config.URL,
		"timeout", config.Timeout,
	)

	// Validate configuration
	if config.URL == "" {
		return nil, errors.NewUnexpected("NATS URL is required")
	}

	// Configure NATS connection options
	opts := []nats.Option{
		nats.Name(constants.ServiceName),
		nats.Timeout(config.Timeout),
		nats.MaxReconnects(config.MaxReconnect),
		nats.ReconnectWait(config.ReconnectWait),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			slog.WarnContext(ctx, "NATS disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			slog.InfoContext(ctx, "NATS reconnected", "url", nc.ConnectedUrl())
		}),
		nats.ErrorHandler(func(_ *nats.Conn, s *nats.Subscription, err error) {
			if s != nil {
				slog.With("error", err, "subject", s.Subject, "queue", s.Queue).Error("async NATS error")
			} else {
				slog.With("error", err).Error("async NATS error outside subscription")
			}
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			slog.InfoContext(ctx, "NATS connection closed")
		}),
	}

	// Establish connection
	conn, err := nats.Connect(config.URL, opts...)
	if err != nil {
		return nil, errors.NewServiceUnavailable("failed to connect to NATS", err)
	}

	client := &NATSClient{
		conn:    conn,
		config:  config,
		timeout: config.Timeout,
	}

	// Core buckets are required for the service to function — failing to
	// initialize any of them is fatal.
	for _, bucketName := range []string{
		constants.KVBucketNameCommittees,
		constants.KVBucketNameCommitteeSettings,
		constants.KVBucketNameCommitteeMembers,
		constants.KVBucketNameCommitteeInvites,
		constants.KVBucketNameCommitteeApplications,
		constants.KVBucketNameCommitteeLinks,
		constants.KVBucketNameCommitteeFolders,
		constants.KVBucketNameCommitteeDocuments,
	} {
		if err := client.KeyValueStore(ctx, bucketName); err != nil {
			slog.ErrorContext(ctx, "failed to initialize NATS key-value store",
				"error", err,
				"bucket", bucketName,
			)
			return nil, errors.NewServiceUnavailable("failed to initialize NATS key-value store", err)
		}
		slog.InfoContext(ctx, "NATS key-value store initialized",
			"bucket", bucketName,
		)
	}

	// Weekly-brief buckets are initialized best-effort. If they aren't yet
	// provisioned (e.g. a rolling deploy where the chart hasn't created them, or
	// a local NATS without them) the service still starts; only the weekly-brief
	// endpoints return ServiceUnavailable until the buckets exist.
	for _, bucketName := range []string{
		constants.KVBucketNameGroupWeeklyBriefs,
		constants.KVBucketNameGroupWeeklyBriefUIDIndex,
		constants.KVBucketNameGroupWeeklyBriefThrottle,
	} {
		if err := client.KeyValueStore(ctx, bucketName); err != nil {
			slog.WarnContext(ctx, "weekly-brief KV bucket not initialized; weekly-brief endpoints will be unavailable until it is provisioned",
				"error", err,
				"bucket", bucketName,
			)
			continue
		}
		slog.InfoContext(ctx, "NATS key-value store initialized",
			"bucket", bucketName,
		)
	}

	for _, storeName := range []string{
		constants.ObjectStoreNameCommitteeDocuments,
	} {
		if err := client.ObjectStore(ctx, storeName); err != nil {
			slog.ErrorContext(ctx, "failed to initialize NATS object store",
				"error", err,
				"store", storeName,
			)
			return nil, errors.NewServiceUnavailable("failed to initialize NATS object store", err)
		}
		slog.InfoContext(ctx, "NATS object store initialized",
			"store", storeName,
		)
	}

	slog.InfoContext(ctx, "NATS client created successfully",
		"connected_url", conn.ConnectedUrl(),
		"status", conn.Status(),
	)

	return client, nil
}
