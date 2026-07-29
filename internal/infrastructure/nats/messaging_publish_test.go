// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"testing"
	"time"

	fgaconstants "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/constants"
	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type publisherClientStub struct {
	readyErr   error
	publishErr error
	published  int
	requested  int
}

func (s *publisherClientStub) IsReady(context.Context) error {
	return s.readyErr
}

func (s *publisherClientStub) publishWithSpan(ctx context.Context, _ string, _ []byte) (context.Context, error) {
	s.published++
	return ctx, s.publishErr
}

func (s *publisherClientStub) requestWithSpan(ctx context.Context, _ string, _ []byte) (context.Context, *natsgo.Msg, error) {
	s.requested++
	return ctx, &natsgo.Msg{}, nil
}

func TestMessagePublisher_AccessCommandsPublishWithoutReply(t *testing.T) {
	_, url := startTestNATSServer(t)

	nc, err := natsgo.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	previousProvider := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	publisher := &messagePublisher{client: &NATSClient{conn: nc}}
	tests := []struct {
		name    string
		subject string
		publish func(context.Context, any) error
	}{
		{
			name:    "update_access",
			subject: fgaconstants.GenericUpdateAccessSubject,
			publish: publisher.UpdateAccess,
		},
		{
			name:    "delete_access",
			subject: fgaconstants.GenericDeleteAccessSubject,
			publish: publisher.DeleteAccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := make(chan *natsgo.Msg, 1)
			sub, err := nc.Subscribe(tt.subject, func(msg *natsgo.Msg) {
				messages <- msg
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, sub.Unsubscribe())
			})
			require.NoError(t, nc.Flush())

			spanStart := len(recorder.Ended())
			require.NoError(t, tt.publish(context.Background(), map[string]string{"uid": "committee-1"}))
			require.NoError(t, nc.Flush())

			select {
			case msg := <-messages:
				assert.Empty(t, msg.Reply)
				assert.JSONEq(t, `{"uid":"committee-1"}`, string(msg.Data))
			case <-time.After(time.Second):
				t.Fatalf("timed out waiting for %s publication", tt.name)
			}

			var spanNames []string
			for _, span := range recorder.Ended()[spanStart:] {
				spanNames = append(spanNames, span.Name())
			}
			assert.Contains(t, spanNames, "nats.publish")
			assert.NotContains(t, spanNames, "nats.request")
		})
	}
}

func TestMessagePublisher_AccessGuardsUpdateAccessSubject(t *testing.T) {
	tests := []struct {
		name string
		sync bool
	}{
		{name: "sync=true cannot reach requestMessage", sync: true},
		{name: "sync=false behaves as UpdateAccess", sync: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &publisherClientStub{}
			publisher := &messagePublisher{client: client}

			err := publisher.Access(context.Background(), fgaconstants.GenericUpdateAccessSubject, "payload", tt.sync)

			require.NoError(t, err)
			assert.Equal(t, 1, client.published, "Access must route GenericUpdateAccessSubject through publishWithSpan regardless of sync")
			assert.Zero(t, client.requested, "Access must never reach requestWithSpan for GenericUpdateAccessSubject")
		})
	}
}

func TestMessagePublisher_AccessGuardsDeleteAccessSubject(t *testing.T) {
	tests := []struct {
		name string
		sync bool
	}{
		{name: "sync=true cannot reach requestMessage", sync: true},
		{name: "sync=false behaves as DeleteAccess", sync: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &publisherClientStub{}
			publisher := &messagePublisher{client: client}

			err := publisher.Access(context.Background(), fgaconstants.GenericDeleteAccessSubject, "payload", tt.sync)

			require.NoError(t, err)
			assert.Equal(t, 1, client.published, "Access must route GenericDeleteAccessSubject through publishWithSpan regardless of sync")
			assert.Zero(t, client.requested, "Access must never reach requestWithSpan for GenericDeleteAccessSubject")
		})
	}
}

func TestMessagePublisher_UpdateAccessErrors(t *testing.T) {
	tests := []struct {
		name          string
		client        *publisherClientStub
		message       any
		wantError     string
		wantPublished int
	}{
		{
			name:      "client not ready",
			client:    &publisherClientStub{readyErr: errors.New("disconnected")},
			message:   "payload",
			wantError: "NATS client is not ready",
		},
		{
			name:      "message cannot be serialized",
			client:    &publisherClientStub{},
			message:   make(chan int),
			wantError: "failed to marshal message",
		},
		{
			name:          "core publish fails",
			client:        &publisherClientStub{publishErr: errors.New("publish failed")},
			message:       "payload",
			wantError:     "failed to publish message",
			wantPublished: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := &messagePublisher{client: tt.client}

			err := publisher.UpdateAccess(context.Background(), tt.message)

			require.ErrorContains(t, err, tt.wantError)
			assert.Equal(t, tt.wantPublished, tt.client.published)
		})
	}
}

func TestMessagePublisher_DeleteAccessErrors(t *testing.T) {
	tests := []struct {
		name          string
		client        *publisherClientStub
		message       any
		wantError     string
		wantPublished int
	}{
		{
			name:      "client not ready",
			client:    &publisherClientStub{readyErr: errors.New("disconnected")},
			message:   "payload",
			wantError: "NATS client is not ready",
		},
		{
			name:      "message cannot be serialized",
			client:    &publisherClientStub{},
			message:   make(chan int),
			wantError: "failed to marshal message",
		},
		{
			name:          "core publish fails",
			client:        &publisherClientStub{publishErr: errors.New("publish failed")},
			message:       "payload",
			wantError:     "failed to publish message",
			wantPublished: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := &messagePublisher{client: tt.client}

			err := publisher.DeleteAccess(context.Background(), tt.message)

			require.ErrorContains(t, err, tt.wantError)
			assert.Equal(t, tt.wantPublished, tt.client.published)
			assert.Zero(t, tt.client.requested)
		})
	}
}
