// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

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
		ctx := context.Background()

		mock := &MockRepository{
			committees:         make(map[string]*model.Committee),
			committeeSettings:  make(map[string]*model.CommitteeSettings),
			projectSlugs:       make(map[string]string),
			committeeIndexKeys: make(map[string]*model.Committee),
		}

		// Add some sample data
		sampleCommittee := &model.Committee{
			CommitteeBase: model.CommitteeBase{
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
			},
			CommitteeSettings: &model.CommitteeSettings{
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
		mock.committeeSettings[sampleCommittee.UID] = sampleCommittee.CommitteeSettings
		mock.projectSlugs["7cad5a8d-19d0-41a4-81a6-043453daf9ee"] = "sample-project"
		mock.committeeIndexKeys[sampleCommittee.BuildIndexKey(ctx)] = sampleCommittee

		// Add another sample committee
		sampleCommittee2 := &model.Committee{
			CommitteeBase: model.CommitteeBase{
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
			},
			CommitteeSettings: &model.CommitteeSettings{
				CommitteeUID:          "committee-2",
				BusinessEmailRequired: false,
				Writers:               []string{"security@example.com"},
				Auditors:              []string{"auditor1@example.com"},
				CreatedAt:             now.Add(-12 * time.Hour),
				UpdatedAt:             now,
			},
		}

		mock.committees[sampleCommittee2.UID] = sampleCommittee2
		mock.committeeSettings[sampleCommittee2.UID] = sampleCommittee2.CommitteeSettings
		mock.committeeIndexKeys[sampleCommittee2.BuildIndexKey(ctx)] = sampleCommittee2
		globalMockRepo = mock
	})

	return globalMockRepo
}

// MockRepository provides a mock implementation of all repository interfaces for testing
type MockRepository struct {
	committees         map[string]*model.Committee
	committeeSettings  map[string]*model.CommitteeSettings
	projectSlugs       map[string]string           // projectUID -> slug
	committeeIndexKeys map[string]*model.Committee // indexKey -> committee
}

// ================== CommitteeBaseReader implementation ==================

