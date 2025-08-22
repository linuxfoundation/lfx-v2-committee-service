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

// TestMockCommitteeMemberWriter implements the full CommitteeWriter interface for testing
type TestMockCommitteeMemberWriter struct {
	*mock.MockRepository
	members         map[string]*model.CommitteeMember
	keys            map[string]string // uniqueness keys
	customRevisions map[string]uint64 // for testing revision conflicts
}

func NewTestMockCommitteeMemberWriter(mockRepo *mock.MockRepository) *TestMockCommitteeMemberWriter {
	return &TestMockCommitteeMemberWriter{
		MockRepository:  mockRepo,
		members:         make(map[string]*model.CommitteeMember),
		keys:            make(map[string]string),
		customRevisions: make(map[string]uint64),
	}
}

// Implement CommitteeBaseWriter interface
func (w *TestMockCommitteeMemberWriter) Create(ctx context.Context, committee *model.Committee) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.Create(ctx, committee)
}

func (w *TestMockCommitteeMemberWriter) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UpdateBase(ctx, committee, revision)
}

func (w *TestMockCommitteeMemberWriter) Delete(ctx context.Context, uid string, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.Delete(ctx, uid, revision)
}

func (w *TestMockCommitteeMemberWriter) UniqueNameProject(ctx context.Context, committee *model.Committee) (string, error) {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UniqueNameProject(ctx, committee)
}

func (w *TestMockCommitteeMemberWriter) UniqueSSOGroupName(ctx context.Context, committee *model.Committee) (string, error) {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UniqueSSOGroupName(ctx, committee)
}

// Implement CommitteeSettingsWriter interface
func (w *TestMockCommitteeMemberWriter) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UpdateSetting(ctx, settings, revision)
}

// Implement CommitteeMemberWriter interface
func (w *TestMockCommitteeMemberWriter) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	if member == nil {
		return errs.NewValidation("member cannot be nil")
	}

	// Store the member
	w.members[member.UID] = member
	return nil
}

func (w *TestMockCommitteeMemberWriter) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) error {
	return errs.NewUnexpected("committee member update not yet implemented")
}

func (w *TestMockCommitteeMemberWriter) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	if _, exists := w.members[uid]; !exists {
		return errs.NewNotFound("member not found")
	}

	// Check revision for optimistic locking
	currentRevision, err := w.GetMemberRevision(ctx, uid)
	if err != nil {
		return err
	}

	if currentRevision != revision {
		return errs.NewConflict("committee member has been modified by another process")
	}

	delete(w.members, uid)
	return nil
}

func (w *TestMockCommitteeMemberWriter) UniqueMember(ctx context.Context, member *model.CommitteeMember) (string, error) {
	key := member.BuildIndexKey(ctx)

	// Check if this key already exists
	if existingUID, exists := w.keys[key]; exists {
		return existingUID, errs.NewConflict("member with the same email already exists in the committee")
	}

	// Reserve the key
	w.keys[key] = member.UID
	return key, nil
}

func (w *TestMockCommitteeMemberWriter) GetMemberRevision(ctx context.Context, uid string) (uint64, error) {
	// Check if member exists in our local storage
	if _, exists := w.members[uid]; exists {
		// Check if we have a custom revision set
		if rev, exists := w.customRevisions[uid]; exists {
			return rev, nil
		}
		return 1, nil
	}

	// Delegate to mock repository for members that might be in the global mock
	return w.MockRepository.GetMemberRevision(ctx, uid)
}

// SetMemberRevision allows tests to set custom revisions
func (w *TestMockCommitteeMemberWriter) SetMemberRevision(uid string, revision uint64) {
	if w.customRevisions == nil {
		w.customRevisions = make(map[string]uint64)
	}
	w.customRevisions[uid] = revision
}

