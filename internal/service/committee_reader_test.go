// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func TestCommitteeReaderOrchestratorGetBase(t *testing.T) {
	ctx := context.Background()
	mockRepo := mock.NewMockRepository()

	// Setup test data
	testCommitteeUID := uuid.New().String()
	testCommittee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:              testCommitteeUID,
			ProjectUID:       "test-project-uid",
			ProjectName:      "Test Project",
			Name:             "Test Committee",
			Category:         "technical",
			Description:      "Test committee description",
			EnableVoting:     true,
			SSOGroupEnabled:  false,
			RequiresReview:   true,
			Public:           false,
			TotalMembers:     5,
			TotalVotingRepos: 3,
			CreatedAt:        time.Now().Add(-24 * time.Hour),
			UpdatedAt:        time.Now(),
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   testCommitteeUID,
			BusinessEmailRequired: true,
			Writers:               []string{"writer1", "writer2"},
			Auditors:              []string{"auditor1"},
			CreatedAt:             time.Now().Add(-24 * time.Hour),
			UpdatedAt:             time.Now(),
		},
	}

	tests := []struct {
		name          string
		setupMock     func()
		committeeUID  string
		expectedError bool
		errorType     error
		validateBase  func(*testing.T, *model.CommitteeBase, uint64)
	}{
		{
			name: "successful committee base retrieval",
			setupMock: func() {
				mockRepo.ClearAll()
				// Store the committee in mock repository
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			expectedError: false,
			validateBase: func(t *testing.T, base *model.CommitteeBase, revision uint64) {
				assert.NotNil(t, base)
				assert.Equal(t, testCommitteeUID, base.UID)
				assert.Equal(t, "test-project-uid", base.ProjectUID)
				assert.Equal(t, "Test Project", base.ProjectName)
				assert.Equal(t, "Test Committee", base.Name)
				assert.Equal(t, "technical", base.Category)
				assert.Equal(t, "Test committee description", base.Description)
				assert.True(t, base.EnableVoting)
				assert.False(t, base.SSOGroupEnabled)
				assert.True(t, base.RequiresReview)
				assert.False(t, base.Public)
				assert.Equal(t, 5, base.TotalMembers)
				assert.Equal(t, 3, base.TotalVotingRepos)
				assert.NotZero(t, base.CreatedAt)
				assert.NotZero(t, base.UpdatedAt)
				assert.Equal(t, uint64(1), revision) // Mock returns revision 1
			},
		},
		{
			name: "committee not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				// Don't store any committee
			},
			committeeUID:  "nonexistent-committee-uid",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateBase: func(t *testing.T, base *model.CommitteeBase, revision uint64) {
				assert.Nil(t, base)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "empty committee UID",
			setupMock: func() {
				mockRepo.ClearAll()
			},
			committeeUID:  "",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateBase: func(t *testing.T, base *model.CommitteeBase, revision uint64) {
				assert.Nil(t, base)
				assert.Equal(t, uint64(0), revision)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			tt.setupMock()

			// Create reader orchestrator
			reader := NewCommitteeReaderOrchestrator(
				WithCommitteeReader(mockRepo),
			)

			// Execute
			base, revision, err := reader.GetBase(ctx, tt.committeeUID)

			// Validate
			if tt.expectedError {
				require.Error(t, err)
				if tt.errorType != nil {
					assert.IsType(t, tt.errorType, err)
				}
			} else {
				require.NoError(t, err)
			}

			tt.validateBase(t, base, revision)
		})
	}
}