// GetBase retrieves a committee base by UID
func (m *MockRepository) GetBase(ctx context.Context, uid string) (*model.Committee, error) {
	slog.DebugContext(ctx, "mock repository: getting committee base", "uid", uid)

	committee, exists := m.committees[uid]
	if !exists {
		return nil, errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Return a copy to avoid data races
	committeeCopy := *committee
	return &committeeCopy, nil
}

// ByNameProject retrieves a committee by name and project UID using index key
func (m *MockRepository) ByNameProject(ctx context.Context, nameProjectKey string) (*model.Committee, error) {
	slog.DebugContext(ctx, "mock repository: getting committee by name project key", "name_project_key", nameProjectKey)

	committee, exists := m.committeeIndexKeys[nameProjectKey]
	if !exists {
		return nil, errors.NewNotFound(fmt.Sprintf("committee with name project key %s not found", nameProjectKey))
	}

	// Return a copy to avoid data races
	committeeCopy := *committee
	return &committeeCopy, nil
}

// BySSOGroupName retrieves a committee by SSO group name
func (m *MockRepository) BySSOGroupName(ctx context.Context, name string) (*model.Committee, error) {
	slog.DebugContext(ctx, "mock repository: getting committee by SSO group name", "name", name)

	for _, committee := range m.committees {
		if committee.SSOGroupName == name {
			committeeCopy := *committee
			return &committeeCopy, nil
		}
	}

	return nil, errors.NewNotFound(fmt.Sprintf("committee with SSO group name %s not found", name))
}

// ================== CommitteeSettingsReader implementation ==================

// GetSettings retrieves committee settings by committee UID
func (m *MockRepository) GetSettings(ctx context.Context, committeeUID string) (*model.CommitteeSettings, error) {
	slog.DebugContext(ctx, "mock repository: getting committee settings", "committee_uid", committeeUID)

	settings, exists := m.committeeSettings[committeeUID]
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

// ================== CommitteeBaseWriter implementation ==================

// Create creates a new committee
func (w *MockCommitteeWriter) Create(ctx context.Context, committee *model.Committee) error {
	slog.DebugContext(ctx, "mock committee writer: creating committee")

	committee.UID = uuid.New().String()

	now := time.Now()
	committee.CommitteeBase.CreatedAt = now
	committee.CommitteeBase.UpdatedAt = now

	// Create committee settings as well
	committee.CommitteeSettings.CommitteeUID = committee.UID
	committee.CommitteeSettings.CreatedAt = now
	committee.CommitteeSettings.UpdatedAt = now

	// Store committee and settings
	w.mock.committees[committee.UID] = committee
	w.mock.committeeSettings[committee.UID] = committee.CommitteeSettings
	w.mock.committeeIndexKeys[committee.BuildIndexKey(ctx)] = committee

	return nil
}

// UpdateBase updates an existing committee base
func (w *MockCommitteeWriter) UpdateBase(ctx context.Context, committee *model.Committee) error {
	slog.DebugContext(ctx, "mock committee writer: updating committee base", "uid", committee.UID)

	// Check if committee exists
	if _, exists := w.mock.committees[committee.UID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", committee.UID))
	}

	committee.CommitteeBase.UpdatedAt = time.Now()
	w.mock.committees[committee.UID] = committee
	w.mock.committeeIndexKeys[committee.BuildIndexKey(ctx)] = committee

	return nil
}

// Delete deletes a committee and its settings
func (w *MockCommitteeWriter) Delete(ctx context.Context, uid string) error {
	slog.DebugContext(ctx, "mock committee writer: deleting committee", "uid", uid)

	// Check if committee exists and get it to obtain the index key
	committee, exists := w.mock.committees[uid]
	if !exists {
		return errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Get the index key before deleting
	indexKey := committee.BuildIndexKey(ctx)

	// Delete committee and its settings
	delete(w.mock.committees, uid)
	delete(w.mock.committeeSettings, uid)
	delete(w.mock.committeeIndexKeys, indexKey)

	return nil
}

// ================== CommitteeSettingsWriter implementation ==================

// UpdateSetting updates committee settings
func (w *MockCommitteeWriter) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings) error {
	slog.DebugContext(ctx, "mock committee writer: updating settings", "committee_uid", settings.CommitteeUID)

	// Check if committee settings exist
	if _, exists := w.mock.committeeSettings[settings.CommitteeUID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee settings for UID %s not found", settings.CommitteeUID))
	}

	settings.UpdatedAt = time.Now()
	w.mock.committeeSettings[settings.CommitteeUID] = settings

	// Also update the settings in the committee
	if committee, exists := w.mock.committees[settings.CommitteeUID]; exists {
		committee.CommitteeSettings = settings
		committee.CommitteeBase.UpdatedAt = time.Now()
		w.mock.committeeIndexKeys[committee.BuildIndexKey(ctx)] = committee
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

// NewMockCommitteeReader creates a mock committee reader
func NewMockCommitteeReader(mock *MockRepository) port.CommitteeReader {
	return mock
}

// NewMockCommitteeWriter creates a mock committee writer
func NewMockCommitteeWriter(mock *MockRepository) port.CommitteeWriter {
	return &MockCommitteeWriter{mock: mock}
}

// NewMockCommitteeReaderWriter creates a mock committee reader writer
func NewMockCommitteeReaderWriter(mock *MockRepository) port.CommitteeReaderWriter {
	return &MockCommitteeReaderWriter{
		CommitteeReader: mock,
		CommitteeWriter: &MockCommitteeWriter{mock: mock},
	}
}

// MockCommitteeReaderWriter combines reader and writer functionality
type MockCommitteeReaderWriter struct {
	port.CommitteeReader
	port.CommitteeWriter
}

// NewMockProjectRetriever creates a mock project retriever
func NewMockProjectRetriever(mock *MockRepository) port.ProjectRetriever {
	return &MockProjectRetriever{mock: mock}
}

// Utility functions for mock data manipulation

// AddCommittee adds a committee to the mock data (useful for testing)
func (m *MockRepository) AddCommittee(committee *model.Committee) {
	m.committees[committee.UID] = committee
	m.committeeSettings[committee.UID] = committee.CommitteeSettings
	m.committeeIndexKeys[committee.BuildIndexKey(context.Background())] = committee
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
	m.committeeIndexKeys = make(map[string]*model.Committee)
}

// GetCommitteeCount returns the total number of committees
func (m *MockRepository) GetCommitteeCount() int {
	return len(m.committees)
}

// stringPtr is a helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