func setupMemberWriterTest() (*committeeWriterOrchestrator, *mock.MockRepository, *TestMockCommitteeMemberWriter) {
	mockRepo := mock.NewMockRepository()
	memberWriter := NewTestMockCommitteeMemberWriter(mockRepo)

	// Create orchestrator with mocks
	orchestrator := &committeeWriterOrchestrator{
		committeeReader:    mock.NewMockCommitteeReader(mockRepo),
		committeeWriter:    memberWriter,
		committeePublisher: mock.NewMockCommitteePublisher(),
		projectRetriever:   mock.NewMockProjectRetriever(mockRepo),
	}

	return orchestrator, mockRepo, memberWriter
}

// TestMockCommitteeReader is a minimal mock reader for testing
type TestMockCommitteeReader struct {
	memberRevisions map[string]uint64
}

func (r *TestMockCommitteeReader) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	return nil, 0, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) GetRevision(ctx context.Context, uid string) (uint64, error) {
	return 0, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) GetSettings(ctx context.Context, committeeUID string) (*model.CommitteeSettings, uint64, error) {
	return nil, 0, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	return nil, 0, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) GetMemberRevision(ctx context.Context, uid string) (uint64, error) {
	if revision, exists := r.memberRevisions[uid]; exists {
		return revision, nil
	}
	return 0, errs.NewNotFound("member not found")
}

func TestCommitteeWriterOrchestrator_CreateMember(t *testing.T) {
	tests := []struct {
		name           string
		setupMock      func(*mock.MockRepository)
		member         *model.CommitteeMember
		expectError    bool
		expectedError  string
		validateResult func(*testing.T, *model.CommitteeMember)
	}{
		{
			name: "successful member creation",
			setupMock: func(mockRepo *mock.MockRepository) {
				// Add a test committee
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:       "committee-123",
						Name:      "Test Committee",
						Category:  "Technical",
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:                   "committee-123",
						BusinessEmailRequired: false,
						CreatedAt:             time.Now(),
						UpdatedAt:             time.Now(),
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-123",
					Email:        "test@example.com",
					Username:     "testuser",
					FirstName:    "Test",
					LastName:     "User",
					Organization: model.CommitteeMemberOrganization{
						Name: "Test Org",
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, member *model.CommitteeMember) {
				assert.NotEmpty(t, member.UID, "UID should be generated")
				assert.NotZero(t, member.CreatedAt, "CreatedAt should be set")
				assert.NotZero(t, member.UpdatedAt, "UpdatedAt should be set")
				assert.Equal(t, "committee-123", member.CommitteeUID)
				assert.Equal(t, "test@example.com", member.Email)
			},
		},
		{
			name: "committee not found",
			setupMock: func(mockRepo *mock.MockRepository) {
				// Don't add any committee
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "nonexistent-committee",
					Email:        "test@example.com",
				},
			},
			expectError:   true,
			expectedError: "committee not found",
		},
		{
			name: "GAC member validation - missing agency",
			setupMock: func(mockRepo *mock.MockRepository) {
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:      "gac-committee",
						Name:     "Government Advisory Council",
						Category: "Government Advisory Council",
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "gac-committee",
					Email:        "test@example.com",
					Country:      "USA",
					// Missing Agency
				},
			},
			expectError:   true,
			expectedError: "missing required fields for Government Advisory Council members: agency",
		},
		{
			name: "GAC member validation - missing country",
			setupMock: func(mockRepo *mock.MockRepository) {
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:      "gac-committee",
						Name:     "Government Advisory Council",
						Category: "Government Advisory Council",
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "gac-committee",
					Email:        "test@example.com",
					Agency:       "GSA",
					// Missing Country
				},
			},
			expectError:   true,
			expectedError: "missing required fields for Government Advisory Council members: country",
		},
		{
			name: "valid GAC member",
			setupMock: func(mockRepo *mock.MockRepository) {
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:      "gac-committee",
						Name:     "Government Advisory Council",
						Category: "Government Advisory Council",
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "gac-committee",
					Email:        "test@example.com",
					Agency:       "GSA",
					Country:      "USA",
					Username:     "testuser",
					Organization: model.CommitteeMemberOrganization{
						Name: "Government Agency",
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, member *model.CommitteeMember) {
				assert.Equal(t, "GSA", member.Agency)
				assert.Equal(t, "USA", member.Country)
			},
		},
		{
			name: "duplicate member in same committee",
			setupMock: func(mockRepo *mock.MockRepository) {
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:      "committee-123",
						Name:     "Test Committee",
						Category: "Technical",
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-123",
					Email:        "duplicate@example.com",
					Username:     "testuser",
					Organization: model.CommitteeMemberOrganization{
						Name: "Test Org",
					},
				},
			},
			expectError:   true,
			expectedError: "member with the same email already exists in the committee",
		},
		{
			name: "missing required email",
			setupMock: func(mockRepo *mock.MockRepository) {
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:      "committee-123",
						Name:     "Test Committee",
						Category: "Technical",
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-123",
					Username:     "testuser",
					// Missing Email
				},
			},
			expectError:   true,
			expectedError: "email is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
			tt.setupMock(mockRepo)

			// For duplicate test, create the first member
			if tt.name == "duplicate member in same committee" {
				firstMember := &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          uuid.New().String(),
						CommitteeUID: "committee-123",
						Email:        "duplicate@example.com",
						Username:     "firstuser",
					},
				}
				_, _ = memberWriter.UniqueMember(context.Background(), firstMember)
			}

			ctx := context.Background()
			result, err := orchestrator.CreateMember(ctx, tt.member)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if tt.validateResult != nil {
					tt.validateResult(t, result)
				}
			}
		})
	}
}

