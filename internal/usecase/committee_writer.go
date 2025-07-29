// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package usecase

import (
	"context"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// CommitteeWriter defines the interface for committee write operations
type CommitteeWriter interface {
	// Create inserts a new committee into the storage, along with its settings, when applicable
	Create(ctx context.Context, committee *model.Committee) (*model.Committee, error)
}

// committeeWriterOrchestratorOption defines a function type for setting options
type committeeWriterOrchestratorOption func(*committeeWriterOrchestrator)

// WithCommitteeWriter sets the committee writer
func WithCommitteeWriter(writer port.CommitteeWriter) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.committeeWriter = writer
	}
}

// WithCommitteeRetriever sets the committee retriever
func WithCommitteeRetriever(retriever port.CommitteeRetriever) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.committeeRetriever = retriever
	}
}

// WithProjectRetriever sets the project retriever
func WithProjectRetriever(retriever port.ProjectRetriever) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.projectRetriever = retriever
	}
}

// committeeWriterOrchestrator orchestrates the committee creation process
type committeeWriterOrchestrator struct {
	committeeWriter    port.CommitteeWriter
	committeeRetriever port.CommitteeRetriever
	projectRetriever   port.ProjectRetriever
}

// Execute orchestrates the committee creation process
func (uc *committeeWriterOrchestrator) Create(ctx context.Context, committee *model.Committee) (*model.Committee, error) {

	slog.DebugContext(ctx, "executing create committee use case",
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	// Check project exists
	slug, err := uc.projectRetriever.Slug(ctx, committee.ProjectUID)
	if err != nil {
		slog.ErrorContext(ctx, "project not found",
			"error", err,
			"project_uid", committee.ProjectUID,
		)
		return nil, errors.NewNotFound("project not found", err)
	}
	slog.DebugContext(ctx, "project found",
		"project_uid", committee.ProjectUID,
		"project_name", slug,
	)

	// Check parent committee exists (if specified)
	if committee.ParentUID != nil && *committee.ParentUID != "" {
		parent, err := uc.committeeRetriever.Base().Get(ctx, *committee.ParentUID)
		if err != nil {
			slog.ErrorContext(ctx, "parent committee not found",
				"error", err,
				"parent_uid", *committee.ParentUID,
			)
			return nil, errors.NewNotFound("parent committee not found", err)
		}
		slog.DebugContext(ctx, "parent committee found",
			"parent_uid", parent.UID,
			"parent_name", parent.Name,
			"parent_project_uid", parent.ProjectUID,
		)
	}

	// Check SSO group exists (if specified)
	if committee.SSOGroupEnabled {

		for {

			errSSOGroupNameBuild := committee.SSOGroupNameBuild(ctx, slug)
			if errSSOGroupNameBuild != nil {
				slog.ErrorContext(ctx, "failed to build SSO group name",
					"error", errSSOGroupNameBuild,
					"project_slug", slug,
					"committee_name", committee.Name,
				)
				return nil, errors.NewUnexpected("failed to build SSO group name", errSSOGroupNameBuild)
			}

			slog.DebugContext(ctx, "checking if SSO group exists",
				"sso_group_name", committee.SSOGroupName,
			)

			existing, errBySSOGroupName := uc.committeeRetriever.Base().BySSOGroupName(ctx, committee.SSOGroupName)
			if errBySSOGroupName != nil {
				slog.ErrorContext(ctx, "failed to check SSO group existence",
					"error", errBySSOGroupName,
					"sso_group_name", committee.SSOGroupName,
				)
				return nil, errors.NewUnexpected("failed to check SSO group existence", errBySSOGroupName)
			}

			if existing == nil {
				slog.DebugContext(ctx, "SSO group does not exist, proceeding with creation",
					"sso_group_name", committee.SSOGroupName,
				)
				break
			}
		}
	}

	slog.InfoContext(ctx, "committee created successfully",
		"committee_uid", committee.UID,
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	return committee, nil
}

// NewcommitteeWriterOrchestrator creates a new create committee use case using the option pattern
func NewcommitteeWriterOrchestrator(opts ...committeeWriterOrchestratorOption) CommitteeWriter {
	uc := &committeeWriterOrchestrator{}
	for _, opt := range opts {
		opt(uc)
	}
	return uc
}
