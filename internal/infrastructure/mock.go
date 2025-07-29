// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package infrastructure

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// Global mock repository instance to share data between all repositories
var (
	globalMockRepo     *MockRepository
	globalMockRepoOnce = &sync.Once{}
)

// NewMockRepository creates a new mock repository with sample data
func NewMockRepository() *MockRepository {

	globalMockRepoOnce.Do(func() {
		now := time.Now()

		mock := &MockRepository{
			committees:        make(map[string]*model.Committee),
			committeeSettings: make(map[string]*model.CommitteeSettings),
			projectSlugs:      make(map[string]string),
		}

		// Add some sample data
		sampleCommittee := &model.Committee{
			UID:             "committee-1",
			ProjectUID:      "7cad5a8d-19d0-41a4-81a6-043453daf9ee",
			Name:            "Technical Advisory Committee",
			Category:        "governance",
			Description:     "Main technical governance body for the project",
			Website:         stringPtr("https://example.com/tac"),
			EnableVoting:    true,
			SSOGroupEnabled: true,
			SSOGroupName:    "7cad5a8d-19d0-41a4-81a6-043453daf9ee-technical-advisory-committee-1",
			RequiresReview:  false,
			Public:          false,
			Calendar: model.Calendar{
				Public: true,
			},
			DisplayName:      "TAC",
			ParentUID:        nil,
			TotalMembers:     5,
			TotalVotingRepos: 3,
			CreatedAt:        now.Add(-24 * time.Hour),
			UpdatedAt:        now,
			CommitteeSettings: model.CommitteeSettings{
				CommitteeUID:          "committee-1",
				BusinessEmailRequired: true,
				LastReviewedAt:        &now,
				LastReviewedBy:        stringPtr("admin@example.com"),
				Writers:               []string{"writer1@example.com", "writer2@example.com"},
				Auditors:              []string{"auditor1@example.com"},
				CreatedAt:             now.Add(-24 * time.Hour),
				UpdatedAt:             now,
			},
		}

		mock.committees[sampleCommittee.UID] = sampleCommittee
		mock.committeeSettings[sampleCommittee.UID] = &sampleCommittee.CommitteeSettings
		mock.projectSlugs["7cad5a8d-19d0-41a4-81a6-043453daf9ee"] = "sample-project"

		// Add another sample committee
		sampleCommittee2 := &model.Committee{
			UID:             "committee-2",
			ProjectUID:      "7cad5a8d-19d0-41a4-81a6-043453daf9ee",
			Name:            "Security Committee",
			Category:        "security",
			Description:     "Handles security-related matters",
			EnableVoting:    false,
			SSOGroupEnabled: true,
			SSOGroupName:    "7cad5a8d-19d0-41a4-81a6-043453daf9ee-security-committee-1",
			RequiresReview:  true,
			Public:          true,
			Calendar: model.Calendar{
				Public: false,
			},
			DisplayName:      "Security",
			TotalMembers:     3,
			TotalVotingRepos: 1,
			CreatedAt:        now.Add(-12 * time.Hour),
			UpdatedAt:        now,
			CommitteeSettings: model.CommitteeSettings{
				CommitteeUID:          "committee-2",
				BusinessEmailRequired: false,
				Writers:               []string{"security@example.com"},
				Auditors:              []string{"auditor1@example.com"},
				CreatedAt:             now.Add(-12 * time.Hour),
				UpdatedAt:             now,
			},
		}

		mock.committees[sampleCommittee2.UID] = sampleCommittee2
		mock.committeeSettings[sampleCommittee2.UID] = &sampleCommittee2.CommitteeSettings
		globalMockRepo = mock
	})

	return globalMockRepo
}

// MockRepository provides a mock implementation of all repository interfaces for testing
type MockRepository struct {
	committees        map[string]*model.Committee
	committeeSettings map[string]*model.CommitteeSettings
	projectSlugs      map[string]string // projectUID -> slug
}

// Base returns the mock committee base retriever
func (m *MockRepository) Base() port.CommitteeBaseRetriever {
	return &mockCommitteeBaseRetriever{mock: m}
}

