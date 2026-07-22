// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
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

			if tt.wantErrStr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrStr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantUser, got)
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

type emailsByAuthTokenTestEnv struct {
	reader       *messageRequest
	respondErrCh <-chan error
}

func setupEmailsByAuthTokenTest(t *testing.T, responder func(payload []byte) []byte) emailsByAuthTokenTestEnv {
	t.Helper()

	_, url := startTestNATSServer(t)

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	respondErrCh := make(chan error, 1)
	_, err = nc.Subscribe(constants.AuthUserEmailsReadSubject, func(msg *nats.Msg) {
		if respondErr := msg.Respond(responder(msg.Data)); respondErr != nil {
			select {
			case respondErrCh <- respondErr:
			default:
			}
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	return emailsByAuthTokenTestEnv{
		reader: &messageRequest{
			client: &NATSClient{
				conn:    nc,
				timeout: 2 * time.Second,
			},
		},
		respondErrCh: respondErrCh,
	}
}

func TestMessageRequest_EmailsByAuthToken(t *testing.T) {
	tests := []struct {
		name       string
		authToken  string
		responder  func(payload []byte) []byte
		setup      func(t *testing.T) *messageRequest
		wantEmails *struct {
			primary   string
			alternate string
			verified  bool
		}
		wantErrStr string
	}{
		{
			name:      "success returns primary and alternate emails",
			authToken: "auth0|alice",
			responder: func(payload []byte) []byte {
				var req UserEmailsNATSRequest
				if err := json.Unmarshal(payload, &req); err != nil || req.User.AuthToken != "auth0|alice" {
					return []byte(`{"success":false,"error":"unexpected request"}`)
				}
				return []byte(`{"success":true,"data":{"primary_email":"alice@example.com","alternate_emails":[{"email":"alice.alt@example.com","verified":true}]}}`)
			},
			wantEmails: &struct {
				primary   string
				alternate string
				verified  bool
			}{
				primary:   "alice@example.com",
				alternate: "alice.alt@example.com",
				verified:  true,
			},
		},
		{
			name:       "empty auth token returns validation error",
			authToken:  "",
			wantErrStr: "auth token must not be empty",
		},
		{
			name:      "auth-service not-found envelope returns NotFound",
			authToken: "auth0|missing",
			responder: func(payload []byte) []byte {
				return []byte(`{"success":false,"error":"user not found"}`)
			},
			wantErrStr: "user emails not found",
		},
		{
			name:      "auth-service validation error envelope returns Unexpected (not NotFound)",
			authToken: "auth0|missing",
			responder: func(payload []byte) []byte {
				return []byte(`{"success":false,"error":"user_id is required to get user"}`)
			},
			wantErrStr: "auth-service user emails request failed",
		},
		{
			name:      "success with nil data returns NotFound",
			authToken: "auth0|alice",
			responder: func(payload []byte) []byte {
				return []byte(`{"success":true}`)
			},
			wantErrStr: "no email data returned for user",
		},
		{
			name:      "malformed JSON response returns parse error",
			authToken: "auth0|alice",
			responder: func(payload []byte) []byte {
				return []byte(`{"success":`)
			},
			wantErrStr: "failed to parse user_emails response",
		},
		{
			name:      "transport error is wrapped as ServiceUnavailable",
			authToken: "auth0|alice",
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
			wantErrStr: "auth service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader *messageRequest
			var respondErrCh <-chan error
			if tt.setup != nil {
				reader = tt.setup(t)
			} else {
				env := setupEmailsByAuthTokenTest(t, tt.responder)
				reader = env.reader
				respondErrCh = env.respondErrCh
			}

			got, err := reader.EmailsByAuthToken(context.Background(), tt.authToken)
			if respondErrCh != nil {
				assertNoRespondError(t, respondErrCh)
			}

			if tt.wantErrStr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrStr)
				assert.Nil(t, got)
				if tt.wantErrStr == "auth service unavailable" {
					var unavailable pkgerrors.ServiceUnavailable
					assert.True(t, errors.As(err, &unavailable))
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			require.NotNil(t, tt.wantEmails)
			assert.Equal(t, tt.wantEmails.primary, got.PrimaryEmail)
			require.Len(t, got.AlternateEmails, 1)
			assert.Equal(t, tt.wantEmails.alternate, got.AlternateEmails[0].Email)
			assert.Equal(t, tt.wantEmails.verified, got.AlternateEmails[0].Verified)
		})
	}
}

func TestMessageRequest_EmailsByAuthToken_NotFoundType(t *testing.T) {
	env := setupEmailsByAuthTokenTest(t, func(payload []byte) []byte {
		return []byte(`{"success":false,"error":"user not found"}`)
	})

	_, err := env.reader.EmailsByAuthToken(context.Background(), "auth0|missing")
	assertNoRespondError(t, env.respondErrCh)
	require.Error(t, err)

	var notFound pkgerrors.NotFound
	assert.True(t, errors.As(err, &notFound))
}

type userMetadataByPrincipalTestEnv struct {
	reader       *messageRequest
	respondErrCh <-chan error
	gotKeyCh     <-chan string
}

func setupUserMetadataByPrincipalTest(t *testing.T, responder func(key string) []byte) userMetadataByPrincipalTestEnv {
	t.Helper()

	_, url := startTestNATSServer(t)

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	respondErrCh := make(chan error, 1)
	gotKeyCh := make(chan string, 1)
	_, err = nc.Subscribe(constants.AuthUserMetadataReadSubject, func(msg *nats.Msg) {
		key := string(msg.Data)
		select {
		case gotKeyCh <- key:
		default:
		}
		if respondErr := msg.Respond(responder(key)); respondErr != nil {
			select {
			case respondErrCh <- respondErr:
			default:
			}
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	return userMetadataByPrincipalTestEnv{
		reader: &messageRequest{
			client: &NATSClient{
				conn:    nc,
				timeout: 2 * time.Second,
			},
		},
		respondErrCh: respondErrCh,
		gotKeyCh:     gotKeyCh,
	}
}

// Asserts what lands on the wire (the derived sub), so reverting the request payload to the raw
// principal is caught — the response-handling branches are covered separately below.
func TestMessageRequest_UserMetadataByPrincipal_SendsDerivedSub(t *testing.T) {
	tests := []struct {
		name      string
		principal string
		wantKey   string
	}{
		{name: "bare LFID is mapped to its deterministic auth0| sub", principal: "alice", wantKey: "auth0|alice"},
		{name: "already-qualified auth0 principal passes through unchanged", principal: "auth0|abc123", wantKey: "auth0|abc123"},
		{name: "non-auth0 provider principal passes through unchanged", principal: "oidc|okta|xyz", wantKey: "oidc|okta|xyz"},
		{name: "principal is trimmed before lookup", principal: "  alice  ", wantKey: "auth0|alice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupUserMetadataByPrincipalTest(t, func(key string) []byte {
				return []byte(`{"success":true,"data":{"name":"Alice"}}`)
			})

			_, err := env.reader.UserMetadataByPrincipal(context.Background(), tt.principal)
			require.NoError(t, err)
			assertNoRespondError(t, env.respondErrCh)

			select {
			case gotKey := <-env.gotKeyCh:
				assert.Equal(t, tt.wantKey, gotKey)
			case <-time.After(2 * time.Second):
				t.Fatal("responder did not receive a request")
			}
		})
	}
}

func TestMessageRequest_UserMetadataByPrincipal(t *testing.T) {
	const (
		errNone       = ""
		errNotFound   = "NotFound"
		errUnexpected = "Unexpected"
		errAny        = "any"
	)

	tests := []struct {
		name        string
		responder   func(key string) []byte
		setup       func(t *testing.T) *messageRequest
		wantPicture string
		wantName    string
		wantErrStr  string
		wantErrType string
	}{
		{
			name: "success populates metadata",
			responder: func(key string) []byte {
				return []byte(`{"success":true,"data":{"picture":"https://example.com/a.png","name":"Alice"}}`)
			},
			wantPicture: "https://example.com/a.png",
			wantName:    "Alice",
			wantErrType: errNone,
		},
		{
			name:        "empty reply returns NotFound",
			responder:   func(key string) []byte { return []byte("") },
			wantErrStr:  "user metadata not found for principal",
			wantErrType: errNotFound,
		},
		{
			name:        "whitespace-only reply returns NotFound",
			responder:   func(key string) []byte { return []byte("  \n\t") },
			wantErrStr:  "user metadata not found for principal",
			wantErrType: errNotFound,
		},
		{
			name: "search miss envelope returns NotFound",
			responder: func(key string) []byte {
				return []byte(`{"success":false,"error":"user not found"}`)
			},
			wantErrType: errNotFound,
		},
		{
			name: "get-by-id miss envelope returns NotFound",
			responder: func(key string) []byte {
				return []byte(`{"success":false,"error":"The user does not exist."}`)
			},
			wantErrType: errNotFound,
		},
		{
			name: "success envelope with nil data returns NotFound",
			responder: func(key string) []byte {
				return []byte(`{"success":true}`)
			},
			wantErrStr:  "user metadata not found for principal",
			wantErrType: errNotFound,
		},
		{
			name: "non-miss error envelope returns Unexpected",
			responder: func(key string) []byte {
				return []byte(`{"success":false,"error":"internal server error"}`)
			},
			wantErrStr:  "user metadata lookup failed for principal",
			wantErrType: errUnexpected,
		},
		{
			name: "rate-limit envelope returns Unexpected (transient, not a miss)",
			responder: func(key string) []byte {
				return []byte(`{"success":false,"error":"too_many_requests: Global limit has been reached"}`)
			},
			wantErrType: errUnexpected,
		},
		{
			name: "malformed JSON returns Unexpected parse error",
			responder: func(key string) []byte {
				return []byte(`{"success":`)
			},
			wantErrStr:  "failed to parse user_metadata response",
			wantErrType: errUnexpected,
		},
		{
			name: "transport error is returned",
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
			wantErrType: errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader *messageRequest
			var respondErrCh <-chan error
			if tt.setup != nil {
				reader = tt.setup(t)
			} else {
				env := setupUserMetadataByPrincipalTest(t, tt.responder)
				reader = env.reader
				respondErrCh = env.respondErrCh
			}

			got, err := reader.UserMetadataByPrincipal(context.Background(), "alice")
			if respondErrCh != nil {
				assertNoRespondError(t, respondErrCh)
			}

			if tt.wantErrType != errNone {
				require.Error(t, err)
				assert.Nil(t, got)
				if tt.wantErrStr != "" {
					assert.Contains(t, err.Error(), tt.wantErrStr)
				}
				switch tt.wantErrType {
				case errNotFound:
					var notFound pkgerrors.NotFound
					assert.True(t, errors.As(err, &notFound), "want NotFound, got %T: %v", err, err)
				case errUnexpected:
					var unexpected pkgerrors.Unexpected
					assert.True(t, errors.As(err, &unexpected), "want Unexpected, got %T: %v", err, err)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantPicture, got.Picture)
			assert.Equal(t, tt.wantName, got.Name)
		})
	}
}
