// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/ai"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/m2m"
	infrastructure "github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	usecaseSvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"

	"github.com/auth0/go-auth0/authentication"
	"github.com/auth0/go-auth0/authentication/oauth"
	"golang.org/x/oauth2"
)

var (
	natsStorage    port.CommitteeReaderWriter
	natsMessaging  port.ProjectReader
	natsUserReader port.UserReader
	natsPublisher  port.CommitteePublisher

	// expose the NATS client for direct access in subscriptions
	natsClient *nats.NATSClient

	natsDoOnce sync.Once
)

func natsInit(ctx context.Context) {

	natsDoOnce.Do(func() {
		natsURL := os.Getenv("NATS_URL")
		if natsURL == "" {
			natsURL = "nats://localhost:4222"
		}

		natsTimeout := os.Getenv("NATS_TIMEOUT")
		if natsTimeout == "" {
			natsTimeout = "10s"
		}
		natsTimeoutDuration, err := time.ParseDuration(natsTimeout)
		if err != nil {
			log.Fatalf("invalid NATS timeout duration: %v", err)
		}

		natsMaxReconnect := os.Getenv("NATS_MAX_RECONNECT")
		if natsMaxReconnect == "" {
			natsMaxReconnect = "3"
		}
		natsMaxReconnectInt, err := strconv.Atoi(natsMaxReconnect)
		if err != nil {
			log.Fatalf("invalid NATS max reconnect value %s: %v", natsMaxReconnect, err)
		}

		natsReconnectWait := os.Getenv("NATS_RECONNECT_WAIT")
		if natsReconnectWait == "" {
			natsReconnectWait = "2s"
		}
		natsReconnectWaitDuration, err := time.ParseDuration(natsReconnectWait)
		if err != nil {
			log.Fatalf("invalid NATS reconnect wait duration %s : %v", natsReconnectWait, err)
		}

		config := nats.Config{
			URL:           natsURL,
			Timeout:       natsTimeoutDuration,
			MaxReconnect:  natsMaxReconnectInt,
			ReconnectWait: natsReconnectWaitDuration,
		}

		client, errNewClient := nats.NewClient(ctx, config)
		if errNewClient != nil {
			log.Fatalf("failed to create NATS client: %v", errNewClient)
		}
		natsClient = client
		natsStorage = nats.NewStorage(client)
		natsMessaging = nats.NewMessageRequest(client)
		natsUserReader = nats.NewUserRequest(client)
		natsPublisher = nats.NewMessagePublisher(client)
	})
}

func natsStorageImpl(ctx context.Context) port.CommitteeReaderWriter {
	natsInit(ctx)
	return natsStorage
}

func natsMessagingImpl(ctx context.Context) port.ProjectReader {
	natsInit(ctx)
	return natsMessaging
}

func natsPublisherImpl(ctx context.Context) port.CommitteePublisher {
	natsInit(ctx)
	return natsPublisher
}

// CommitteeReaderImpl initializes the committee reader implementation based on the repository source
func CommitteeReaderImpl(ctx context.Context) port.CommitteeReader {
	var committeeRetriever port.CommitteeReader

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee reader")
		committeeRetriever = infrastructure.NewMockCommitteeReader(infrastructure.NewMockRepository())

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee reader")
		natsClient := natsStorageImpl(ctx)
		if natsClient == nil {
			log.Fatalf("failed to initialize NATS client")
		}
		committeeRetriever = natsClient

	default:
		log.Fatalf("unsupported committee reader implementation: %s", repoSource)
	}

	return committeeRetriever
}

// CommitteeWriterImpl initializes the committee writer implementation based on the repository source
func CommitteeWriterImpl(ctx context.Context) port.CommitteeWriter {
	var committeeWriter port.CommitteeWriter

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee writer")
		committeeWriter = infrastructure.NewMockCommitteeWriter(infrastructure.NewMockRepository())

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee writer")
		natsClient := natsStorageImpl(ctx)
		if natsClient == nil {
			log.Fatalf("failed to initialize NATS client")
		}
		committeeWriter = natsClient

	default:
		log.Fatalf("unsupported committee writer implementation: %s", repoSource)
	}

	return committeeWriter
}