func TestCommitteeWriterOrchestrator_CreateMember_BusinessEmailValidation(t *testing.T) {
	orchestrator, mockRepo, _ := setupMemberWriterTest()

	// Setup committee with business email required
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-business-email",
			Name:     "Business Committee",
			Category: "Technical",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   "committee-business-email",
			BusinessEmailRequired: true,
		},
	}
	mockRepo.AddCommittee(committee)

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "committee-business-email",
			Email:        "test@example.com",
			Username:     "testuser",
			Organization: model.CommitteeMemberOrganization{
				Name: "Test Org",
			},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.CreateMember(ctx, member)

	// Since validateCorporateEmailDomain is currently a placeholder that returns nil,
	// this should succeed
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCommitteeWriterOrchestrator_UpdateMember(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:   "member-123",
			Email: "test@example.com",
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, member, 1)

	// Should return not implemented error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "committee member update not yet implemented")
	assert.Nil(t, result)
}

func TestCommitteeWriterOrchestrator_DeleteMember(t *testing.T) {
	tests := []struct {
		name           string
		setupMock      func(*mock.MockRepository, *TestMockCommitteeMemberWriter)
		memberUID      string
		revision       uint64
		expectError    bool
		expectedError  string
		validateResult func(*testing.T, *TestMockCommitteeMemberWriter)
	}{
		{
			name: "successful member deletion",
			setupMock: func(mockRepo *mock.MockRepository, memberWriter *TestMockCommitteeMemberWriter) {
				// Add a test member
				member := &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-123",
						CommitteeUID: "committee-123",
						Email:        "test@example.com",
						Username:     "testuser",
						CreatedAt:    time.Now(),
						UpdatedAt:    time.Now(),
					},
				}
				memberWriter.members["member-123"] = member

				// Add member to mock repo which will set revision automatically
				mockRepo.AddCommitteeMember("committee-123", member)
			},
			memberUID:   "member-123",
			revision:    1,
			expectError: false,
			validateResult: func(t *testing.T, memberWriter *TestMockCommitteeMemberWriter) {
				// Verify member was deleted
				_, exists := memberWriter.members["member-123"]
				assert.False(t, exists, "Member should have been deleted")
			},
		},
		{
			name: "member not found",
			setupMock: func(mockRepo *mock.MockRepository, memberWriter *TestMockCommitteeMemberWriter) {
				// Don't add any member
			},
			memberUID:     "nonexistent-member",
			revision:      1,
			expectError:   true,
			expectedError: "member not found",
		},
		{
			name: "revision mismatch",
			setupMock: func(mockRepo *mock.MockRepository, memberWriter *TestMockCommitteeMemberWriter) {
				// Add a test member
				member := &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-456",
						CommitteeUID: "committee-123",
						Email:        "test2@example.com",
						Username:     "testuser2",
						CreatedAt:    time.Now(),
						UpdatedAt:    time.Now(),
					},
				}
				memberWriter.members["member-456"] = member

				// Add member to mock repo
				mockRepo.AddCommitteeMember("committee-123", member)
				// Set custom revision to 2 to simulate the member being updated
				memberWriter.SetMemberRevision("member-456", 2)
			},
			memberUID:     "member-456",
			revision:      1, // Wrong revision
			expectError:   true,
			expectedError: "committee member has been modified by another process",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
			tt.setupMock(mockRepo, memberWriter)

			ctx := context.Background()
			err := orchestrator.DeleteMember(ctx, tt.memberUID, tt.revision)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				if tt.validateResult != nil {
					tt.validateResult(t, memberWriter)
				}
			}
		})
	}
}