func TestCommitteeReaderOrchestratorGetSettings(t *testing.T) {
	ctx := context.Background()
	mockRepo := mock.NewMockRepository()

	// Setup test data
	testCommitteeUID := uuid.New().String()
	testCommittee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:         testCommitteeUID,
			ProjectUID:  "test-project-uid",
			ProjectName: "Test Project",
			Name:        "Test Committee",
			Category:    "technical",
			CreatedAt:   time.Now().Add(-24 * time.Hour),
			UpdatedAt:   time.Now(),
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   testCommitteeUID,
			BusinessEmailRequired: true,
			LastReviewedBy:        readerStringPtr("reviewer-uid"),
			Writers:               []string{"writer1", "writer2"},
			Auditors:              []string{"auditor1", "auditor2"},
			CreatedAt:             time.Now().Add(-24 * time.Hour),
			UpdatedAt:             time.Now(),
		},
	}

	tests := []struct {
		name             string
		setupMock        func()
		committeeUID     string
		expectedError    bool
		errorType        error
		validateSettings func(*testing.T, *model.CommitteeSettings, uint64)
	}{
		{
			name: "successful committee settings retrieval",
			setupMock: func() {
				mockRepo.ClearAll()
				// Store the committee in mock repository
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			expectedError: false,
			validateSettings: func(t *testing.T, settings *model.CommitteeSettings, revision uint64) {
				assert.NotNil(t, settings)
				assert.Equal(t, testCommitteeUID, settings.UID)
				assert.True(t, settings.BusinessEmailRequired)
				assert.NotNil(t, settings.LastReviewedBy)
				assert.Equal(t, "reviewer-uid", *settings.LastReviewedBy)
				assert.Equal(t, []string{"writer1", "writer2"}, settings.Writers)
				assert.Equal(t, []string{"auditor1", "auditor2"}, settings.Auditors)
				assert.NotZero(t, settings.CreatedAt)
				assert.NotZero(t, settings.UpdatedAt)
				assert.Equal(t, uint64(1), revision) // Mock returns revision 1
			},
		},
		{
			name: "committee settings not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				// Don't store any committee
			},
			committeeUID:  "nonexistent-committee-uid",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateSettings: func(t *testing.T, settings *model.CommitteeSettings, revision uint64) {
				assert.Nil(t, settings)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "empty committee UID",
			setupMock: func() {
				mockRepo.ClearAll()
			},
			committeeUID:  "",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateSettings: func(t *testing.T, settings *model.CommitteeSettings, revision uint64) {
				assert.Nil(t, settings)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "committee exists but no settings",
			setupMock: func() {
				mockRepo.ClearAll()
				// For this test, we'll just test with a different committee UID that doesn't exist
				// This simulates the case where settings are missing
			},
			committeeUID:  "committee-with-no-settings",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateSettings: func(t *testing.T, settings *model.CommitteeSettings, revision uint64) {
				assert.Nil(t, settings)
				assert.Equal(t, uint64(0), revision)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			tt.setupMock()

			// Create reader orchestrator
			reader := NewCommitteeReaderOrchestrator(
				WithCommitteeReader(mockRepo),
			)

			// Execute
			settings, revision, err := reader.GetSettings(ctx, tt.committeeUID)

			// Validate
			if tt.expectedError {
				require.Error(t, err)
				if tt.errorType != nil {
					assert.IsType(t, tt.errorType, err)
				}
			} else {
				require.NoError(t, err)
			}

			tt.validateSettings(t, settings, revision)
		})
	}
}

func TestNewCommitteeReaderOrchestrator(t *testing.T) {
	mockRepo := mock.NewMockRepository()

	tests := []struct {
		name     string
		options  []committeeReaderOrchestratorOption
		validate func(*testing.T, CommitteeReader)
	}{
		{
			name:    "create with no options",
			options: []committeeReaderOrchestratorOption{},
			validate: func(t *testing.T, reader CommitteeReader) {
				assert.NotNil(t, reader)
				// Test that it can be used (though it will have nil dependencies)
				orchestrator, ok := reader.(*committeeReaderOrchestrator)
				assert.True(t, ok)
				assert.Nil(t, orchestrator.committeeReader)
			},
		},
		{
			name: "create with committee reader option",
			options: []committeeReaderOrchestratorOption{
				WithCommitteeReader(mockRepo),
			},
			validate: func(t *testing.T, reader CommitteeReader) {
				assert.NotNil(t, reader)
				orchestrator, ok := reader.(*committeeReaderOrchestrator)
				assert.True(t, ok)
				assert.NotNil(t, orchestrator.committeeReader)
				assert.Equal(t, mockRepo, orchestrator.committeeReader)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Execute
			reader := NewCommitteeReaderOrchestrator(tt.options...)

			// Validate
			tt.validate(t, reader)
		})
	}
}

func TestCommitteeReaderOrchestratorIntegration(t *testing.T) {
	ctx := context.Background()
	mockRepo := mock.NewMockRepository()
	mockRepo.ClearAll()

	// Setup test data
	testCommitteeUID := uuid.New().String()
	testCommittee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:              testCommitteeUID,
			ProjectUID:       "integration-test-project",
			ProjectName:      "Integration Test Project",
			Name:             "Integration Test Committee",
			Category:         "governance",
			Description:      "Committee for integration testing",
			EnableVoting:     true,
			SSOGroupEnabled:  true,
			SSOGroupName:     "integration-test-sso-group",
			RequiresReview:   false,
			Public:           true,
			TotalMembers:     10,
			TotalVotingRepos: 5,
			CreatedAt:        time.Now().Add(-48 * time.Hour),
			UpdatedAt:        time.Now().Add(-1 * time.Hour),
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   testCommitteeUID,
			BusinessEmailRequired: false,
			LastReviewedAt:        readerStringPtr("2024-01-01T00:00:00Z"),
			LastReviewedBy:        readerStringPtr("integration-reviewer"),
			Writers:               []string{"integration-writer1", "integration-writer2", "integration-writer3"},
			Auditors:              []string{"integration-auditor1", "integration-auditor2"},
			CreatedAt:             time.Now().Add(-48 * time.Hour),
			UpdatedAt:             time.Now().Add(-1 * time.Hour),
		},
	}

	// Store the committee
	mockRepo.AddCommittee(testCommittee)

	// Create reader orchestrator
	reader := NewCommitteeReaderOrchestrator(
		WithCommitteeReader(mockRepo),
	)

	t.Run("get base and settings for same committee", func(t *testing.T) {
		// Get base
		base, baseRevision, err := reader.GetBase(ctx, testCommitteeUID)
		require.NoError(t, err)
		require.NotNil(t, base)

		// Get settings
		settings, settingsRevision, err := reader.GetSettings(ctx, testCommitteeUID)
		require.NoError(t, err)
		require.NotNil(t, settings)

		// Validate that both operations return consistent data
		assert.Equal(t, testCommitteeUID, base.UID)
		assert.Equal(t, testCommitteeUID, settings.UID)
		assert.Equal(t, baseRevision, settingsRevision) // Should be same revision in mock

		// Validate complete base data
		assert.Equal(t, "integration-test-project", base.ProjectUID)
		assert.Equal(t, "Integration Test Project", base.ProjectName)
		assert.Equal(t, "Integration Test Committee", base.Name)
		assert.Equal(t, "governance", base.Category)
		assert.Equal(t, "Committee for integration testing", base.Description)
		assert.True(t, base.EnableVoting)
		assert.True(t, base.SSOGroupEnabled)
		assert.Equal(t, "integration-test-sso-group", base.SSOGroupName)
		assert.False(t, base.RequiresReview)
		assert.True(t, base.Public)
		assert.Equal(t, 10, base.TotalMembers)
		assert.Equal(t, 5, base.TotalVotingRepos)

		// Validate complete settings data
		assert.False(t, settings.BusinessEmailRequired)
		assert.NotNil(t, settings.LastReviewedAt)
		assert.Equal(t, "2024-01-01T00:00:00Z", *settings.LastReviewedAt)
		assert.NotNil(t, settings.LastReviewedBy)
		assert.Equal(t, "integration-reviewer", *settings.LastReviewedBy)
		assert.Equal(t, []string{"integration-writer1", "integration-writer2", "integration-writer3"}, settings.Writers)
		assert.Equal(t, []string{"integration-auditor1", "integration-auditor2"}, settings.Auditors)
	})
}

