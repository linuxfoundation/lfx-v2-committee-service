// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// TestMockCommitteeWriter implements proper reservation logic for testing
type TestMockCommitteeWriter struct {
	mock         *mock.MockRepository
	reservations map[string]string // key -> reservationID for rollback
}

func NewTestMockCommitteeWriter(mockRepo *mock.MockRepository) *TestMockCommitteeWriter {
	return &TestMockCommitteeWriter{
		mock:         mockRepo,
		reservations: make(map[string]string),
	}
}

func (w *TestMockCommitteeWriter) Create(ctx context.Context, committee *model.Committee) error {
	// Generate UID if not set
	if committee.CommitteeBase.UID == "" {
		committee.CommitteeBase.UID = uuid.New().String()
	}

	now := time.Now()
	committee.CommitteeBase.CreatedAt = now
	committee.CommitteeBase.UpdatedAt = now

	// Create committee settings as well
	if committee.CommitteeSettings != nil {
		committee.CommitteeSettings.UID = committee.CommitteeBase.UID
		committee.CommitteeSettings.CreatedAt = now
		committee.CommitteeSettings.UpdatedAt = now
	}

	// Store committee and settings
	w.mock.AddCommittee(committee)
	return nil
}

func (w *TestMockCommitteeWriter) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.mock)
	return mockWriter.UpdateBase(ctx, committee, revision)
}

func (w *TestMockCommitteeWriter) Delete(ctx context.Context, uid string, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.mock)
	return mockWriter.Delete(ctx, uid, revision)
}

func (w *TestMockCommitteeWriter) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.mock)
	return mockWriter.UpdateSetting(ctx, settings, revision)
}

// UniqueNameProject reserves a unique name/project combination
func (w *TestMockCommitteeWriter) UniqueNameProject(ctx context.Context, committee *model.Committee) (string, error) {
	nameProjectKey := committee.BuildIndexKey(ctx)

	// Use the mock's existing logic but invert the result for proper reservation behavior
	mockWriter := mock.NewMockCommitteeWriter(w.mock)
	existingUID, err := mockWriter.UniqueNameProject(ctx, committee)

	// If we get a conflict error, that means it already exists - return the conflict
	if err != nil {
		var conflictErr errs.Conflict
		if errors.As(err, &conflictErr) {
			return existingUID, err
		}
		// If it's a "not found" error, that means it's unique - we can reserve it
		reservationID := uuid.New().String()
		w.reservations[nameProjectKey] = reservationID
		return reservationID, nil
	}

	// Should not reach here with the current mock implementation
	return existingUID, err
}

// UniqueSSOGroupName reserves a unique SSO group name
func (w *TestMockCommitteeWriter) UniqueSSOGroupName(ctx context.Context, committee *model.Committee) (string, error) {
	// Use the mock's existing logic but invert the result for proper reservation behavior
	mockWriter := mock.NewMockCommitteeWriter(w.mock)
	existingUID, err := mockWriter.UniqueSSOGroupName(ctx, committee)

	// If we get a conflict error, that means it already exists - return the conflict
	if err != nil {
		var conflictErr errs.Conflict
		if errors.As(err, &conflictErr) {
			return existingUID, err
		}
		// If it's a "not found" error, that means it's unique - we can reserve it
		ssoKey := "sso:" + committee.SSOGroupName
		reservationID := uuid.New().String()
		w.reservations[ssoKey] = reservationID
		return reservationID, nil
	}

	// Should not reach here with the current mock implementation
	return existingUID, err
}