func TestCommitteeWriterOrchestrator_deleteMemberKeys(t *testing.T) {
	orchestrator, _, memberWriter := setupMemberWriterTest()

	// Create a custom mock reader that knows about our test member
	customReader := &TestMockCommitteeReader{
		memberRevisions: map[string]uint64{
			"member-to-delete": 1,
		},
	}
	orchestrator.committeeReader = customReader

	// Add a test member to our writer
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-to-delete",
			Email:        "delete@example.com",
			CommitteeUID: "committee-123",
		},
	}
	memberWriter.members["member-to-delete"] = member

	ctx := context.Background()
	keys := []string{"member-to-delete"}

	// Test successful deletion
	orchestrator.deleteMemberKeys(ctx, keys, false)

	// Verify member was deleted from our test writer
	_, exists := memberWriter.members["member-to-delete"]
	assert.False(t, exists, "Member should have been deleted from test writer")
}

func TestCommitteeWriterOrchestrator_deleteMemberKeys_EmptyKeys(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	ctx := context.Background()
	keys := []string{}

	// Should handle empty keys gracefully
	orchestrator.deleteMemberKeys(ctx, keys, false)
	// No assertion needed, just ensure it doesn't panic
}

func TestCommitteeWriterOrchestrator_validateCorporateEmailDomain(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	ctx := context.Background()
	err := orchestrator.validateCorporateEmailDomain(ctx, "test@example.com")

	// Currently a placeholder that returns nil
	assert.NoError(t, err)
}

func TestCommitteeWriterOrchestrator_validateUsernameExists(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	ctx := context.Background()
	err := orchestrator.validateUsernameExists(ctx, "testuser")

	// Currently a placeholder that returns nil
	assert.NoError(t, err)
}

func TestCommitteeWriterOrchestrator_validateOrganizationExists(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	ctx := context.Background()
	err := orchestrator.validateOrganizationExists(ctx, "Test Organization")

	// Currently a placeholder that returns nil
	assert.NoError(t, err)
}

func TestCommitteeWriterOrchestrator_addOrganizationUserEngagement(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	ctx := context.Background()
	err := orchestrator.addOrganizationUserEngagement(ctx, "Test Organization", "testuser")

	// Currently a placeholder that returns nil
	assert.NoError(t, err)
}