// ProjectRetrieverImpl initializes the project retriever implementation based on the repository source
func ProjectRetrieverImpl(ctx context.Context) port.ProjectReader {
	var projectReader port.ProjectReader

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock project retriever")
		projectReader = infrastructure.NewMockProjectRetriever(infrastructure.NewMockRepository())

	case "nats":
		slog.InfoContext(ctx, "initializing NATS project retriever")
		natsClient := natsMessagingImpl(ctx)
		if natsClient == nil {
			log.Fatalf("failed to initialize NATS client")
		}
		projectReader = natsClient

	default:
		log.Fatalf("unsupported project reader implementation: %s", repoSource)
	}

	return projectReader
}

// UserReaderImpl initializes the user reader implementation based on the repository source
func UserReaderImpl(ctx context.Context) port.UserReader {
	var userReader port.UserReader

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock user reader")
		userReader = infrastructure.NewMockUserReader()

	case "nats":
		slog.InfoContext(ctx, "initializing NATS user reader")
		natsInit(ctx)
		if natsUserReader == nil {
			log.Fatalf("failed to initialize NATS user reader")
		}
		userReader = natsUserReader

	default:
		log.Fatalf("unsupported user reader implementation: %s", repoSource)
	}

	return userReader
}

// AuthServiceImpl initializes the authentication service implementation
func AuthServiceImpl(ctx context.Context) port.Authenticator {
	var authService port.Authenticator

	// Repository implementation configuration
	authSource := os.Getenv("AUTH_SOURCE")
	if authSource == "" {
		authSource = "jwt"
	}

	switch authSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock authentication service")
		authService = infrastructure.NewMockAuthService()
	case "jwt":
		slog.InfoContext(ctx, "initializing JWT authentication service")
		jwtConfig := auth.JWTAuthConfig{
			JWKSURL:  os.Getenv("JWKS_URL"),
			Audience: os.Getenv("JWT_AUDIENCE"),
		}
		if jwtConfig.JWKSURL == "" || jwtConfig.Audience == "" {
			log.Fatalf("JWT configuration incomplete: JWKS_URL and JWT_AUDIENCE are required")
		}
		jwtAuth, err := auth.NewJWTAuth(jwtConfig)
		if err != nil {
			log.Fatalf("failed to initialize JWT authentication service: %v", err)
		}
		authService = jwtAuth
	default:
		log.Fatalf("unsupported authentication service implementation: %s", authSource)
	}

	return authService
}

// AIAdapterImpl initializes the AI adapter used for weekly-brief generation.
// Selection is driven by AI_SOURCE:
//   - "fake" (default)  -> deterministic in-process adapter (local dev, CI, tests)
//   - "live"            -> LiteLLM HTTP adapter, configured via
//     LITELLM_BASE_URL, LITELLM_API_KEY, LITELLM_MODEL
//
// When AI_SOURCE is unset, "fake" is selected so a fresh install boots
// without LiteLLM credentials provisioned. Operators must explicitly set
// AI_SOURCE=live to enable the live adapter; in that mode, missing required
// LiteLLM env vars are a fail-fast at startup rather than a silent degrade.
func AIAdapterImpl(ctx context.Context) port.AIAdapter {
	aiSource := os.Getenv("AI_SOURCE")
	if aiSource == "" {
		aiSource = "fake"
	}

	switch aiSource {
	case "fake":
		slog.InfoContext(ctx, "initializing fake AI adapter", "ai_source", aiSource)
		return ai.NewFakeAdapter()
	case "live":
		cfg := ai.LiteLLMConfig{
			BaseURL: os.Getenv("LITELLM_BASE_URL"),
			APIKey:  os.Getenv("LITELLM_API_KEY"),
			Model:   os.Getenv("LITELLM_MODEL"),
		}
		if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
			log.Fatalf(
				"AI_SOURCE=live requires LITELLM_BASE_URL, LITELLM_API_KEY, and LITELLM_MODEL "+
					"(set AI_SOURCE=fake for local dev/CI); got base_url=%q, api_key_set=%t, model=%q",
				cfg.BaseURL, cfg.APIKey != "", cfg.Model,
			)
		}
		slog.InfoContext(ctx, "initializing live LiteLLM AI adapter",
			"ai_source", aiSource, "model", cfg.Model)
		return ai.NewLiteLLMAdapter(cfg)
	default:
		log.Fatalf("unsupported AI adapter implementation: %s (expected one of: fake, live)", aiSource)
	}

	// unreachable
	return nil
}