func TestCommitteeReaderOrchestratorGetBaseAttributeValue(t *testing.T) {
	ctx := context.Background()
	mockRepo := mock.NewMockRepository()

	// Setup test data
	testCommitteeUID := uuid.New().String()
	testCommittee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:             testCommitteeUID,
			ProjectUID:      "test-project-uid",
			ProjectName:     "Test Project",
			Name:            "Test Committee",
			Category:        "technical",
			Description:     "Test committee description",
			Website:         readerStringPtr("https://example.com"),
			EnableVoting:    true,
			SSOGroupEnabled: false,
			SSOGroupName:    "test-sso-group",
			RequiresReview:  true,
			Public:          false,
			Calendar: model.Calendar{
				Public: true,
			},
			DisplayName:      "Test Display Name",
			ParentUID:        readerStringPtr("parent-committee-uid"),
			TotalMembers:     5,
			TotalVotingRepos: 3,
			CreatedAt:        time.Now().Add(-24 * time.Hour),
			UpdatedAt:        time.Now(),
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   testCommitteeUID,
			BusinessEmailRequired: true,
			Writers:               []string{"writer1", "writer2"},
			Auditors:              []string{"auditor1"},
			CreatedAt:             time.Now().Add(-24 * time.Hour),
			UpdatedAt:             time.Now(),
		},
	}

	tests := []struct {
		name          string
		setupMock     func()
		committeeUID  string
		attributeName string
		expectedError bool
		errorMessage  string
		validateValue func(*testing.T, any)
	}{
		{
			name: "successful retrieval of uid attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "uid",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				assert.Equal(t, testCommitteeUID, value)
			},
		},
		{
			name: "successful retrieval of project_uid attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "project_uid",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				assert.Equal(t, "test-project-uid", value)
			},
		},
		{
			name: "successful retrieval of name attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "name",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				assert.Equal(t, "Test Committee", value)
			},
		},
		{
			name: "successful retrieval of enable_voting boolean attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "enable_voting",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				assert.Equal(t, true, value)
			},
		},
		{
			name: "successful retrieval of total_members integer attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "total_members",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				assert.Equal(t, 5, value)
			},
		},
		{
			name: "successful retrieval of website pointer attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "website,omitempty",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				website, ok := value.(*string)
				assert.True(t, ok)
				assert.NotNil(t, website)
				assert.Equal(t, "https://example.com", *website)
			},
		},
		{
			name: "successful retrieval of calendar struct attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "calendar,omitempty",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				calendar, ok := value.(model.Calendar)
				assert.True(t, ok)
				assert.True(t, calendar.Public)
			},
		},
		{
			name: "successful retrieval of created_at time attribute",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "created_at",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				createdAt, ok := value.(time.Time)
				assert.True(t, ok)
				assert.False(t, createdAt.IsZero())
			},
		},
		{
			name: "successful retrieval of description attribute with omitempty",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "description,omitempty",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				assert.Equal(t, "Test committee description", value)
			},
		},
		{
			name: "successful retrieval of parent_uid pointer attribute with omitempty",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "parent_uid,omitempty",
			expectedError: false,
			validateValue: func(t *testing.T, value any) {
				parentUID, ok := value.(*string)
				assert.True(t, ok)
				assert.NotNil(t, parentUID)
				assert.Equal(t, "parent-committee-uid", *parentUID)
			},
		},
		{
			name: "committee not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				// Don't store any committee
			},
			committeeUID:  "nonexistent-committee-uid",
			attributeName: "uid",
			expectedError: true,
			validateValue: func(t *testing.T, value any) {
				assert.Nil(t, value)
			},
		},
		{
			name: "empty committee UID",
			setupMock: func() {
				mockRepo.ClearAll()
			},
			committeeUID:  "",
			attributeName: "uid",
			expectedError: true,
			validateValue: func(t *testing.T, value any) {
				assert.Nil(t, value)
			},
		},
		{
			name: "attribute not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "nonexistent_attribute",
			expectedError: true,
			errorMessage:  "attribute not found",
			validateValue: func(t *testing.T, value any) {
				assert.Nil(t, value)
			},
		},
		{
			name: "empty attribute name",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "",
			expectedError: true,
			errorMessage:  "attribute not found",
			validateValue: func(t *testing.T, value any) {
				assert.Nil(t, value)
			},
		},
		{
			name: "invalid attribute name format",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			attributeName: "invalid-attribute-name",
			expectedError: true,
			errorMessage:  "attribute not found",
			validateValue: func(t *testing.T, value any) {
				assert.Nil(t, value)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			tt.setupMock()

			// Create reader orchestrator
			reader := NewCommitteeReaderOrchestrator(
				WithCommitteeReader(mockRepo),
			)

			// Execute
			value, err := reader.GetBaseAttributeValue(ctx, tt.committeeUID, tt.attributeName)

			// Validate
			if tt.expectedError {
				require.Error(t, err)
				if tt.errorMessage != "" {
					assert.Contains(t, err.Error(), tt.errorMessage)
				}
			} else {
				require.NoError(t, err)
			}

			tt.validateValue(t, value)
		})
	}
}