func TestCommitteeWriterOrchestrator_Create(t *testing.T) {
	testCases := []struct {
		name           string
		setupMock      func(*mock.MockRepository)
		inputCommittee *model.Committee
		expectedError  error
		validate       func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository)
	}{
		{
			name: "successful committee creation without SSO group",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				mockRepo.AddProject("project-1", "test-project", "Test Project")
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "Test Committee",
					Category:        "governance",
					Description:     "A test committee",
					EnableVoting:    true,
					SSOGroupEnabled: false,
					RequiresReview:  false,
					Public:          true,
				},
				CommitteeSettings: &model.CommitteeSettings{
					BusinessEmailRequired: true,
					Writers:               []string{"writer@example.com"},
					Auditors:              []string{"auditor@example.com"},
				},
			},
			expectedError: nil,
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.NotEmpty(t, result.CommitteeBase.UID)
				assert.Equal(t, "project-1", result.ProjectUID)
				assert.Equal(t, "Test Project", result.ProjectName)
				assert.Equal(t, "Test Committee", result.Name)
				assert.Equal(t, 1, mockRepo.GetCommitteeCount())
			},
		},
		{
			name: "successful committee creation with SSO group",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				mockRepo.AddProject("project-1", "test-project", "Test Project")
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "SSO Committee",
					Category:        "technical",
					Description:     "Committee with SSO",
					EnableVoting:    true,
					SSOGroupEnabled: true,
					RequiresReview:  false,
					Public:          false,
				},
				CommitteeSettings: &model.CommitteeSettings{
					BusinessEmailRequired: false,
					Writers:               []string{"writer@example.com"},
				},
			},
			expectedError: nil,
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.NotEmpty(t, result.CommitteeBase.UID)
				assert.NotEmpty(t, result.SSOGroupName)
				assert.Contains(t, result.SSOGroupName, "test-project")
				assert.Contains(t, result.SSOGroupName, "sso-committee")
				assert.Equal(t, 1, mockRepo.GetCommitteeCount())
			},
		},
		{
			name: "successful committee creation with parent committee",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				mockRepo.AddProject("project-1", "test-project", "Test Project")

				// Add parent committee
				parentCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        "parent-committee-1",
						ProjectUID: "project-1",
						Name:       "Parent Committee",
						Category:   "governance",
						CreatedAt:  time.Now().Add(-24 * time.Hour),
						UpdatedAt:  time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:       "parent-committee-1",
						CreatedAt: time.Now().Add(-24 * time.Hour),
						UpdatedAt: time.Now(),
					},
				}
				mockRepo.AddCommittee(parentCommittee)
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "Child Committee",
					Category:        "technical",
					Description:     "Child committee",
					ParentUID:       stringPtr("parent-committee-1"),
					EnableVoting:    false,
					SSOGroupEnabled: false,
					RequiresReview:  true,
					Public:          true,
				},
				CommitteeSettings: &model.CommitteeSettings{
					BusinessEmailRequired: true,
					Writers:               []string{"writer@example.com"},
				},
			},
			expectedError: nil,
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.NotEmpty(t, result.CommitteeBase.UID)
				assert.Equal(t, "parent-committee-1", *result.ParentUID)
				assert.Equal(t, 2, mockRepo.GetCommitteeCount()) // parent + child
			},
		},
		{
			name: "project not found error",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				// Don't add any projects
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "nonexistent-project",
					Name:            "Test Committee",
					Category:        "governance",
					EnableVoting:    true,
					SSOGroupEnabled: false,
				},
				CommitteeSettings: &model.CommitteeSettings{},
			},
			expectedError: errs.NotFound{},
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.Nil(t, result)
				assert.Equal(t, 0, mockRepo.GetCommitteeCount())
			},
		},
		{
			name: "parent committee not found error",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				mockRepo.AddProject("project-1", "test-project", "Test Project")
				// Don't add parent committee
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "Child Committee",
					Category:        "technical",
					ParentUID:       stringPtr("nonexistent-parent"),
					EnableVoting:    false,
					SSOGroupEnabled: false,
				},
				CommitteeSettings: &model.CommitteeSettings{},
			},
			expectedError: errs.NotFound{},
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.Nil(t, result)
				assert.Equal(t, 0, mockRepo.GetCommitteeCount())
			},
		},
		{
			name: "committee name already exists error",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				mockRepo.AddProject("project-1", "test-project", "Test Project")

				// Add existing committee with same name
				existingCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        "existing-committee",
						ProjectUID: "project-1",
						Name:       "Existing Committee",
						Category:   "governance",
						CreatedAt:  time.Now().Add(-24 * time.Hour),
						UpdatedAt:  time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:       "existing-committee",
						CreatedAt: time.Now().Add(-24 * time.Hour),
						UpdatedAt: time.Now(),
					},
				}
				mockRepo.AddCommittee(existingCommittee)
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "Existing Committee", // Same name as existing
					Category:        "technical",
					EnableVoting:    true,
					SSOGroupEnabled: false,
				},
				CommitteeSettings: &model.CommitteeSettings{},
			},
			expectedError: errs.Conflict{},
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.Nil(t, result)
				assert.Equal(t, 1, mockRepo.GetCommitteeCount()) // Only the existing one
			},
		},
		{
			name: "SSO group name conflict with retry logic",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				mockRepo.AddProject("project-1", "test-project", "Test Project")

				// Add existing committee with SSO group that might conflict
				existingCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:             "existing-sso-committee",
						ProjectUID:      "project-1",
						Name:            "Existing SSO Committee",
						Category:        "governance",
						SSOGroupEnabled: true,
						SSOGroupName:    "project-1-existing-sso-committee",
						CreatedAt:       time.Now().Add(-24 * time.Hour),
						UpdatedAt:       time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:       "existing-sso-committee",
						CreatedAt: time.Now().Add(-24 * time.Hour),
						UpdatedAt: time.Now(),
					},
				}
				mockRepo.AddCommittee(existingCommittee)
			},
			inputCommittee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "New SSO Committee",
					Category:        "technical",
					EnableVoting:    true,
					SSOGroupEnabled: true,
				},
				CommitteeSettings: &model.CommitteeSettings{},
			},
			expectedError: nil,
			validate: func(t *testing.T, result *model.Committee, mockRepo *mock.MockRepository) {
				assert.NotEmpty(t, result.CommitteeBase.UID)
				assert.NotEmpty(t, result.SSOGroupName)
				assert.Equal(t, 2, mockRepo.GetCommitteeCount())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			mockRepo := mock.NewMockRepository()
			tc.setupMock(mockRepo)

			committeeReader := mock.NewMockCommitteeReader(mockRepo)
			committeeWriter := NewTestMockCommitteeWriter(mockRepo)
			projectReader := mock.NewMockProjectRetriever(mockRepo)
			committeePublisher := mock.NewMockCommitteePublisher()

			orchestrator := NewCommitteeWriterOrchestrator(
				WithCommitteeRetriever(committeeReader),
				WithCommitteeWriter(committeeWriter),
				WithProjectRetriever(projectReader),
				WithCommitteePublisher(committeePublisher),
			)

			// Execute
			ctx := context.Background()
			result, err := orchestrator.Create(ctx, tc.inputCommittee)

			// Validate
			if tc.expectedError != nil {
				require.Error(t, err)
				assert.IsType(t, tc.expectedError, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}

			tc.validate(t, result, mockRepo)
		})
	}
}