// CommitteePublisherImpl initializes the committee publisher implementation based on the messaging source
func CommitteePublisherImpl(ctx context.Context) port.CommitteePublisher {
	var committeePublisher port.CommitteePublisher

	// Messaging implementation configuration
	messagingSource := os.Getenv("MESSAGING_SOURCE")
	if messagingSource == "" {
		messagingSource = "nats"
	}

	switch messagingSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee publisher")
		committeePublisher = infrastructure.NewMockCommitteePublisher()

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee publisher")
		committeePublisher = natsPublisherImpl(ctx)

	default:
		log.Fatalf("unsupported committee publisher implementation: %s", messagingSource)
	}

	return committeePublisher
}

// CommitteeReaderWriterImpl initializes the committee reader/writer implementation based on the repository source
func CommitteeReaderWriterImpl(ctx context.Context) port.CommitteeReaderWriter {
	var storage port.CommitteeReaderWriter

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee storage")
		storage = infrastructure.NewMockCommitteeReaderWriter(infrastructure.NewMockRepository())

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee storage")
		natsClient := natsStorageImpl(ctx)
		if natsClient == nil {
			log.Fatalf("failed to initialize NATS client")
		}
		storage = natsClient

	default:
		log.Fatalf("unsupported committee storage implementation: %s", repoSource)
	}

	return storage
}

// CommitteeLinkReaderWriterImpl initializes the committee link reader/writer implementation based on the repository source
func CommitteeLinkReaderWriterImpl(ctx context.Context) port.CommitteeLinkReaderWriter {
	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee link storage")
		return infrastructure.NewMockLinkRepository()

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee link storage")
		s := natsStorageImpl(ctx)
		if s == nil {
			log.Fatalf("failed to initialize NATS client for link storage")
		}
		linkRW, ok := s.(port.CommitteeLinkReaderWriter)
		if !ok {
			log.Fatalf("NATS storage does not implement CommitteeLinkReaderWriter")
		}
		return linkRW

	default:
		log.Fatalf("unsupported committee link storage implementation: %s", repoSource)
	}

	// unreachable
	return nil
}

// EmailSenderImpl initializes the email sender for notification emails.
// Returns nil (disabling all outbound emails) when EMAILS_ENABLED is not "true".
func EmailSenderImpl(ctx context.Context) port.EmailSender {
	if os.Getenv("EMAILS_ENABLED") != "true" {
		slog.InfoContext(ctx, "outbound emails disabled — set EMAILS_ENABLED=true to enable")
		return nil
	}

	messagingSource := os.Getenv("MESSAGING_SOURCE")
	if messagingSource == "" {
		messagingSource = "nats"
	}

	switch messagingSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock email sender")
		return nil // notifications are skipped when emailSender is nil
	case "nats":
		slog.InfoContext(ctx, "initializing NATS email sender")
		natsInit(ctx)
		return nats.NewEmailSender(natsClient)
	default:
		log.Fatalf("unsupported messaging source for email sender: %s", messagingSource)
	}

	// unreachable
	return nil
}

// InviteSenderImpl initializes the invite sender for non-LFID users.
// Returns nil (disabling all outbound invites) when INVITES_ENABLED is not "true".
func InviteSenderImpl(ctx context.Context) port.InviteSender {
	if os.Getenv("INVITES_ENABLED") != "true" {
		slog.InfoContext(ctx, "outbound invites disabled — set INVITES_ENABLED=true to enable")
		return nil
	}

	messagingSource := os.Getenv("MESSAGING_SOURCE")
	if messagingSource == "" {
		messagingSource = "nats"
	}

	switch messagingSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock invite sender")
		return nil // invites are skipped when inviteSender is nil
	case "nats":
		slog.InfoContext(ctx, "initializing NATS invite sender")
		natsInit(ctx)
		return nats.NewInviteSender(natsClient)
	default:
		log.Fatalf("unsupported messaging source for invite sender: %s", messagingSource)
	}

	// unreachable
	return nil
}

