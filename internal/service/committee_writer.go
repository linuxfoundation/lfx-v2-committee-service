// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
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
func WithCommitteeRetriever(retriever port.CommitteeReader) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.committeeReader = retriever
	}
}

// WithProjectRetriever sets the project retriever
func WithProjectRetriever(retriever port.ProjectReader) committeeWriterOrchestratorOption {
	return func(u *committeeWriterOrchestrator) {
		u.projectRetriever = retriever
	}
}

// committeeWriterOrchestrator orchestrates the committee creation process
type committeeWriterOrchestrator struct {
	projectRetriever port.ProjectReader
	committeeReader  port.CommitteeReader
	committeeWriter  port.CommitteeWriter
}

// rollback undoes the changes made during committee creation
// since the creation can involve multiple steps, this function ensures that if any step fails,
// all previous steps are rolled back to maintain data integrity (it's not atomic)
func (uc *committeeWriterOrchestrator) rollback(ctx context.Context, keys []string) {

	slog.ErrorContext(ctx, "rolling back committee creation due to error",
		"keys", keys,
	)

	for _, key := range keys {
		rev, errGet := uc.committeeReader.GetRevision(ctx, key)
		if errGet != nil {
			slog.ErrorContext(ctx, "failed to get committee for rollback",
				"key", key,
				"error", errGet,
			)
			continue
		}

		err := uc.committeeWriter.Delete(ctx, key, rev)
		if err != nil {
			slog.ErrorContext(ctx, "failed to rollback key",
				"key", key,
				"error", err,
			)
		}
	}

	slog.InfoContext(ctx, "rollback completed",
		"keys", keys,
	)

}

// checkReserveSSOName checks if the SSO group name is unique and reserves it if it is
// It retries until it finds a unique name or returns an error
// This is used to ensure that the SSO group name is unique across all committees
// It builds the SSO group name based on the committee name and project slug
// It returns the unique key corresponding to the SSO group name or an error if it fails to find a unique name
func (uc *committeeWriterOrchestrator) checkReserveSSOName(ctx context.Context, committee *model.Committee, slug string) (string, error) {

	slog.DebugContext(ctx, "checking SSO group name uniqueness",
		"project_slug", slug,
		"committee_name", committee.Name,
	)

	for {

		errSSOGroupNameBuild := committee.SSOGroupNameBuild(ctx, slug)
		if errSSOGroupNameBuild != nil {
			slog.ErrorContext(ctx, "failed to build SSO group name",
				"error", errSSOGroupNameBuild,
				"project_slug", slug,
				"committee_name", committee.Name,
			)
			return "", errs.NewUnexpected("failed to build SSO group name", errSSOGroupNameBuild)
		}

		slog.DebugContext(ctx, "checking if SSO group exists",
			"sso_group_name", committee.SSOGroupName,
		)

		key, errBySSOGroupName := uc.committeeWriter.UniqueSSOGroupName(ctx, committee)
		if errors.As(errBySSOGroupName, &errs.Conflict{}) {
			slog.WarnContext(ctx, "SSO group name already exists, retrying with a new name",
				"sso_group_name", committee.SSOGroupName,
				"existing_uid", key,
			)
			continue
		}

		if errBySSOGroupName != nil {
			return "", errBySSOGroupName
		}

		slog.DebugContext(ctx, "SSO group name is unique, proceeding with creation",
			"sso_group_name", committee.SSOGroupName,
		)
		return key, nil
	}

}

// Execute orchestrates the committee creation process
func (uc *committeeWriterOrchestrator) Create(ctx context.Context, committee *model.Committee) (*model.Committee, error) {

	slog.DebugContext(ctx, "executing create committee use case",
		"project_uid", committee.ProjectUID,
		"name", committee.Name,
	)

	// for rollback purposes
	var (
		keys             []string
		rollbackRequired bool
	)
	defer func() {
		if err := recover(); err != nil || rollbackRequired {
			uc.rollback(ctx, keys)
		}
	}()

	// Check project exists
	slug, errSlug := uc.projectRetriever.Slug(ctx, committee.ProjectUID)
	if errSlug != nil {
		slog.ErrorContext(ctx, "failed to retrieve project slug",
			"error", errSlug,
			"project_uid", committee.ProjectUID,
		)
		return nil, errSlug
	}
	projectName, errProjectName := uc.projectRetriever.Name(ctx, committee.ProjectUID)
	if errProjectName != nil {
		slog.ErrorContext(ctx, "failed to retrieve project name",
			"error", errProjectName,
			"project_uid", committee.ProjectUID,
		)
		return nil, errProjectName
	}
	committee.ProjectName = projectName

	slog.DebugContext(ctx, "project found",
		"project_uid", committee.ProjectUID,
		"project_slug", slug,
		"project_name", projectName,
	)

	// Check parent committee exists (if specified)
	if committee.ParentUID != nil && *committee.ParentUID != "" {
		parent, revision, errParent := uc.committeeReader.GetBase(ctx, *committee.ParentUID)
		if errParent != nil {
			slog.ErrorContext(ctx, "parent committee not found",
				"error", errParent,
				"parent_uid", *committee.ParentUID,
			)
			return nil, errParent
		}
		slog.DebugContext(ctx, "parent committee found",
			"parent_uid", parent.UID,
			"parent_name", parent.Name,
			"revision", revision,
			"parent_project_uid", parent.ProjectUID,
		)
	}

	// Check if the project and committee name already exist
	uniqueNameProjectKey, errUniqueName := uc.committeeWriter.UniqueNameProject(ctx, committee)
	if errUniqueName != nil {
		return nil, errUniqueName
	}
	keys = append(keys, uniqueNameProjectKey)

	// Check SSO group exists (if specified)
	if committee.SSOGroupEnabled {
		uniqueSSOName, errCheckReserveSSOName := uc.checkReserveSSOName(ctx, committee, slug)
		if errCheckReserveSSOName != nil {
			return nil, errCheckReserveSSOName
		}
		keys = append(keys, uniqueSSOName)
	}

	// Create the committee and settings (if applicable)
	errCreate := uc.committeeWriter.Create(ctx, committee)
	if errCreate != nil {
		slog.ErrorContext(ctx, "failed to create committee",
			"error", errCreate,
			"committee_uid", committee.CommitteeBase.UID,
		)
		rollbackRequired = true
		return nil, errCreate
	}

	slog.DebugContext(ctx, "committee created successfully",
		"committee_uid", committee.CommitteeBase.UID,
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
