// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/auth"
	infrastructure "github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
)

var (
	natsStorage   port.CommitteeReaderWriter
	natsMessaging port.ProjectReader

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

		natsClient, errNewClient := nats.NewClient(ctx, config)
		if errNewClient != nil {
			log.Fatalf("failed to create NATS client: %v", errNewClient)
		}
		natsStorage = nats.NewStorage(natsClient)
		natsMessaging = nats.NewMessage(natsClient)
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

// CommitteeReaderImpl initializes the committee reader implementation based on the repository source
func CommitteeReaderImpl(ctx context.Context) port.CommitteeReader {
	var committeeRetriever port.CommitteeReader

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "mock"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee retriever")
		committeeRetriever = infrastructure.NewMockCommitteeReader(infrastructure.NewMockRepository())

	case "nats":
		slog.InfoContext(ctx, "initializing NATS committee retriever")
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
		slog.InfoContext(ctx, "initializing NATS committee retriever")
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
		log.Fatalf("unsupported proejct reader implementation: %s", repoSource)
	}

	return projectReader
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