// LFXSelfServeBaseURL derives the LFX Self-Serve base URL from environment variables.
// LFX_SELF_SERVE_BASE_URL takes precedence; otherwise it falls back to LFX_ENVIRONMENT.
func LFXSelfServeBaseURL() string {
	if url := os.Getenv("LFX_SELF_SERVE_BASE_URL"); url != "" {
		return url
	}
	switch os.Getenv("LFX_ENVIRONMENT") {
	case "prod":
		return "https://app.lfx.dev"
	case "staging", "stg":
		return "https://app.staging.lfx.dev"
	default:
		return "https://app.dev.lfx.dev"
	}
}

// GroupWeeklyBriefReaderImpl initializes the working-group weekly brief reader.
// Phase 1 only supports the NATS-backed implementation; the storage struct
// already satisfies port.GroupWeeklyBriefReader.
func GroupWeeklyBriefReaderImpl(ctx context.Context) port.GroupWeeklyBriefReader {
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock group weekly brief reader")
		// Phase 1 has no mock — return an always-miss stub so the mock
		// REPOSITORY_SOURCE still produces a working HTTP service.
		return &alwaysMissGroupWeeklyBriefReader{}

	case "nats":
		slog.InfoContext(ctx, "initializing NATS group weekly brief reader")
		natsInit(ctx)
		if natsClient == nil {
			log.Fatalf("failed to initialize NATS client for weekly brief reader")
		}
		reader, ok := natsStorage.(port.GroupWeeklyBriefReader)
		if !ok {
			log.Fatalf("NATS storage does not implement GroupWeeklyBriefReader")
		}
		return reader

	default:
		log.Fatalf("unsupported group weekly brief reader implementation: %s", repoSource)
	}

	// unreachable
	return nil
}

// tokenExpiryLeeway shrinks the refresh window so we never present a token
// that's about to expire in flight. Matches the meeting-service ITX proxy.
const tokenExpiryLeeway = 60 * time.Second

// auth0TokenSource implements oauth2.TokenSource backed by an Auth0 SDK
// authentication client configured with a private-key JWT client assertion.
// Wrap with oauth2.ReuseTokenSource to get caching + automatic refresh.
type auth0TokenSource struct {
	ctx        context.Context
	authConfig *authentication.Authentication
	audience   string
}

// Token issues a client-credentials request, signing the assertion JWT with
// the configured RSA private key.
func (a *auth0TokenSource) Token() (*oauth2.Token, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.TODO()
	}
	tokenSet, err := a.authConfig.OAuth.LoginWithClientCredentials(ctx,
		oauth.LoginWithClientCredentialsRequest{Audience: a.audience},
		oauth.IDTokenValidationOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain M2M token from Auth0: %w", err)
	}
	return &oauth2.Token{
		AccessToken: tokenSet.AccessToken,
		TokenType:   tokenSet.TokenType,
		Expiry:      time.Now().Add(time.Duration(tokenSet.ExpiresIn)*time.Second - tokenExpiryLeeway),
	}, nil
}