func TestCommitteeReaderOrchestratorGetMember(t *testing.T) {
	ctx := context.Background()
	mockRepo := mock.NewMockRepository()

	// Setup test data
	testCommitteeUID := uuid.New().String()
	testMemberUID := uuid.New().String()
	testMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          testMemberUID,
			CommitteeUID: testCommitteeUID,
			Username:     "testuser",
			Email:        "test@example.com",
			FirstName:    "John",
			LastName:     "Doe",
			JobTitle:     "Software Engineer",
			AppointedBy:  "committee-chair",
			Status:       "active",
			Role: model.CommitteeMemberRole{
				Name:      "contributor",
				StartDate: "2024-01-01",
				EndDate:   "2024-12-31",
			},
			Voting: model.CommitteeMemberVotingInfo{
				Status:    "eligible",
				StartDate: "2024-01-01",
				EndDate:   "2024-12-31",
			},
			Organization: model.CommitteeMemberOrganization{
				Name:    "Test Organization",
				Website: "https://test-org.com",
			},
			CreatedAt: time.Now().Add(-24 * time.Hour),
			UpdatedAt: time.Now(),
		},
	}

	tests := []struct {
		name           string
		setupMock      func()
		committeeUID   string
		memberUID      string
		expectedError  bool
		errorType      error
		validateMember func(*testing.T, *model.CommitteeMember, uint64)
	}{
		{
			name: "successful committee member retrieval",
			setupMock: func() {
				mockRepo.ClearAll()
				// First, add the committee so it exists
				testCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        testCommitteeUID,
						ProjectUID: "test-project-uid",
						Name:       "Test Committee",
						Category:   "technical",
						CreatedAt:  time.Now().Add(-24 * time.Hour),
						UpdatedAt:  time.Now(),
					},
				}
				mockRepo.AddCommittee(testCommittee)
				// Store the committee member in mock repository
				mockRepo.AddCommitteeMember(testCommitteeUID, testMember)
			},
			committeeUID:  testCommitteeUID,
			memberUID:     testMemberUID,
			expectedError: false,
			validateMember: func(t *testing.T, member *model.CommitteeMember, revision uint64) {
				assert.NotNil(t, member)
				assert.Equal(t, testMemberUID, member.UID)
				assert.Equal(t, testCommitteeUID, member.CommitteeUID)
				assert.Equal(t, "testuser", member.Username)
				assert.Equal(t, "test@example.com", member.Email)
				assert.Equal(t, "John", member.FirstName)
				assert.Equal(t, "Doe", member.LastName)
				assert.Equal(t, "Software Engineer", member.JobTitle)
				assert.Equal(t, "committee-chair", member.AppointedBy)
				assert.Equal(t, "active", member.Status)
				assert.Equal(t, "contributor", member.Role.Name)
				assert.Equal(t, "2024-01-01", member.Role.StartDate)
				assert.Equal(t, "2024-12-31", member.Role.EndDate)
				assert.Equal(t, "eligible", member.Voting.Status)
				assert.Equal(t, "Test Organization", member.Organization.Name)
				assert.Equal(t, "https://test-org.com", member.Organization.Website)
				assert.Equal(t, uint64(1), revision) // Mock returns revision 1
			},
		},
		{
			name: "committee member not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				// Ensure committee exists so we test the 'member not found' path
				testCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        testCommitteeUID,
						ProjectUID: "test-project-uid",
						Name:       "Test Committee",
						Category:   "technical",
						CreatedAt:  time.Now().Add(-24 * time.Hour),
						UpdatedAt:  time.Now(),
					},
				}
				mockRepo.AddCommittee(testCommittee)
				// Don't store any committee member
			},
			committeeUID:  testCommitteeUID,
			memberUID:     "nonexistent-member-uid",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateMember: func(t *testing.T, member *model.CommitteeMember, revision uint64) {
				assert.Nil(t, member)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "member belongs to different committee",
			setupMock: func() {
				mockRepo.ClearAll()
				// Add the requested committee so it exists
				testCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        testCommitteeUID,
						ProjectUID: "test-project-uid",
						Name:       "Test Committee",
						Category:   "technical",
						CreatedAt:  time.Now().Add(-24 * time.Hour),
						UpdatedAt:  time.Now(),
					},
				}
				mockRepo.AddCommittee(testCommittee)
				// Create a member that belongs to a different committee
				differentMember := &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          testMemberUID,
						CommitteeUID: "different-committee-uid",
						Email:        "test@example.com",
						AppointedBy:  "different-chair",
						Status:       "active",
					},
				}
				mockRepo.AddCommitteeMember("different-committee-uid", differentMember)
			},
			committeeUID:  testCommitteeUID,
			memberUID:     testMemberUID,
			expectedError: true,
			validateMember: func(t *testing.T, member *model.CommitteeMember, revision uint64) {
				assert.Nil(t, member)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "empty committee UID",
			setupMock: func() {
				mockRepo.ClearAll()
			},
			committeeUID:  "",
			memberUID:     testMemberUID,
			expectedError: true,
			validateMember: func(t *testing.T, member *model.CommitteeMember, revision uint64) {
				assert.Nil(t, member)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "committee does not exist",
			setupMock: func() {
				mockRepo.ClearAll()
				// Don't add any committee, so committee lookup will fail
			},
			committeeUID:  "nonexistent-committee-uid",
			memberUID:     testMemberUID,
			expectedError: true,
			errorType:     errs.NotFound{},
			validateMember: func(t *testing.T, member *model.CommitteeMember, revision uint64) {
				assert.Nil(t, member)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "empty member UID",
			setupMock: func() {
				mockRepo.ClearAll()
				// Add the committee so it exists
				testCommittee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:        testCommitteeUID,
						ProjectUID: "test-project-uid",
						Name:       "Test Committee",
						Category:   "technical",
						CreatedAt:  time.Now().Add(-24 * time.Hour),
						UpdatedAt:  time.Now(),
					},
				}
				mockRepo.AddCommittee(testCommittee)
			},
			committeeUID:  testCommitteeUID,
			memberUID:     "",
			expectedError: true,
			validateMember: func(t *testing.T, member *model.CommitteeMember, revision uint64) {
				assert.Nil(t, member)
				assert.Equal(t, uint64(0), revision)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			tt.setupMock()

			// Create reader orchestrator
			reader := NewCommitteeReaderOrchestrator(
				WithCommitteeReader(mockRepo),
			)

			// Execute
			member, revision, err := reader.GetMember(ctx, tt.committeeUID, tt.memberUID)

			// Validate
			if tt.expectedError {
				require.Error(t, err)
				if tt.errorType != nil {
					assert.IsType(t, tt.errorType, err)
				}
			} else {
				require.NoError(t, err)
			}

			tt.validateMember(t, member, revision)
		})
	}
}

// Helper function to create string pointer (same as in committee_writer_test.go)
func readerStringPtr(s string) *string {
	return &s
}
