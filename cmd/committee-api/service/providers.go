// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure"
)

// CommitteeWriterImpl initializes the committee writer implementation based on the repository source
func CommitteeRetrieverImpl(ctx context.Context) port.CommitteeRetriever {
	var committeeRetriever port.CommitteeRetriever

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "mock"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee retriever")
		committeeRetriever = infrastructure.NewMockCommitteeRetriever(infrastructure.NewMockRepository())

	// Add other repository implementations here (e.g., database, etc.)
	// case "database":
	//     slog.InfoContext(ctx, "initializing database committee retriever")
	//     // Initialize database connection and create database repository
	//     committeeRetriever = database.NewCommitteeRetriever(dbConn)

	default:
		log.Fatalf("unsupported repository implementation: %s", repoSource)
	}

	return committeeRetriever
}

// CommitteeWriterImpl initializes the committee writer implementation based on the repository source
func CommitteeWriterImpl(ctx context.Context) port.CommitteeWriter {
	var committeeWriter port.CommitteeWriter

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "mock"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock committee writer")
		committeeWriter = infrastructure.NewMockCommitteeWriter(infrastructure.NewMockRepository())

	// Add other repository implementations here (e.g., database, etc.)
	// case "database":
	//     slog.InfoContext(ctx, "initializing database committee writer")
	//     // Initialize database connection and create database repository
	//     committeeWriter = database.NewCommitteeWriter(dbConn)

	default:
		log.Fatalf("unsupported repository implementation: %s", repoSource)
	}

	return committeeWriter
}

// ProjectRetrieverImpl initializes the project retriever implementation based on the repository source
func ProjectRetrieverImpl(ctx context.Context) port.ProjectRetriever {
	var projectRetriever port.ProjectRetriever

	// Repository implementation configuration
	repoSource := os.Getenv("REPOSITORY_SOURCE")
	if repoSource == "" {
		repoSource = "mock"
	}

	switch repoSource {
	case "mock":
		slog.InfoContext(ctx, "initializing mock project retriever")
		projectRetriever = infrastructure.NewMockProjectRetriever(infrastructure.NewMockRepository())

	// Add other repository implementations here (e.g., database, etc.)
	// case "database":
	//     slog.InfoContext(ctx, "initializing database project retriever")
	//     // Initialize database connection and create database repository
	//     projectRetriever = database.NewProjectRetriever(dbConn)

	default:
		log.Fatalf("unsupported repository implementation: %s", repoSource)
	}

	return projectRetriever
}