// m2mHTTPClient builds the *http.Client used to call other LFX services on
// behalf of THIS service identity (NOT the caller's bearer token). The
// returned client transparently issues an Auth0 OAuth2 client-credentials
// request, signing the assertion JWT with the configured RSA private key
// (RS256), and refreshes the access token as needed.
//
// Env vars for live mode (required when QUERY_SERVICE_URL is set, except where noted):
//   - M2M_AUTH_CLIENT_ID       (required)
//   - M2M_AUTH_PRIVATE_KEY     (required; RSA private key in PEM format)
//   - M2M_AUTH_DOMAIN          (required; Auth0 domain, e.g. "linuxfoundation-dev.auth0.com")
//   - M2M_AUTH_AUDIENCE        (optional)
//
// Behaviour:
//   - When QUERY_SERVICE_URL is unset, the *SourceImpl callers short-circuit
//     to "no results" without making outbound requests; we return a plain
//     *http.Client so wiring stays uniform and the service still boots.
//   - When QUERY_SERVICE_URL IS set, M2M is required — we will be making
//     outbound calls and must not do so unauthenticated. Missing credentials
//     fail-fast at startup to prevent silent identity-less upstream calls.
func m2mHTTPClient(ctx context.Context) *http.Client {
	clientID := os.Getenv("M2M_AUTH_CLIENT_ID")
	privateKey := os.Getenv("M2M_AUTH_PRIVATE_KEY")
	domain := os.Getenv("M2M_AUTH_DOMAIN")
	audience := os.Getenv("M2M_AUTH_AUDIENCE")

	queryURL := os.Getenv("QUERY_SERVICE_URL")
	if clientID == "" || privateKey == "" || domain == "" {
		if queryURL != "" {
			// QUERY_SERVICE_URL is set but M2M is incomplete — refuse to issue
			// unauthenticated upstream calls. (Don't log the private key.)
			log.Fatalf(
				"QUERY_SERVICE_URL is set but M2M credentials are missing — refusing to issue unauthenticated upstream calls. "+
					"Set M2M_AUTH_CLIENT_ID, M2M_AUTH_PRIVATE_KEY (RSA PEM), M2M_AUTH_DOMAIN (and optionally M2M_AUTH_AUDIENCE), or unset QUERY_SERVICE_URL. "+
					"Got client_id_set=%t, private_key_set=%t, domain_set=%t",
				clientID != "", privateKey != "", domain != "",
			)
		}
		slog.WarnContext(ctx, "QUERY_SERVICE_URL not set; M2M HTTP client unused (returning unauthenticated client for uniform wiring)",
			"client_id_set", clientID != "",
			"private_key_set", privateKey != "",
			"domain_set", domain != "",
		)
		return &http.Client{Timeout: 15 * time.Second}
	}

	authConfig, err := authentication.New(ctx,
		domain,
		authentication.WithClientID(clientID),
		authentication.WithClientAssertion(privateKey, "RS256"),
	)
	if err != nil {
		// PrivateKey must be a valid RSA PEM; surface a clear message rather
		// than letting later token requests fail opaquely.
		log.Fatalf("failed to initialize Auth0 M2M client (ensure M2M_AUTH_PRIVATE_KEY contains a valid RSA private key in PEM format): %v", err)
	}
	tokenSource := oauth2.ReuseTokenSource(nil, &auth0TokenSource{
		ctx:        ctx,
		authConfig: authConfig,
		audience:   audience,
	})

	slog.InfoContext(ctx, "initialized M2M HTTP client with Auth0 private-key JWT assertion",
		"domain", domain,
		"audience_set", audience != "",
	)
	httpClient := oauth2.NewClient(ctx, tokenSource)
	httpClient.Timeout = 15 * time.Second
	return httpClient
}

// Live query-service sources share the same base URL and M2M HTTP client.
//
//   - QUERY_SERVICE_URL — base URL for all query-service calls. When empty,
//     every source degrades to "no results".
//   - QUERY_MAILING_LIST_TYPE — overrides the resource type queried by the
//     mailing-list source (defaults to m2m.DefaultMailingListType).
//   - QUERY_VOTE_TYPE — overrides the resource type queried by the vote
//     source (defaults to m2m.DefaultVoteType).
//
// The meeting source's resource type is fixed to "v1_past_meeting".

// MeetingSourceImpl builds the meeting source. When QUERY_SERVICE_URL is
// unset the resulting source returns zero meetings (graceful degrade).
func MeetingSourceImpl(ctx context.Context) port.MeetingSource {
	baseURL := os.Getenv("QUERY_SERVICE_URL")
	if baseURL == "" {
		slog.WarnContext(ctx, "QUERY_SERVICE_URL not set; meeting source will return zero meetings")
	}
	client := m2mHTTPClient(ctx)
	return m2m.NewMeetingSource(m2m.MeetingSourceConfig{
		BaseURL: baseURL,
		Timeout: 15 * time.Second,
	}, client)
}

