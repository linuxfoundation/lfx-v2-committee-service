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
		{
			name:    "member_put",
			subject: fgaconstants.GenericMemberPutSubject,
			publish: publisher.MemberPut,
		},
		{
			name:    "member_remove",
			subject: fgaconstants.GenericMemberRemoveSubject,
			publish: publisher.MemberRemove,
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

// TestMessagePublisher_AccessMethodsErrors covers the shared readiness, serialization,
// and core-publish failure paths for UpdateAccess, DeleteAccess, MemberPut, and
// MemberRemove, which all funnel through the same publishAccessAsync helper.
func TestMessagePublisher_AccessMethodsErrors(t *testing.T) {
	methods := []struct {
		name    string
		publish func(*messagePublisher) func(context.Context, any) error
	}{
		{name: "UpdateAccess", publish: func(p *messagePublisher) func(context.Context, any) error { return p.UpdateAccess }},
		{name: "DeleteAccess", publish: func(p *messagePublisher) func(context.Context, any) error { return p.DeleteAccess }},
		{name: "MemberPut", publish: func(p *messagePublisher) func(context.Context, any) error { return p.MemberPut }},
		{name: "MemberRemove", publish: func(p *messagePublisher) func(context.Context, any) error { return p.MemberRemove }},
	}

	tests := []struct {
		name          string
		newClient     func() *publisherClientStub
		message       any
		wantError     string
		wantPublished int
	}{
		{
			name:      "client not ready",
			newClient: func() *publisherClientStub { return &publisherClientStub{readyErr: errors.New("disconnected")} },
			message:   "payload",
			wantError: "NATS client is not ready",
		},
		{
			name:      "message cannot be serialized",
			newClient: func() *publisherClientStub { return &publisherClientStub{} },
			message:   make(chan int),
			wantError: "failed to marshal message",
		},
		{
			name:          "core publish fails",
			newClient:     func() *publisherClientStub { return &publisherClientStub{publishErr: errors.New("publish failed")} },
			message:       "payload",
			wantError:     "failed to publish message",
			wantPublished: 1,
		},
	}

	for _, method := range methods {
		t.Run(method.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					client := tt.newClient()
					publisher := &messagePublisher{client: client}

					err := method.publish(publisher)(context.Background(), tt.message)

					require.ErrorContains(t, err, tt.wantError)
					assert.Equal(t, tt.wantPublished, client.published)
					assert.Zero(t, client.requested)
				})
			}
		})
	}
}