// Settings returns the mock committee settings retriever
func (m *MockRepository) Settings() port.CommitteeSettingsRetriever {
	return &mockCommitteeSettingsRetriever{mock: m}
}

// mockCommitteeBaseRetriever implements CommitteeBaseRetriever
type mockCommitteeBaseRetriever struct {
	mock *MockRepository
}

// Get retrieves a committee by UID
func (r *mockCommitteeBaseRetriever) Get(ctx context.Context, uid string) (*model.Committee, error) {
	slog.DebugContext(ctx, "mock committee base retriever: getting committee", "uid", uid)

	committee, exists := r.mock.committees[uid]
	if !exists {
		return nil, errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Return a copy to avoid data races
	committeeCopy := *committee
	return &committeeCopy, nil
}

// ByNameProject retrieves a committee by name and project UID
func (r *mockCommitteeBaseRetriever) ByNameProject(ctx context.Context, name, projectUID string) (*model.Committee, error) {
	slog.DebugContext(ctx, "mock committee base retriever: getting committee by name and project",
		"name", name, "project_uid", projectUID)

	for _, committee := range r.mock.committees {
		if committee.Name == name && committee.ProjectUID == projectUID {
			committeeCopy := *committee
			return &committeeCopy, nil
		}
	}

	return nil, errors.NewNotFound(fmt.Sprintf("committee with name %s in project %s not found", name, projectUID))
}

// BySSOGroupName retrieves a committee by SSO group name
func (r *mockCommitteeBaseRetriever) BySSOGroupName(ctx context.Context, name string) (*model.Committee, error) {
	slog.DebugContext(ctx, "mock committee base retriever: getting committee by SSO group name", "name", name)

	for _, committee := range r.mock.committees {
		if committee.SSOGroupName == name {
			committeeCopy := *committee
			return &committeeCopy, nil
		}
	}

	return nil, errors.NewNotFound(fmt.Sprintf("committee with SSO group name %s not found", name))
}

// mockCommitteeSettingsRetriever implements CommitteeSettingsRetriever
type mockCommitteeSettingsRetriever struct {
	mock *MockRepository
}

// Get retrieves committee settings by committee UID
func (r *mockCommitteeSettingsRetriever) Get(ctx context.Context, committeeUID string) (*model.CommitteeSettings, error) {
	slog.DebugContext(ctx, "mock committee settings retriever: getting settings", "committee_uid", committeeUID)

	settings, exists := r.mock.committeeSettings[committeeUID]
	if !exists {
		return nil, errors.NewNotFound(fmt.Sprintf("committee settings for UID %s not found", committeeUID))
	}

	// Return a copy to avoid data races
	settingsCopy := *settings
	return &settingsCopy, nil
}

// MockCommitteeWriter implements CommitteeWriter interface
type MockCommitteeWriter struct {
	mock *MockRepository
}

// Base returns the mock committee base writer
func (w *MockCommitteeWriter) Base() port.CommitteeBaseWriter {
	return &mockCommitteeBaseWriter{mock: w.mock}
}

// Settings returns the mock committee settings writer
func (w *MockCommitteeWriter) Settings() port.CommitteeSettingsWriter {
	return &mockCommitteeSettingsWriter{mock: w.mock}
}

// mockCommitteeBaseWriter implements CommitteeBaseWriter
type mockCommitteeBaseWriter struct {
	mock *MockRepository
}

// Create creates a new committee
func (w *mockCommitteeBaseWriter) Create(ctx context.Context, committee *model.Committee) error {
	slog.DebugContext(ctx, "mock committee base writer: creating committee")

	committee.UID = uuid.New().String()

	now := time.Now()
	committee.CreatedAt = now
	committee.UpdatedAt = now

	// Create committee settings as well
	committee.CommitteeSettings.CommitteeUID = committee.UID
	committee.CommitteeSettings.CreatedAt = now
	committee.CommitteeSettings.UpdatedAt = now

	// Store committee and settings
	w.mock.committees[committee.UID] = committee
	w.mock.committeeSettings[committee.UID] = &committee.CommitteeSettings

	return nil
}

// Update updates an existing committee
func (w *mockCommitteeBaseWriter) Update(ctx context.Context, committee *model.Committee) error {
	slog.DebugContext(ctx, "mock committee base writer: updating committee", "uid", committee.UID)

	// Check if committee exists
	if _, exists := w.mock.committees[committee.UID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", committee.UID))
	}

	committee.UpdatedAt = time.Now()
	w.mock.committees[committee.UID] = committee

	return nil
}

// Delete deletes a committee and its settings
func (w *mockCommitteeBaseWriter) Delete(ctx context.Context, uid string) error {
	slog.DebugContext(ctx, "mock committee base writer: deleting committee", "uid", uid)

	// Check if committee exists
	if _, exists := w.mock.committees[uid]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Delete committee and its settings
	delete(w.mock.committees, uid)
	delete(w.mock.committeeSettings, uid)

	return nil
}

// mockCommitteeSettingsWriter implements CommitteeSettingsWriter
type mockCommitteeSettingsWriter struct {
	mock *MockRepository
}

// Update updates committee settings
func (w *mockCommitteeSettingsWriter) Update(ctx context.Context, settings *model.CommitteeSettings) error {
	slog.DebugContext(ctx, "mock committee settings writer: updating settings", "committee_uid", settings.CommitteeUID)

	// Check if committee settings exist
	if _, exists := w.mock.committeeSettings[settings.CommitteeUID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee settings for UID %s not found", settings.CommitteeUID))
	}

	settings.UpdatedAt = time.Now()
	w.mock.committeeSettings[settings.CommitteeUID] = settings

	// Also update the settings in the committee
	if committee, exists := w.mock.committees[settings.CommitteeUID]; exists {
		committee.CommitteeSettings = *settings
		committee.UpdatedAt = time.Now()
	}

	return nil
}

// MockProjectRetriever implements ProjectRetriever interface
type MockProjectRetriever struct {
	mock *MockRepository
}

// Slug returns the project slug for a given UID
func (r *MockProjectRetriever) Slug(ctx context.Context, uid string) (string, error) {
	slog.DebugContext(ctx, "mock project retriever: getting slug", "uid", uid)

	slug, exists := r.mock.projectSlugs[uid]
	if !exists {
		return "", errors.NewNotFound(fmt.Sprintf("project with UID %s not found", uid))
	}

	return slug, nil
}

// Helper functions

// NewMockCommitteeRetriever creates a mock committee retriever
func NewMockCommitteeRetriever(mock *MockRepository) port.CommitteeRetriever {
	return mock
}

// NewMockCommitteeWriter creates a mock committee writer
func NewMockCommitteeWriter(mock *MockRepository) port.CommitteeWriter {
	return &MockCommitteeWriter{mock: mock}
}

// NewMockProjectRetriever creates a mock project retriever
func NewMockProjectRetriever(mock *MockRepository) port.ProjectRetriever {
	return &MockProjectRetriever{mock: mock}
}

// Utility functions for mock data manipulation

// AddCommittee adds a committee to the mock data (useful for testing)
func (m *MockRepository) AddCommittee(committee *model.Committee) {
	m.committees[committee.UID] = committee
	m.committeeSettings[committee.UID] = &committee.CommitteeSettings
}

// AddProjectSlug adds a project slug mapping (useful for testing)
func (m *MockRepository) AddProjectSlug(uid, slug string) {
	m.projectSlugs[uid] = slug
}

// ClearAll clears all mock data (useful for testing)
func (m *MockRepository) ClearAll() {
	m.committees = make(map[string]*model.Committee)
	m.committeeSettings = make(map[string]*model.CommitteeSettings)
	m.projectSlugs = make(map[string]string)
}

// GetCommitteeCount returns the total number of committees
func (m *MockRepository) GetCommitteeCount() int {
	return len(m.committees)
}

// stringPtr is a helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