func TestCommitteeWriterOrchestrator_buildIndexerMessage(t *testing.T) {
	testCases := []struct {
		name          string
		committee     any
		tags          []string
		expectedError bool
	}{
		{
			name: "successful indexer message build",
			committee: &model.CommitteeBase{
				UID:        "test-committee",
				ProjectUID: "test-project",
				Name:       "Test Committee",
			},
			tags:          []string{"project_uid:test-project"},
			expectedError: false,
		},
		{
			name:          "build with nil committee",
			committee:     nil,
			tags:          []string{"tag1", "tag2"},
			expectedError: false, // The Build method doesn't validate nil input, it just creates a message with nil data
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			orchestrator := &committeeWriterOrchestrator{}
			ctx := context.Background()

			// Execute
			result, err := orchestrator.buildIndexerMessage(ctx, tc.committee, tc.tags)

			// Validate
			if tc.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, model.ActionCreated, result.Action)
				assert.Equal(t, tc.tags, result.Tags)

				// For nil committee case, validate that data is nil
				if tc.committee == nil {
					assert.Nil(t, result.Data)
				}
			}
		})
	}
}

func TestCommitteeWriterOrchestrator_buildAccessControlMessage(t *testing.T) {
	testCases := []struct {
		name      string
		committee *model.Committee
		expected  *model.CommitteeAccessMessage
	}{
		{
			name: "committee without parent",
			committee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					UID:       "committee-1",
					Public:    true,
					ParentUID: nil,
				},
				CommitteeSettings: &model.CommitteeSettings{
					Writers:  []string{"writer1@example.com", "writer2@example.com"},
					Auditors: []string{"auditor1@example.com"},
				},
			},
			expected: &model.CommitteeAccessMessage{
				UID:       "committee-1",
				Public:    true,
				ParentUID: "",
				Writers:   []string{"writer1@example.com", "writer2@example.com"},
				Auditors:  []string{"auditor1@example.com"},
			},
		},
		{
			name: "committee with parent",
			committee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					UID:       "committee-2",
					Public:    false,
					ParentUID: stringPtr("parent-committee"),
				},
				CommitteeSettings: &model.CommitteeSettings{
					Writers:  []string{"writer@example.com"},
					Auditors: []string{},
				},
			},
			expected: &model.CommitteeAccessMessage{
				UID:       "committee-2",
				Public:    false,
				ParentUID: "parent-committee",
				Writers:   []string{"writer@example.com"},
				Auditors:  []string{},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			orchestrator := &committeeWriterOrchestrator{}
			ctx := context.Background()

			// Execute
			result := orchestrator.buildAccessControlMessage(ctx, tc.committee)

			// Validate
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCommitteeWriterOrchestrator_checkReserveSSOName(t *testing.T) {
	testCases := []struct {
		name          string
		setupMock     func(*mock.MockRepository)
		committee     *model.Committee
		slug          string
		expectedError bool
		validateName  func(t *testing.T, committee *model.Committee)
	}{
		{
			name: "unique SSO group name",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
			},
			committee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					Name: "New Committee",
				},
			},
			slug:          "test-project",
			expectedError: false,
			validateName: func(t *testing.T, committee *model.Committee) {
				assert.Contains(t, committee.SSOGroupName, "test-project")
				assert.Contains(t, committee.SSOGroupName, "new-committee")
			},
		},
		{
			name: "SSO group name conflict with retry",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				// Add committee with potentially conflicting SSO name
				existingCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:             "existing-committee",
						Name:            "Existing Committee",
						SSOGroupEnabled: true,
						SSOGroupName:    "test-project-test-committee",
						CreatedAt:       time.Now(),
						UpdatedAt:       time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:       "existing-committee",
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					},
				}
				mockRepo.AddCommittee(existingCommittee)
			},
			committee: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					Name: "Test Committee",
				},
			},
			slug:          "test-project",
			expectedError: false,
			validateName: func(t *testing.T, committee *model.Committee) {
				assert.NotEmpty(t, committee.SSOGroupName)
				assert.Contains(t, committee.SSOGroupName, "test-project")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			mockRepo := mock.NewMockRepository()
			tc.setupMock(mockRepo)

			committeeWriter := NewTestMockCommitteeWriter(mockRepo)
			orchestrator := &committeeWriterOrchestrator{
				committeeWriter: committeeWriter,
			}

			ctx := context.Background()

			// Execute
			key, err := orchestrator.checkReserveSSOName(ctx, tc.committee, tc.slug)

			// Validate
			if tc.expectedError {
				assert.Error(t, err)
				assert.Empty(t, key)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, key)
				tc.validateName(t, tc.committee)
			}
		})
	}
}