// MailingListSourceImpl builds the live mailing-list source. When
// QUERY_SERVICE_URL is unset the source returns zero threads (graceful
// degrade). The query-service resource type defaults to
// m2m.DefaultMailingListType and can be overridden via QUERY_MAILING_LIST_TYPE.
func MailingListSourceImpl(ctx context.Context) port.MailingListSource {
	baseURL := os.Getenv("QUERY_SERVICE_URL")
	if baseURL == "" {
		slog.WarnContext(ctx, "QUERY_SERVICE_URL not set; mailing list source will return zero threads")
	}
	client := m2mHTTPClient(ctx)
	return m2m.NewMailingListSource(m2m.MailingListSourceConfig{
		BaseURL: baseURL,
		Type:    os.Getenv("QUERY_MAILING_LIST_TYPE"), // empty → DefaultMailingListType inside NewMailingListSource
		Timeout: 15 * time.Second,
	}, client)
}

// VoteSourceImpl builds the live vote source. When QUERY_SERVICE_URL is unset
// the source returns zero votes (graceful degrade). The query-service resource
// type defaults to m2m.DefaultVoteType and can be overridden via
// QUERY_VOTE_TYPE.
func VoteSourceImpl(ctx context.Context) port.VoteSource {
	baseURL := os.Getenv("QUERY_SERVICE_URL")
	if baseURL == "" {
		slog.WarnContext(ctx, "QUERY_SERVICE_URL not set; vote source will return zero votes")
	}
	client := m2mHTTPClient(ctx)
	return m2m.NewVoteSource(m2m.VoteSourceConfig{
		BaseURL: baseURL,
		Type:    os.Getenv("QUERY_VOTE_TYPE"), // empty → DefaultVoteType inside NewVoteSource
		Timeout: 15 * time.Second,
	}, client)
}

// OrgCommitteeSeatReaderImpl builds the Org Lens org-committee-seat reader. In mock mode it returns
// synthetic sample seats; otherwise it reads the committee-members NATS KV bucket via the
// by-organization secondary index.
func OrgCommitteeSeatReaderImpl(ctx context.Context) port.OrgCommitteeSeatReader {
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}
	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock org committee seat reader")
		return infrastructure.NewMockOrgCommitteeSeatReader()
	case "nats":
		natsInit(ctx)
		memberReader, ok := natsStorage.(port.CommitteeMemberReader)
		if !ok {
			log.Fatalf("NATS storage does not implement CommitteeMemberReader")
		}
		return nats.NewNATSOrgCommitteeSeatReader(memberReader)
	default:
		log.Fatalf("unsupported org committee seat reader implementation: %s", repoSource)
	}
	// unreachable
	return nil
}

// CommitteeWeeklyMemberReaderImpl builds the live weekly member reader. The
// reader is backed by any port.CommitteeMemberReader — in production this is
// the NATS storage adapter — and partitions members by created_at/updated_at
// against the window.
func CommitteeWeeklyMemberReaderImpl(ctx context.Context) port.CommitteeWeeklyMemberReader {
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}
	switch repoSource {
	case "mock":
		return &emptyWeeklyMemberReader{}
	case "nats":
		natsInit(ctx)
		memberReader, ok := natsStorage.(port.CommitteeMemberReader)
		if !ok {
			log.Fatalf("NATS storage does not implement CommitteeMemberReader")
		}
		return nats.NewCommitteeWeeklyMemberReader(memberReader)
	default:
		log.Fatalf("unsupported repository source for weekly member reader: %s", repoSource)
	}
	return nil
}

// GroupWeeklyBriefWriterImpl returns the persistence port the generator uses
// for brief + throttle writes.
func GroupWeeklyBriefWriterImpl(ctx context.Context) port.GroupWeeklyBriefWriter {
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}
	switch repoSource {
	case "mock":
		return &inMemoryGroupWeeklyBriefWriter{}
	case "nats":
		natsInit(ctx)
		writer, ok := natsStorage.(port.GroupWeeklyBriefWriter)
		if !ok {
			log.Fatalf("NATS storage does not implement GroupWeeklyBriefWriter")
		}
		return writer
	default:
		log.Fatalf("unsupported repository source for weekly brief writer: %s", repoSource)
	}
	return nil
}

// emptyWeeklyMemberReader is the mock-mode fallback: no joins, no updates.
type emptyWeeklyMemberReader struct{}