func TestCommitteeWriterOrchestrator_publishMemberMessages(t *testing.T) {
	tests := []struct {
		name   string
		action model.MessageAction
		data   any
	}{
		{
			name:   "publish create message with member data",
			action: model.ActionCreated,
			data: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-123",
					Email:        "test@example.com",
					Username:     "testuser",
				},
			},
		},
		{
			name:   "publish update message with member data",
			action: model.ActionUpdated,
			data: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-456",
					CommitteeUID: "committee-123",
					Email:        "updated@example.com",
					Username:     "updateduser",
				},
			},
		},
		{
			name:   "publish delete message with uid string",
			action: model.ActionDeleted,
			data:   "member-789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator, _, _ := setupMemberWriterTest()

			ctx := context.Background()
			err := orchestrator.publishMemberMessages(ctx, tt.action, tt.data)

			// Should succeed with mock publisher
			assert.NoError(t, err)
		})
	}
}

func TestCommitteeWriterOrchestrator_CreateMember_RollbackOnError(t *testing.T) {
	orchestrator, mockRepo, _ := setupMemberWriterTest()

	// Setup committee
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-123",
			Name:     "Test Committee",
			Category: "Technical",
		},
	}
	mockRepo.AddCommittee(committee)

	// Create a member with an invalid committee UID to trigger an error
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "nonexistent-committee",
			Email:        "test@example.com",
			Username:     "testuser",
			Organization: model.CommitteeMemberOrganization{
				Name: "Test Org",
			},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.CreateMember(ctx, member)

	// Should fail because committee doesn't exist
	require.Error(t, err)
	assert.Contains(t, err.Error(), "committee not found")
	assert.Nil(t, result)
}

func TestCommitteeWriterOrchestrator_CreateMember_SettingsNotFound(t *testing.T) {
	orchestrator, mockRepo, _ := setupMemberWriterTest()

	// Setup committee without settings
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-no-settings",
			Name:     "Committee Without Settings",
			Category: "Technical",
		},
		// No settings
	}
	mockRepo.AddCommittee(committee)

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "committee-no-settings",
			Email:        "test@example.com",
			Username:     "testuser",
			Organization: model.CommitteeMemberOrganization{
				Name: "Test Org",
			},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.CreateMember(ctx, member)

	// Should succeed with default settings
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestCommitteeWriterOrchestrator_DeleteMember_CompleteFlow(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	// Setup a complete member with all data
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-complete",
			CommitteeUID: "committee-123",
			Email:        "complete@example.com",
			Username:     "completeuser",
			FirstName:    "Complete",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				Name: "Complete Org",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	// Add member to storage
	memberWriter.members["member-complete"] = member
	mockRepo.AddCommitteeMember("committee-123", member)

	// Setup member lookup key (simulating secondary index)
	lookupKey := member.BuildIndexKey(context.Background())
	memberWriter.keys[lookupKey] = member.UID

	ctx := context.Background()
	err := orchestrator.DeleteMember(ctx, "member-complete", 1)

	// Should succeed
	require.NoError(t, err)

	// Verify member was deleted
	_, exists := memberWriter.members["member-complete"]
	assert.False(t, exists, "Member should have been deleted from storage")

	// Note: Secondary index cleanup is tested in deleteMemberKeys test
	// The actual cleanup happens in the background and would be tested
	// in integration tests with real NATS storage
}

func TestCommitteeWriterOrchestrator_DeleteMember_MessagePublishingFailure(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	// Setup a test member
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-msg-fail",
			CommitteeUID: "committee-123",
			Email:        "msgfail@example.com",
			Username:     "msgfailuser",
		},
	}

	memberWriter.members["member-msg-fail"] = member
	mockRepo.AddCommitteeMember("committee-123", member)

	// TODO: When we have a way to make the mock publisher fail,
	// we can test message publishing failure scenarios
	// For now, we test the happy path

	ctx := context.Background()
	err := orchestrator.DeleteMember(ctx, "member-msg-fail", 1)

	// Should succeed even if message publishing fails (currently mock always succeeds)
	require.NoError(t, err)
}