func TestCommitteeWriterOrchestrator_rollback(t *testing.T) {
	testCases := []struct {
		name         string
		setupMock    func(*mock.MockRepository)
		keys         []string
		validateMock func(t *testing.T, mockRepo *mock.MockRepository)
	}{
		{
			name: "successful rollback with existing committees",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
				// Add committees that should be rolled back
				committee1 := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        "committee-to-rollback-1",
						ProjectUID: "project-1",
						Name:       "Committee 1",
						CreatedAt:  time.Now(),
						UpdatedAt:  time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:       "committee-to-rollback-1",
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					},
				}
				committee2 := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        "committee-to-rollback-2",
						ProjectUID: "project-1",
						Name:       "Committee 2",
						CreatedAt:  time.Now(),
						UpdatedAt:  time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:       "committee-to-rollback-2",
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					},
				}
				mockRepo.AddCommittee(committee1)
				mockRepo.AddCommittee(committee2)
			},
			keys: []string{"committee-to-rollback-1", "committee-to-rollback-2"},
			validateMock: func(t *testing.T, mockRepo *mock.MockRepository) {
				// Committees should be deleted during rollback
				assert.Equal(t, 0, mockRepo.GetCommitteeCount())
			},
		},
		{
			name: "rollback with non-existent keys",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
			},
			keys: []string{"nonexistent-key-1", "nonexistent-key-2"},
			validateMock: func(t *testing.T, mockRepo *mock.MockRepository) {
				// No committees to rollback, count should remain 0
				assert.Equal(t, 0, mockRepo.GetCommitteeCount())
			},
		},
		{
			name: "empty keys rollback",
			setupMock: func(mockRepo *mock.MockRepository) {
				mockRepo.ClearAll()
			},
			keys: []string{},
			validateMock: func(t *testing.T, mockRepo *mock.MockRepository) {
				assert.Equal(t, 0, mockRepo.GetCommitteeCount())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			mockRepo := mock.NewMockRepository()
			tc.setupMock(mockRepo)

			committeeReader := mock.NewMockCommitteeReader(mockRepo)
			committeeWriter := mock.NewMockCommitteeWriter(mockRepo)

			orchestrator := &committeeWriterOrchestrator{
				committeeReader: committeeReader,
				committeeWriter: committeeWriter,
			}

			ctx := context.Background()

			// Execute
			orchestrator.rollback(ctx, tc.keys)

			// Validate
			tc.validateMock(t, mockRepo)
		})
	}
}