func (emptyWeeklyMemberReader) ListMemberActivityForWindow(_ context.Context, _ string, _, _ time.Time) (port.WeeklyMemberActivity, error) {
	return port.WeeklyMemberActivity{}, nil
}

// inMemoryGroupWeeklyBriefWriter is the mock-mode fallback. It never persists
// but satisfies the interface so the orchestrator can run end-to-end.
type inMemoryGroupWeeklyBriefWriter struct{}

func (inMemoryGroupWeeklyBriefWriter) PutGroupWeeklyBrief(_ context.Context, b *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	if b.UID == "" {
		b.UID = "mock-" + b.CommitteeUID
	}
	b.Revision++
	return b, nil
}

func (inMemoryGroupWeeklyBriefWriter) GetGroupWeeklyBriefThrottle(_ context.Context, _ string, _ time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	return nil, nil
}

func (inMemoryGroupWeeklyBriefWriter) PutGroupWeeklyBriefThrottle(_ context.Context, t *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	t.Revision++
	return t, nil
}

// alwaysMissGroupWeeklyBriefReader is a stub used when REPOSITORY_SOURCE=mock
// — it always reports "no brief", which is a valid 200/null response.
type alwaysMissGroupWeeklyBriefReader struct{}

func (alwaysMissGroupWeeklyBriefReader) GetGroupWeeklyBriefForWindow(_ context.Context, _ string, _ model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	return nil, nil, nil
}

// CommitteeDocumentReaderWriterImpl initializes the committee document reader/writer implementation
// using a dedicated infrastructure adapter (not the shared storage struct) so it can be swapped
// to S3 or another backend by adding a new case here without touching domain or service code.
func CommitteeDocumentReaderWriterImpl(ctx context.Context) port.CommitteeDocumentReaderWriter {
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "nats"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee document storage")
		return infrastructure.NewMockDocumentRepository()

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee document storage")
		natsInit(ctx)
		return nats.NewDocumentStorage(natsClient)

	default:
		log.Fatalf("unsupported committee document storage implementation: %s", repoSource)
	}

	// unreachable
	return nil
}

