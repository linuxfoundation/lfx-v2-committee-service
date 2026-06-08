// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	pkgerrors "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startTestNATSServer(t *testing.T) (*natsserver.Server, string) {
	t.Helper()

	opts := &natsserver.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	}

	ns, err := natsserver.NewServer(opts)
	require.NoError(t, err)

	go ns.Start()
	require.True(t, ns.ReadyForConnections(4*time.Second), "NATS server not ready")

	t.Cleanup(ns.Shutdown)
	return ns, ns.ClientURL()
}

type usernameByEmailTestEnv struct {
	reader       *messageRequest
	respondErrCh <-chan error
}

func setupUsernameByEmailTest(t *testing.T, responder func(email string) []byte) usernameByEmailTestEnv {
	t.Helper()

	_, url := startTestNATSServer(t)

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	respondErrCh := make(chan error, 1)
	_, err = nc.Subscribe(constants.AuthEmailToUsernameLookupSubject, func(msg *nats.Msg) {
		if respondErr := msg.Respond(responder(string(msg.Data))); respondErr != nil {
			select {
			case respondErrCh <- respondErr:
			default:
			}
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	return usernameByEmailTestEnv{
		reader: &messageRequest{
			client: &NATSClient{
				conn:    nc,
				timeout: 2 * time.Second,
			},
		},
		respondErrCh: respondErrCh,
	}
}

func assertNoRespondError(t *testing.T, respondErrCh <-chan error) {
	t.Helper()

	select {
	case respondErr := <-respondErrCh:
		require.NoError(t, respondErr)
	default:
	}
}

func TestMessageRequest_UsernameByEmail(t *testing.T) {
	tests := []struct {
		name       string
		responder  func(email string) []byte
		setup      func(t *testing.T) *messageRequest
		wantUser   string
		wantErr    error
		wantErrStr string
	}{
		{
			name: "plain-text username returned on success",
			responder: func(email string) []byte {
				return []byte("alice")
			},
			wantUser: "alice",
		},
		{
			name: "trailing newline trimmed from username",
			responder: func(email string) []byte {
				return []byte("alice\n")
			},
			wantUser: "alice",
		},
		{
			name: "leading and trailing whitespace trimmed",
			responder: func(email string) []byte {
				return []byte("  alice  ")
			},
			wantUser: "alice",
		},
		{
			name: "empty body returns NotFound",
			responder: func(email string) []byte {
				return []byte("")
			},
			wantErrStr: "user not found for email",
		},
		{
			name: "whitespace-only body returns NotFound",
			responder: func(email string) []byte {
				return []byte("   \n  ")
			},
			wantErrStr: "user not found for email",
		},
		{
			name: "JSON error envelope returns NotFound",
			responder: func(email string) []byte {
				return []byte(`{"success":false,"error":"user not found"}`)
			},
			wantErrStr: "user not found",
		},
		{
			name: "JSON envelope missing success field returns Unexpected",
			responder: func(email string) []byte {
				return []byte(`{"error":"something unexpected"}`)
			},
			wantErrStr: "something unexpected",
		},
		{
			name: "JSON success envelope returns error instead of leaking JSON as username",
			responder: func(email string) []byte {
				return []byte(`{"success":true,"username":"alice"}`)
			},
			wantErrStr: "unexpected email_to_username success envelope",
		},
		{
			name: "malformed JSON object returns parse error instead of leaking raw body as username",
			responder: func(email string) []byte {
				return []byte(`{"success":"true"}`)
			},
			wantErrStr: "failed to parse NATS error response",
		},
		{
			name: "transport error is wrapped and returned",
			setup: func(t *testing.T) *messageRequest {
				_, url := startTestNATSServer(t)
				nc, err := nats.Connect(url)
				require.NoError(t, err)
				t.Cleanup(nc.Close)

				return &messageRequest{
					client: &NATSClient{
						conn:    nc,
						timeout: 50 * time.Millisecond,
					},
				}
			},
			wantErrStr: "email_to_username request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader *messageRequest
			var respondErrCh <-chan error
			if tt.setup != nil {
				reader = tt.setup(t)
			} else {
				env := setupUsernameByEmailTest(t, tt.responder)
				reader = env.reader
				respondErrCh = env.respondErrCh
			}

			got, err := reader.UsernameByEmail(context.Background(), "test@example.com")
			if respondErrCh != nil {
				assertNoRespondError(t, respondErrCh)
			}

			switch {
			case tt.wantErrStr != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrStr)
				assert.Empty(t, got)
			case tt.wantErr != nil:
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, got)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantUser, got)
			}
		})
	}
}

func TestMessageRequest_UsernameByEmail_NotFoundType(t *testing.T) {
	env := setupUsernameByEmailTest(t, func(email string) []byte {
		return []byte(`{"success":false,"error":"user not found"}`)
	})

	_, err := env.reader.UsernameByEmail(context.Background(), "test@example.com")
	assertNoRespondError(t, env.respondErrCh)
	require.Error(t, err)

	var notFound pkgerrors.NotFound
	assert.True(t, errors.As(err, &notFound))
}