func TestNewcommitteeWriterOrchestrator(t *testing.T) {
	testCases := []struct {
		name     string
		options  []committeeWriterOrchestratorOption
		validate func(t *testing.T, orchestrator CommitteeWriter)
	}{
		{
			name:    "create with no options",
			options: []committeeWriterOrchestratorOption{},
			validate: func(t *testing.T, orchestrator CommitteeWriter) {
				assert.NotNil(t, orchestrator)
			},
		},
		{
			name: "create with all options",
			options: []committeeWriterOrchestratorOption{
				WithCommitteeRetriever(mock.NewMockCommitteeReader(mock.NewMockRepository())),
				WithCommitteeWriter(mock.NewMockCommitteeWriter(mock.NewMockRepository())),
				WithProjectRetriever(mock.NewMockProjectRetriever(mock.NewMockRepository())),
				WithCommitteePublisher(mock.NewMockCommitteePublisher()),
			},
			validate: func(t *testing.T, orchestrator CommitteeWriter) {
				assert.NotNil(t, orchestrator)
				// Test that Create method is available
				ctx := context.Background()
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						ProjectUID: "test-project",
						Name:       "Test Committee",
					},
					CommitteeSettings: &model.CommitteeSettings{},
				}
				_, err := orchestrator.Create(ctx, committee)
				// We expect an error since project doesn't exist, but method should be callable
				assert.Error(t, err)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Execute
			orchestrator := NewCommitteeWriterOrchestrator(tc.options...)

			// Validate
			tc.validate(t, orchestrator)
		})
	}
}

// Helper functions

func stringPtr(s string) *string {
	return &s
}

// MockCommitteePublisherWithError is a mock publisher that can return errors for testing
type MockCommitteePublisherWithError struct {
	indexerError error
	accessError  error
}

func (p *MockCommitteePublisherWithError) Indexer(ctx context.Context, subject string, message any) error {
	if p.indexerError != nil {
		return p.indexerError
	}
	return nil
}

func (p *MockCommitteePublisherWithError) Access(ctx context.Context, subject string, message any) error {
	if p.accessError != nil {
		return p.accessError
	}
	return nil
}

func TestCommitteeWriterOrchestrator_Create_PublishingErrors(t *testing.T) {
	testCases := []struct {
		name           string
		indexerError   error
		accessError    error
		expectComplete bool // Should committee still be created despite publishing errors?
	}{
		{
			name:           "indexer error does not fail creation",
			indexerError:   errors.New("indexer publishing failed"),
			accessError:    nil,
			expectComplete: true,
		},
		{
			name:           "access error does not fail creation",
			indexerError:   nil,
			accessError:    errors.New("access publishing failed"),
			expectComplete: true,
		},
		{
			name:           "both publishing errors do not fail creation",
			indexerError:   errors.New("indexer publishing failed"),
			accessError:    errors.New("access publishing failed"),
			expectComplete: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			mockRepo := mock.NewMockRepository()
			mockRepo.ClearAll()
			mockRepo.AddProject("project-1", "test-project", "Test Project")

			committeeReader := mock.NewMockCommitteeReader(mockRepo)
			committeeWriter := NewTestMockCommitteeWriter(mockRepo)
			projectReader := mock.NewMockProjectRetriever(mockRepo)

			// Use custom publisher that can return errors
			committeePublisher := &MockCommitteePublisherWithError{
				indexerError: tc.indexerError,
				accessError:  tc.accessError,
			}

			orchestrator := NewCommitteeWriterOrchestrator(
				WithCommitteeRetriever(committeeReader),
				WithCommitteeWriter(committeeWriter),
				WithProjectRetriever(projectReader),
				WithCommitteePublisher(committeePublisher),
			)

			committee := &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-1",
					Name:            "Test Committee",
					Category:        "governance",
					EnableVoting:    true,
					SSOGroupEnabled: false,
				},
				CommitteeSettings: &model.CommitteeSettings{
					BusinessEmailRequired: true,
				},
			}

			// Execute
			ctx := context.Background()
			result, err := orchestrator.Create(ctx, committee)

			// Validate
			if tc.expectComplete {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.NotEmpty(t, result.CommitteeBase.UID)
				assert.Equal(t, 1, mockRepo.GetCommitteeCount())
			} else {
				assert.Error(t, err)
				assert.Nil(t, result)
			}
		})
	}
}