// QueueSubscriptions starts all NATS subscriptions with the provided dependencies
func QueueSubscriptions(ctx context.Context, committeeReader port.CommitteeReader) error {
	slog.InfoContext(ctx, "starting NATS subscriptions")

	// Initialize NATS client first
	natsInit(ctx)

	// Build the weekly-brief generator so the message handler can fulfill async
	// generate requests off the durable stream (same wiring as the HTTP path).
	weeklyBriefGenerator := usecaseSvc.NewGroupWeeklyBriefGeneratorOrchestrator(
		usecaseSvc.WithGroupWeeklyBriefReaderForGenerator(GroupWeeklyBriefReaderImpl(ctx)),
		usecaseSvc.WithGroupWeeklyBriefWriter(GroupWeeklyBriefWriterImpl(ctx)),
		usecaseSvc.WithMeetingSource(MeetingSourceImpl(ctx)),
		usecaseSvc.WithMailingListSource(MailingListSourceImpl(ctx)),
		usecaseSvc.WithVoteSource(VoteSourceImpl(ctx)),
		usecaseSvc.WithCommitteeWeeklyMemberReader(CommitteeWeeklyMemberReaderImpl(ctx)),
		usecaseSvc.WithAIAdapter(AIAdapterImpl(ctx)),
	)

	// Create message handler service
	messageHandlerService := &MessageHandlerService{
		messageHandler: usecaseSvc.NewMessageHandlerOrchestrator(
			usecaseSvc.WithGroupWeeklyBriefGeneratorForMessageHandler(weeklyBriefGenerator),
			usecaseSvc.WithCommitteeReaderForMessageHandler(
				// get the committee reader directly from the repository implementation
				usecaseSvc.NewCommitteeReaderOrchestrator(
					usecaseSvc.WithCommitteeReader(committeeReader),
				),
			),
			usecaseSvc.WithCommitteeWriterOrchestratorForMessageHandler(
				usecaseSvc.NewCommitteeWriterOrchestrator(
					usecaseSvc.WithCommitteeRetriever(committeeReader),
					usecaseSvc.WithCommitteeWriter(CommitteeWriterImpl(ctx)),
					usecaseSvc.WithProjectRetriever(ProjectRetrieverImpl(ctx)),
					usecaseSvc.WithUserReader(UserReaderImpl(ctx)),
					usecaseSvc.WithCommitteePublisher(CommitteePublisherImpl(ctx)),
				),
			),
			usecaseSvc.WithCommitteeWriterForMessageHandler(CommitteeWriterImpl(ctx)),
			usecaseSvc.WithCommitteePublisherForMessageHandler(CommitteePublisherImpl(ctx)),
			usecaseSvc.WithEmailSenderForMessageHandler(EmailSenderImpl(ctx)),
			usecaseSvc.WithInviteSenderForMessageHandler(InviteSenderImpl(ctx)),
			usecaseSvc.WithLFXSelfServeBaseURLForMessageHandler(LFXSelfServeBaseURL()),
			usecaseSvc.WithUserReaderForMessageHandler(UserReaderImpl(ctx)),
			usecaseSvc.WithLinkReaderForMessageHandler(CommitteeLinkReaderWriterImpl(ctx)),
		),
	}

	// Get the NATS client - we need to access it directly
	natsClient := getNATSClient()
	if natsClient == nil {
		return fmt.Errorf("NATS client not initialized")
	}

	// Start subscriptions for each subject
	subjects := map[string]func(context.Context, port.TransportMessenger){
		constants.CommitteeGetNameSubject:            messageHandlerService.HandleMessage,
		constants.CommitteeListMembersSubject:        messageHandlerService.HandleMessage,
		constants.MailingListCommitteeChangedSubject: messageHandlerService.HandleMessage,
		constants.CommitteeUpdatedSubject:            messageHandlerService.HandleMessage,
		constants.CommitteeMemberCreatedSubject:      messageHandlerService.HandleMessage,
		constants.CommitteeMemberDeletedSubject:      messageHandlerService.HandleMessage,
		constants.CommitteeSettingsUpdatedSubject:    messageHandlerService.HandleMessage,
		inviteapi.InviteServiceAcceptedSubject:       messageHandlerService.HandleMessage,
		constants.CommitteeDocumentCreatedSubject:    messageHandlerService.HandleMessage,
		constants.CommitteeLinkCreatedSubject:        messageHandlerService.HandleMessage,
	}

	for subject, handler := range subjects {
		slog.InfoContext(ctx, "subscribing to NATS subject", "subject", subject)
		if _, err := natsClient.SubscribeWithTransportMessenger(ctx, subject, constants.CommitteeAPIQueue, handler); err != nil {
			slog.ErrorContext(ctx, "failed to subscribe to NATS subject",
				"error", err,
				"subject", subject,
			)
			return fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
		}
	}

	streamConsumers := map[string]func(context.Context, port.StreamMessenger) error{
		constants.ConsumerNameTotalMembersSync: messageHandlerService.messageHandler.HandleCommitteeTotalMembersSync,
	}

	for consumer, handler := range streamConsumers {
		slog.InfoContext(ctx, "starting stream consumer", "consumer", consumer)
		if _, err := natsClient.StartCommitteeMemberConsumer(ctx, handler); err != nil {
			slog.ErrorContext(ctx, "failed to start stream consumer",
				"error", err,
				"consumer", consumer,
			)
			return fmt.Errorf("failed to start stream consumer %s: %w", consumer, err)
		}
	}

	// Start the durable consumer that fulfills async weekly-brief generation.
	slog.InfoContext(ctx, "starting stream consumer", "consumer", constants.ConsumerNameWeeklyBriefGenerate)
	if _, err := natsClient.StartWeeklyBriefGenerateConsumer(ctx, messageHandlerService.messageHandler.HandleGenerateWeeklyBriefRequested); err != nil {
		slog.ErrorContext(ctx, "failed to start weekly-brief generate consumer", "error", err)
		return fmt.Errorf("failed to start weekly-brief generate consumer: %w", err)
	}

	slog.InfoContext(ctx, "NATS subscriptions started successfully")
	return nil
}

// getNATSClient returns the initialized NATS client
// This is a helper function to access the client for subscription management
func getNATSClient() *nats.NATSClient {
	return natsClient
}
