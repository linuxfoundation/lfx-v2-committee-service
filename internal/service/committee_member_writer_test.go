// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// TestMockCommitteeMemberWriter implements the full CommitteeWriter interface for testing
type TestMockCommitteeMemberWriter struct {
	*mock.MockRepository
	members           map[string]*model.CommitteeMember
	keys              map[string]string // uniqueness keys
	customRevisions   map[string]uint64 // for testing revision conflicts
	indexedKeys       []string          // keys written by IndexMemberByCommittee
	orgIndexErr       error             // when set, IndexMemberByOrganization returns (key, orgIndexErr)
	uniqueMemberCalls int               // incremented on every UniqueMember call

	mu          sync.Mutex // guards deletedKeys (DeleteMember may run in a background cleanup goroutine)
	deletedKeys []string   // keys passed to DeleteMember (for asserting rollback / async stale cleanup)
}

// wasDeleted reports whether DeleteMember was called for key (thread-safe; the org-change stale-key
// cleanup runs in a background goroutine).
func (w *TestMockCommitteeMemberWriter) wasDeleted(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, k := range w.deletedKeys {
		if k == key {
			return true
		}
	}
	return false
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

func (w *TestMockCommitteeMemberWriter) UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error) {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UpdateHasMailingList(ctx, uid, hasMailingList)
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

func (w *TestMockCommitteeMemberWriter) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	if _, exists := w.members[member.UID]; !exists {
		return nil, errs.NewNotFound("committee member not found", fmt.Errorf("member UID: %s", member.UID))
	}

	// Check revision if custom revision is set
	if expectedRev, hasCustom := w.customRevisions[member.UID]; hasCustom {
		if expectedRev != revision {
			return nil, errs.NewConflict("committee member has been modified by another process")
		}
	}

	// Update the member
	w.members[member.UID] = member
	return member, nil
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
	w.mu.Lock()
	w.deletedKeys = append(w.deletedKeys, uid)
	w.mu.Unlock()
	return nil
}

func (w *TestMockCommitteeMemberWriter) UniqueMember(ctx context.Context, member *model.CommitteeMember) (string, error) {
	w.uniqueMemberCalls++
	key := member.BuildIndexKey(ctx)

	// Check if this key already exists
	if existingUID, exists := w.keys[key]; exists {
		return existingUID, errs.NewConflict("member with the same email already exists in the committee")
	}

	// Reserve the key
	w.keys[key] = member.UID
	return key, nil
}

func (w *TestMockCommitteeMemberWriter) IndexMemberByCommittee(_ context.Context, member *model.CommitteeMember) (string, error) {
	key := fmt.Sprintf(constants.KVLookupMembersByCommitteePrefix, member.CommitteeUID, member.UID)
	w.indexedKeys = append(w.indexedKeys, key)
	return key, nil
}

func (w *TestMockCommitteeMemberWriter) IndexMemberByOrganization(_ context.Context, member *model.CommitteeMember) (string, error) {
	orgSFID := utils.NormalizeAccountSFID(member.Organization.ID)
	if orgSFID == "" {
		return "", nil
	}
	key := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, orgSFID, member.UID)
	w.indexedKeys = append(w.indexedKeys, key)
	if w.orgIndexErr != nil {
		// Return the key alongside the error: the create/update paths append the key for rollback
		// before checking the error, so this exercises the rollback cleanup of a partial write.
		return key, w.orgIndexErr
	}
	return key, nil
}

func (w *TestMockCommitteeMemberWriter) IndexMemberByEmail(ctx context.Context, member *model.CommitteeMember) (string, error) {
	hash := member.BuildEmailIndexKey(ctx)
	if hash == "" {
		return "", nil
	}
	key := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, hash, member.UID)
	w.indexedKeys = append(w.indexedKeys, key)
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

// Implement CommitteeInviteWriter interface
func (w *TestMockCommitteeMemberWriter) CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.CreateInvite(ctx, invite)
}

func (w *TestMockCommitteeMemberWriter) UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UpdateInvite(ctx, invite, revision)
}

func (w *TestMockCommitteeMemberWriter) UniqueInvite(ctx context.Context, invite *model.CommitteeInvite) (string, error) {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UniqueInvite(ctx, invite)
}

// Implement CommitteeApplicationWriter interface
func (w *TestMockCommitteeMemberWriter) CreateApplication(ctx context.Context, application *model.CommitteeApplication) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.CreateApplication(ctx, application)
}

func (w *TestMockCommitteeMemberWriter) UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UpdateApplication(ctx, application, revision)
}

func (w *TestMockCommitteeMemberWriter) UniqueApplication(ctx context.Context, application *model.CommitteeApplication) (string, error) {
	mockWriter := mock.NewMockCommitteeWriter(w.MockRepository)
	return mockWriter.UniqueApplication(ctx, application)
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

// writerTestUserReader is a configurable UserReader mock for committee_member_writer tests.
type writerTestUserReader struct {
	usernames map[string]string // email → username
	err       error             // returned by UsernameByEmail when non-nil
}

func (r *writerTestUserReader) UsernameByEmail(_ context.Context, email string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.usernames[email], nil
}

func (r *writerTestUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (r *writerTestUserReader) UserMetadataByPrincipal(_ context.Context, _ string) (*model.UserMetadata, error) {
	return nil, nil
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

func (r *TestMockCommitteeReader) ListAllUIDs(ctx context.Context) ([]string, error) {
	return nil, errs.NewNotFound("not implemented for this test")
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

func (r *TestMockCommitteeReader) ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {
	return []*model.CommitteeMember{}, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) ListMembersByOrganization(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return []*model.CommitteeMember{}, nil
}

func (r *TestMockCommitteeReader) ListMembersByEmail(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return []*model.CommitteeMember{}, nil
}

func (r *TestMockCommitteeReader) ListAllMembers(_ context.Context) ([]*model.CommitteeMember, error) {
	return []*model.CommitteeMember{}, nil
}

func (r *TestMockCommitteeReader) EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	members, err := r.ListAllMembers(ctx)
	if err != nil {
		return err
	}
	for _, m := range members {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

// Implement CommitteeInviteReader interface
func (r *TestMockCommitteeReader) GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error) {
	return nil, 0, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	return []*model.CommitteeInvite{}, nil
}

func (r *TestMockCommitteeReader) ListAllInvites(_ context.Context) ([]*model.CommitteeInvite, error) {
	return []*model.CommitteeInvite{}, nil
}

// Implement CommitteeApplicationReader interface
func (r *TestMockCommitteeReader) GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error) {
	return nil, 0, errs.NewNotFound("not implemented for this test")
}

func (r *TestMockCommitteeReader) ListApplications(ctx context.Context, committeeUID string) ([]*model.CommitteeApplication, error) {
	return []*model.CommitteeApplication{}, nil
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
			// Regression guard (LFXV2-1442 / Org Lens board-committee tab): CreateMember MUST denormalize
			// project_uid/project_slug from the parent committee, and MUST override any caller-supplied
			// value with the committee's authoritative one. Members created without this (pre-LFXV2-1442)
			// carried an empty project_uid in KV truth and were silently dropped by the org-seat project
			// family filter. A trusted-but-wrong caller value must not be able to reintroduce the drift.
			name: "denormalizes project_uid/project_slug from committee, overriding caller input",
			setupMock: func(mockRepo *mock.MockRepository) {
				committee := &model.Committee{
					CommitteeBase: model.CommitteeBase{
						UID:         "committee-123",
						Name:        "Test Committee",
						Category:    "Technical",
						ProjectUID:  "project-authoritative",
						ProjectSlug: "authoritative-foundation",
						CreatedAt:   time.Now(),
						UpdatedAt:   time.Now(),
					},
					CommitteeSettings: &model.CommitteeSettings{
						UID:                   "committee-123",
						BusinessEmailRequired: false,
					},
				}
				mockRepo.AddCommittee(committee)
			},
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-123",
					Email:        "test@example.com",
					Username:     "testuser",
					Organization: model.CommitteeMemberOrganization{Name: "Test Org"},
					// Caller-supplied values that must be ignored in favor of the committee's.
					ProjectUID:  "project-wrong",
					ProjectSlug: "wrong-foundation",
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, member *model.CommitteeMember) {
				assert.Equal(t, "project-authoritative", member.ProjectUID,
					"project_uid must be denormalized from the committee, not the caller")
				assert.Equal(t, "authoritative-foundation", member.ProjectSlug,
					"project_slug must be denormalized from the committee, not the caller")
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
			result, err := orchestrator.CreateMember(ctx, tt.member, false, false)

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
				Name:    "Test Org",
				Website: "https://testorg.com",
			},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.CreateMember(ctx, member, false, false)

	// Since validateCorporateEmailDomain is currently a placeholder that returns nil,
	// this should succeed
	require.NoError(t, err)
	require.NotNil(t, result)
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
			err := orchestrator.DeleteMember(ctx, tt.memberUID, tt.revision, false, false)

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

// TestCreateMember_IndexKeyTracked verifies that CreateMember calls
// IndexMemberByCommittee and records the returned key for rollback.
func TestCreateMember_IndexKeyTracked(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-index-test",
			Name:     "Index Test Committee",
			Category: "Technical",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   "committee-index-test",
			BusinessEmailRequired: false,
		},
	}
	mockRepo.AddCommittee(committee)

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "committee-index-test",
			Email:        "indextest@example.com",
			Username:     "indexuser",
			Organization: model.CommitteeMemberOrganization{Name: "Index Org"},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.CreateMember(ctx, member, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.UID)
	assert.Equal(t, "committee-index-test", result.CommitteeUID)

	// IndexMemberByCommittee and IndexMemberByEmail must both have been called; the first indexed key
	// must be the committee→member key (appended before the email key in CreateMember).
	require.Len(t, memberWriter.indexedKeys, 2)
	expectedCommitteeKey := fmt.Sprintf("lookup/committee-members-by-committee/%s.%s", result.CommitteeUID, result.UID)
	assert.Equal(t, expectedCommitteeKey, memberWriter.indexedKeys[0])
	assert.Contains(t, memberWriter.indexedKeys[1], "lookup/committee-members-by-email/")
}

// TestDeleteMember_IndexKeyIncluded verifies that DeleteMember enqueues the
// committee→member secondary index key for cleanup alongside the primary record.
func TestDeleteMember_IndexKeyIncluded(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	existingMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-del-idx",
			CommitteeUID: "committee-del-idx",
			Email:        "del-idx@example.com",
			Username:     "delidxuser",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
	}
	// Populate the shared mock repo so GetMember succeeds.
	mockRepo.AddCommitteeMember(existingMember.CommitteeUID, existingMember)
	// Add primary record to writer so DeleteMember can find and remove it.
	memberWriter.members["member-del-idx"] = existingMember

	// Pre-register the by-committee index key in the writer's members map so
	// deleteMemberKeys can resolve a revision for it and attempt deletion.
	// We also point the orchestrator's reader at memberWriter: its GetMemberRevision
	// override checks the local members map first, so it finds the synthetic index
	// key entry without needing it to exist as a real member in the shared mock repo.
	indexKey := fmt.Sprintf("lookup/committee-members-by-committee/%s.%s",
		existingMember.CommitteeUID, existingMember.UID)
	memberWriter.members[indexKey] = existingMember
	orchestrator.committeeReader = memberWriter

	ctx := context.Background()
	err := orchestrator.DeleteMember(ctx, "member-del-idx", 1, false, false)
	require.NoError(t, err)

	// Primary record must be gone.
	_, primaryStillExists := memberWriter.members["member-del-idx"]
	assert.False(t, primaryStillExists, "main member record should be deleted")

	// Index key must also have been removed by the cleanup pass.
	_, indexStillExists := memberWriter.members[indexKey]
	assert.False(t, indexStillExists, "committee→member index key should be cleaned up on delete")
}

// flakyOldSeatReader wraps a real reader but forces GetMember(targetUID) to return a configurable
// non-NotFound (transient) error, to exercise ReassignMember's "old seat state unconfirmed" branch.
type flakyOldSeatReader struct {
	port.CommitteeReader
	targetUID string
	err       error
}

func (r *flakyOldSeatReader) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	if uid == r.targetUID {
		return nil, 0, r.err
	}
	return r.CommitteeReader.GetMember(ctx, uid)
}

// TestReassignMember_UnconfirmedOldSeatRetainsNewSeat verifies that when the old-seat delete fails and
// the confirming re-read also fails with a non-NotFound (transient) error, ReassignMember does NOT roll
// back the freshly created seat: rolling back when the delete may already have committed would leave the
// seat with no holder at all. Instead it retains the new seat and returns an error for manual
// reconciliation (a visible duplicate is preferable to silent data loss).
func TestReassignMember_UnconfirmedOldSeatRetainsNewSeat(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-reassign-unconfirmed",
			Name:     "Reassign Unconfirmed Committee",
			Category: "Technical",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   "committee-reassign-unconfirmed",
			BusinessEmailRequired: false,
		},
	}
	mockRepo.AddCommittee(committee)

	const oldMemberUID = "old-holder-unconfirmed"
	// Force the old-seat read (used by both DeleteMember and the post-delete confirm) to fail with a
	// transient, non-NotFound error so we land in the "unconfirmed" branch.
	orchestrator.committeeReader = &flakyOldSeatReader{
		CommitteeReader: orchestrator.committeeReader,
		targetUID:       oldMemberUID,
		err:             errs.NewUnexpected("nats read timeout"),
	}

	newMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "committee-reassign-unconfirmed",
			Email:        "new-holder@example.com",
			Username:     "newholder",
			Organization: model.CommitteeMemberOrganization{Name: "New Org"},
		},
	}

	ctx := context.Background()
	res, err := orchestrator.ReassignMember(ctx, oldMemberUID, 1, newMember, false)

	// The new seat is retained (nil member returned) and an error signals manual reconciliation.
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "new seat retained")

	// The freshly created seat must still be present — it must NOT have been rolled back.
	var retained bool
	for _, m := range memberWriter.members {
		if m != nil && m.Email == newMember.Email {
			retained = true
			break
		}
	}
	assert.True(t, retained, "new seat must be retained (not rolled back) when the old seat state is unconfirmed")
}

// TestCreateMember_OrgIndexWriteFailsRollsBack verifies the org-index failure path in CreateMember:
// when IndexMemberByOrganization returns a (key, error), the error is surfaced and the deferred rollback
// cleans up the records written before the failure (notably the primary member record).
func TestCreateMember_OrgIndexWriteFailsRollsBack(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	// Route reads through the writer so the rollback's GetMemberRevision/DeleteMember see the records
	// CreateMember just wrote into the in-memory store (same pattern as TestDeleteMember_IndexKeyIncluded).
	orchestrator.committeeReader = memberWriter

	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{UID: "committee-org-rollback", Name: "Org Rollback Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   "committee-org-rollback",
			BusinessEmailRequired: false,
		},
	}
	mockRepo.AddCommittee(committee)

	// Force the organization-index write to fail (after returning a non-empty key).
	memberWriter.orgIndexErr = errs.NewUnexpected("org index write failed")

	member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		CommitteeUID: "committee-org-rollback",
		Email:        "org-rollback@example.com",
		Username:     "orgrollbackuser",
		Organization: model.CommitteeMemberOrganization{ID: "001B000000IqhSLIAZ", Name: "Rollback Org"},
	}}

	ctx := context.Background()
	res, err := orchestrator.CreateMember(ctx, member, false, false)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.ErrorIs(t, err, memberWriter.orgIndexErr, "the org-index write error must be surfaced")

	// Rollback must have removed the primary member record created before the failure.
	_, stillExists := memberWriter.members[member.UID]
	assert.False(t, stillExists, "primary member record must be rolled back when the org index write fails")
	assert.True(t, memberWriter.wasDeleted(member.UID), "rollback must delete the primary member record")
}

// TestCommitteeWriterOrchestrator_UpdateMember_OrgChangeReindexes covers the org-change path
// (oldOrgSFID != newOrgSFID, both non-empty): the new organization index entry is written and the old
// entry is cleaned up by the background goroutine.
func TestCommitteeWriterOrchestrator_UpdateMember_OrgChangeReindexes(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"new@example.com": "newuser"}}
	// Route reads through the writer so the background stale-key cleanup goroutine's
	// GetMemberRevision/DeleteMember see the seeded in-memory index entries.
	orchestrator.committeeReader = memberWriter

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	const oldOrg = "001B000000IqhSLIAZ"
	const newOrg = "001C000000AbCdEFGH"
	existing := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-123", CommitteeUID: "committee-123", Email: "old@example.com", Username: "auth0|olduser",
		FirstName: "Old", LastName: "User",
		Organization: model.CommitteeMemberOrganization{ID: oldOrg, Name: "Old Org"},
		CreatedAt:    time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}}
	mockRepo.AddCommitteeMember("committee-123", existing)
	memberWriter.members["member-123"] = existing
	memberWriter.customRevisions["member-123"] = 1

	// Pre-seed the OLD org index key so the background cleanup goroutine can resolve + delete it.
	oldKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, utils.NormalizeAccountSFID(oldOrg), "member-123")
	memberWriter.members[oldKey] = existing

	updated := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-123", CommitteeUID: "committee-123", Email: "new@example.com", Username: "plain-lfid",
		FirstName: "New", LastName: "User",
		Organization: model.CommitteeMemberOrganization{ID: newOrg, Name: "New Org"},
	}}

	result, err := orchestrator.UpdateMember(context.Background(), updated, 1, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)

	// New org index entry must be written synchronously during the update.
	newKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, utils.NormalizeAccountSFID(newOrg), "member-123")
	assert.Contains(t, memberWriter.indexedKeys, newKey, "new organization index entry must be written on org change")

	// Old org index entry must be cleaned up by the background goroutine.
	assert.Eventually(t, func() bool { return memberWriter.wasDeleted(oldKey) }, 2*time.Second, 10*time.Millisecond,
		"stale old-organization index entry must be cleaned up after the org change")
}

// TestCommitteeWriterOrchestrator_UpdateMember_OrgRemovalCleansUp covers the org-removal path
// (newOrgSFID == ""): no new organization index is written, and the old entry is cleaned up.
func TestCommitteeWriterOrchestrator_UpdateMember_OrgRemovalCleansUp(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"new@example.com": "newuser"}}
	// Route reads through the writer so the background stale-key cleanup goroutine's
	// GetMemberRevision/DeleteMember see the seeded in-memory index entries.
	orchestrator.committeeReader = memberWriter

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	const oldOrg = "001B000000IqhSLIAZ"
	existing := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-123", CommitteeUID: "committee-123", Email: "old@example.com", Username: "auth0|olduser",
		FirstName: "Old", LastName: "User",
		Organization: model.CommitteeMemberOrganization{ID: oldOrg, Name: "Old Org"},
		CreatedAt:    time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}}
	mockRepo.AddCommitteeMember("committee-123", existing)
	memberWriter.members["member-123"] = existing
	memberWriter.customRevisions["member-123"] = 1

	oldKey := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, utils.NormalizeAccountSFID(oldOrg), "member-123")
	memberWriter.members[oldKey] = existing

	// Updated member loses its organization affiliation (no organization id).
	updated := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-123", CommitteeUID: "committee-123", Email: "new@example.com", Username: "plain-lfid",
		FirstName: "New", LastName: "User",
		Organization: model.CommitteeMemberOrganization{},
	}}

	result, err := orchestrator.UpdateMember(context.Background(), updated, 1, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)

	// No new organization index entry is written when the member loses its org.
	// (An email-index entry IS written because the email changed in this scenario — that is correct.)
	for _, k := range memberWriter.indexedKeys {
		assert.NotContains(t, k, "lookup/committee-members-by-organization",
			"no organization index entry should be written when the org is removed")
	}

	// The old org index entry must still be cleaned up by the background goroutine.
	assert.Eventually(t, func() bool { return memberWriter.wasDeleted(oldKey) }, 2*time.Second, 10*time.Millisecond,
		"stale old-organization index entry must be cleaned up after org removal")
}

// TestCommitteeWriterOrchestrator_UpdateMember_CaseOnlyEmailChange verifies that a case-only
// email change (same normalized hash) does not write or delete any uniqueness/index keys.
// Writing a new key identical to the old one and then marking it stale would silently delete the
// valid entry, breaking the uniqueness invariant.
func TestCommitteeWriterOrchestrator_UpdateMember_CaseOnlyEmailChange(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"user@example.com": "theuser"}}
	orchestrator.committeeReader = memberWriter

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	existing := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-123", CommitteeUID: "committee-123", Email: "user@example.com",
		FirstName: "Test", LastName: "User",
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}}
	mockRepo.AddCommitteeMember("committee-123", existing)
	memberWriter.members["member-123"] = existing
	memberWriter.customRevisions["member-123"] = 1

	// Pre-seed the uniqueness key so that a duplicate write would be detected as a conflict.
	existingIndexKey := existing.BuildIndexKey(context.Background())
	memberWriter.keys[existingIndexKey] = existing.UID

	// Update with a case-only email change (same normalized form).
	updated := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-123", CommitteeUID: "committee-123", Email: "USER@EXAMPLE.COM",
		FirstName: "Test", LastName: "User",
	}}

	result, err := orchestrator.UpdateMember(context.Background(), updated, 1, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)

	// UniqueMember must not be called at all for a case-only email change.
	assert.Equal(t, 0, memberWriter.uniqueMemberCalls,
		"UniqueMember must not be called for a case-only email change")

	// No new index key should have been written, and the pre-seeded uniqueness key must remain.
	for _, k := range memberWriter.indexedKeys {
		assert.NotContains(t, k, "lookup/committee-members/",
			"case-only email change must not write a new uniqueness key")
		assert.NotContains(t, k, "lookup/committee-members-by-email/",
			"case-only email change must not write a new email-index key")
	}
	assert.Equal(t, existing.UID, memberWriter.keys[existingIndexKey],
		"pre-existing uniqueness key must not be deleted by a case-only email change")
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

type stubB2BOrgResolver struct {
	sfid string
	ok   bool
	err  error
}

func (s stubB2BOrgResolver) ResolveByUID(_ context.Context, _ string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	return s.sfid, s.ok, nil
}

func TestCommitteeWriterOrchestrator_sanitizeMemberOrganization(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{sfid: "0014100000Te2ovAAB", ok: true}

	org := model.CommitteeMemberOrganization{ID: "0014100000Te2ovAAB", Name: "LF"}
	err := orchestrator.sanitizeMemberOrganization(context.Background(), &org)
	require.NoError(t, err)
	assert.Equal(t, "0014100000Te2ovAAB", org.ID)

	orchestrator.b2bOrgResolver = stubB2BOrgResolver{}
	org = model.CommitteeMemberOrganization{ID: "51fde723-67df-4e0e-91c6-936d01d59559", Name: "Acme", Website: "https://acme.com"}
	err = orchestrator.sanitizeMemberOrganization(context.Background(), &org)
	require.NoError(t, err)
	assert.Empty(t, org.ID)
	assert.Equal(t, "Acme", org.Name)
	assert.Equal(t, "https://acme.com", org.Website)
}

func TestCommitteeWriterOrchestrator_sanitizeMemberOrganization_nonSFIDCleared(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()
	// Resolver would return found=true if called, but the pre-filter must prevent
	// non-SFID-shaped ids from even reaching the resolver (FR-005 / T017).
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{sfid: "0014100000Te2ovAAB", ok: true}

	cases := []struct {
		name string
		id   string
	}{
		{"CDP UUID", "51fde723-67df-4e0e-91c6-936d01d59559"},
		{"hex digest", "abc123de45fg6789abcd1234ef567890"},
		{"arbitrary non-sfid", "not-a-sfid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			org := model.CommitteeMemberOrganization{ID: tc.id, Name: "Acme"}
			err := orchestrator.sanitizeMemberOrganization(context.Background(), &org)
			require.NoError(t, err)
			assert.Empty(t, org.ID, "non-SFID id %q must be cleared before lookup", tc.id)
		})
	}
}

func TestCommitteeWriterOrchestrator_CreateMember_UnresolvedOrgIDClearsID(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{} // not found

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
				Name:    "Acme Corp",
				Website: "https://acme.com",
			},
		},
	}

	result, err := orchestrator.CreateMember(context.Background(), member, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Organization.ID)
	assert.Equal(t, "Acme Corp", result.Organization.Name)
	assert.Equal(t, "https://acme.com", result.Organization.Website)

	stored := memberWriter.members[result.UID]
	require.NotNil(t, stored)
	assert.Empty(t, stored.Organization.ID)
	assert.Equal(t, "Acme Corp", stored.Organization.Name)
	assert.Equal(t, "https://acme.com", stored.Organization.Website)
}

func TestCommitteeWriterOrchestrator_UpdateMember_UnresolvedOrgIDClearsID(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{} // not found
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"test@example.com": "testuser"}}

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	existing := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{Name: "Acme Corp"},
			CreatedAt:    time.Now().Add(-time.Hour),
			UpdatedAt:    time.Now().Add(-time.Hour),
		},
	}
	mockRepo.AddCommitteeMember("committee-123", existing)
	memberWriter.members["member-123"] = existing
	memberWriter.customRevisions["member-123"] = 1

	updated := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
				Name:    "Acme Corp",
				Website: "https://acme.com",
			},
		},
	}

	result, err := orchestrator.UpdateMember(context.Background(), updated, 1, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Organization.ID)
	assert.Equal(t, "Acme Corp", result.Organization.Name)
	assert.Equal(t, "https://acme.com", result.Organization.Website)
}

func TestCommitteeWriterOrchestrator_CreateMember_ResolverErrorKeepsOrgID(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	// Use an SFID-shaped id: the pre-filter passes it through, and the fail-open path
	// keeps it when the resolver returns a transient error.
	const wantID = "001B000000IqhSLIAZ"
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{err: fmt.Errorf("b2b_org lookup failed")}

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				ID:      wantID,
				Name:    "Acme Corp",
				Website: "https://acme.com",
			},
		},
	}

	result, err := orchestrator.CreateMember(context.Background(), member, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, wantID, result.Organization.ID)

	stored := memberWriter.members[result.UID]
	require.NotNil(t, stored)
	assert.Equal(t, wantID, stored.Organization.ID)
}

func TestCommitteeWriterOrchestrator_UpdateMember_ResolverErrorKeepsOrgID(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	// Use an SFID-shaped id: the pre-filter passes it through, and the fail-open path
	// keeps it when the resolver returns a transient error.
	const wantID = "001B000000IqhSLIAZ"
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{err: fmt.Errorf("b2b_org lookup failed")}
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"test@example.com": "testuser"}}

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	existing := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{Name: "Acme Corp"},
			CreatedAt:    time.Now().Add(-time.Hour),
			UpdatedAt:    time.Now().Add(-time.Hour),
		},
	}
	mockRepo.AddCommitteeMember("committee-123", existing)
	memberWriter.members["member-123"] = existing
	memberWriter.customRevisions["member-123"] = 1

	updated := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				ID:      wantID,
				Name:    "Acme Corp",
				Website: "https://acme.com",
			},
		},
	}

	result, err := orchestrator.UpdateMember(context.Background(), updated, 1, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, wantID, result.Organization.ID)

	stored := memberWriter.members["member-123"]
	require.NotNil(t, stored)
	assert.Equal(t, wantID, stored.Organization.ID)
}

func TestCommitteeWriterOrchestrator_UpdateMember_TrimsWhitespaceOrgIDWithoutLookup(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	const wantID = "001B000000IqhSLIAZ"
	orchestrator.b2bOrgResolver = stubB2BOrgResolver{err: fmt.Errorf("should not call resolver")}
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"test@example.com": "testuser"}}

	committee := &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: "committee-123", Name: "Test Committee", Category: "Technical"},
		CommitteeSettings: &model.CommitteeSettings{BusinessEmailRequired: false},
	}
	mockRepo.AddCommittee(committee)

	existing := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{ID: wantID, Name: "Acme Corp"},
			CreatedAt:    time.Now().Add(-time.Hour),
			UpdatedAt:    time.Now().Add(-time.Hour),
		},
	}
	mockRepo.AddCommitteeMember("committee-123", existing)
	memberWriter.members["member-123"] = existing
	memberWriter.customRevisions["member-123"] = 1

	updated := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
			FirstName:    "Test",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				ID:   "  " + wantID + "  ",
				Name: "Acme Corp",
			},
		},
	}

	result, err := orchestrator.UpdateMember(context.Background(), updated, 1, false, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, wantID, result.Organization.ID)

	stored := memberWriter.members["member-123"]
	require.NotNil(t, stored)
	assert.Equal(t, wantID, stored.Organization.ID)
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
		name               string
		action             model.MessageAction
		data               *model.CommitteeMemberMessageData
		wantNameAndAliases []string
	}{
		{
			name:   "publish create message with member data",
			action: model.ActionCreated,
			data: &model.CommitteeMemberMessageData{
				Member: &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-123",
						CommitteeUID: "committee-123",
						Email:        "test@example.com",
						Username:     "testuser",
					},
				},
			},
		},
		{
			name:   "publish update message with member data",
			action: model.ActionUpdated,
			data: &model.CommitteeMemberMessageData{
				Member: &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-456",
						CommitteeUID: "committee-123",
						Email:        "updated@example.com",
						Username:     "updateduser",
					},
				},
				OldMember: &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-456",
						CommitteeUID: "committee-123",
						Email:        "old@example.com",
						Username:     "olduser",
					},
				},
			},
		},
		{
			name:   "publish delete message with member data",
			action: model.ActionDeleted,
			data: &model.CommitteeMemberMessageData{
				Member: &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-789",
						CommitteeUID: "committee-123",
						Email:        "deleted@example.com",
						Username:     "deleteduser",
					},
				},
			},
		},
		{
			name:   "publish create message includes combined full name in name_and_aliases",
			action: model.ActionCreated,
			data: &model.CommitteeMemberMessageData{
				Member: &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:           "member-987",
						CommitteeUID:  "committee-123",
						CommitteeName: "Governing Board",
						Email:         "jsmith@example.com",
						FirstName:     "Jane",
						LastName:      "Smith",
						Username:      "jsmith",
					},
				},
			},
			wantNameAndAliases: []string{"Jane", "Smith", "Jane Smith"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mock.NewMockRepository()
			memberWriter := NewTestMockCommitteeMemberWriter(mockRepo)
			publisher := &mock.MockCommitteePublisher{}
			orchestrator := &committeeWriterOrchestrator{
				committeeReader:    mock.NewMockCommitteeReader(mockRepo),
				committeeWriter:    memberWriter,
				committeePublisher: publisher,
				projectRetriever:   mock.NewMockProjectRetriever(mockRepo),
			}

			ctx := context.Background()
			err := orchestrator.publishMemberMessages(ctx, tt.action, tt.data, false)

			// Should succeed with mock publisher
			assert.NoError(t, err)

			if tt.wantNameAndAliases != nil {
				msg, ok := publisher.LastIndexerMessage.(*model.CommitteeIndexerMessage)
				require.True(t, ok)
				require.NotNil(t, msg.IndexingConfig)
				for _, want := range tt.wantNameAndAliases {
					assert.Contains(t, msg.IndexingConfig.NameAndAliases, want)
				}
			}
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
	result, err := orchestrator.CreateMember(ctx, member, false, false)

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
	result, err := orchestrator.CreateMember(ctx, member, false, false)

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
	err := orchestrator.DeleteMember(ctx, "member-complete", 1, false, false)

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
	err := orchestrator.DeleteMember(ctx, "member-msg-fail", 1, false, false)

	// Should succeed even if message publishing fails (currently mock always succeeds)
	require.NoError(t, err)
}

func TestCommitteeWriterOrchestrator_UpdateMember_Success(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
	orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"new@example.com": "newuser"}}

	// Setup committee with settings
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-123",
			Name:     "Test Committee",
			Category: "Technical",
		},
		CommitteeSettings: &model.CommitteeSettings{
			BusinessEmailRequired: false,
		},
	}
	mockRepo.AddCommittee(committee)

	// Setup existing member
	existingMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "old@example.com",
			Username:     "olduser",
			FirstName:    "Old",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				Name: "Old Org",
			},
			CreatedAt: time.Now().Add(-time.Hour),
			UpdatedAt: time.Now().Add(-time.Hour),
		},
	}

	// Add member to mock repository (this is what the orchestrator will read from)
	mockRepo.AddCommitteeMember("committee-123", existingMember)

	// Also add to the member writer for storage operations
	memberWriter.members["member-123"] = existingMember
	memberWriter.customRevisions["member-123"] = 1

	// Create updated member with changes; caller supplies a plain LFID which must be overridden.
	updatedMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "new@example.com", // Email changed
			Username:     "plain-lfid",      // caller-supplied; must be overridden by email lookup
			FirstName:    "New",
			LastName:     "User",
			Organization: model.CommitteeMemberOrganization{
				Name: "New Org", // Organization changed
			},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, updatedMember, 1, false, false)

	// Should succeed
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the member was updated and the username was resolved from email.
	assert.Equal(t, "member-123", result.UID)
	assert.Equal(t, "new@example.com", result.Email)
	assert.Equal(t, "newuser", result.Username)
	assert.Equal(t, "New Org", result.Organization.Name)

	// Verify timestamps were preserved/updated correctly
	assert.Equal(t, existingMember.CreatedAt, result.CreatedAt)      // CreatedAt should be preserved
	assert.True(t, result.UpdatedAt.After(existingMember.UpdatedAt)) // UpdatedAt should be newer
}

func TestCommitteeWriterOrchestrator_UpdateMember_RevisionMismatch(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	// Setup committee
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-123",
			Name:     "Test Committee",
			Category: "Technical",
		},
	}
	mockRepo.AddCommittee(committee)

	// Setup existing member
	existingMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
			Username:     "testuser",
		},
	}
	memberWriter.members["member-123"] = existingMember
	memberWriter.customRevisions["member-123"] = 5 // Current revision is 5

	updatedMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "updated@example.com",
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, updatedMember, 3, false, false) // Using old revision 3

	// Should fail with conflict error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "modified by another process")
	assert.Nil(t, result)
}

func TestCommitteeWriterOrchestrator_UpdateMember_MemberNotFound(t *testing.T) {
	orchestrator, _, _ := setupMemberWriterTest()

	updatedMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "nonexistent-member",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, updatedMember, 1, false, false)

	// Should fail with not found error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Nil(t, result)
}

func TestCommitteeWriterOrchestrator_UpdateMember_CommitteeNotFound(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	// Setup existing member belonging to a valid committee
	existingMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "test@example.com",
		},
	}
	// Add member to mock repository (this is what the orchestrator will read from)
	mockRepo.AddCommitteeMember("committee-123", existingMember)
	// Also add to the member writer for storage operations
	memberWriter.members["member-123"] = existingMember
	memberWriter.customRevisions["member-123"] = 1

	// Try to update member to belong to a nonexistent committee
	updatedMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "nonexistent-committee",
			Email:        "updated@example.com",
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, updatedMember, 1, false, false)

	// Should fail because member belongs to different committee
	require.Error(t, err)
	assert.Contains(t, err.Error(), "committee member does not belong to the requested committee")
	assert.Nil(t, result)
}

func TestCommitteeWriterOrchestrator_UpdateMember_EmailChangeWithCorporateValidation(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	// Setup committee with business email required
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-123",
			Name:     "Test Committee",
			Category: "Technical",
		},
		CommitteeSettings: &model.CommitteeSettings{
			BusinessEmailRequired: true, // Corporate email validation required
		},
	}
	mockRepo.AddCommittee(committee)

	// Setup existing member
	existingMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "old@example.com",
			Username:     "testuser",
			Organization: model.CommitteeMemberOrganization{
				Name:    "Test Org",
				Website: "https://testorg.com",
			},
		},
	}
	memberWriter.members["member-123"] = existingMember
	memberWriter.customRevisions["member-123"] = 1
	mockRepo.AddCommitteeMember("committee-123", existingMember)

	// Create updated member with new email
	updatedMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "new@corporate.com", // Email changed
			Username:     "testuser",
			Organization: model.CommitteeMemberOrganization{
				Name:    "Test Org",
				Website: "https://testorg.com",
			},
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, updatedMember, 1, false, false)

	// Should succeed (corporate validation is mocked to always pass)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "new@corporate.com", result.Email)
}

func TestCommitteeWriterOrchestrator_UpdateMember_EmailAlreadyExists(t *testing.T) {
	orchestrator, mockRepo, memberWriter := setupMemberWriterTest()

	// Setup committee
	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:      "committee-123",
			Name:     "Test Committee",
			Category: "Technical",
		},
	}
	mockRepo.AddCommittee(committee)

	// Setup existing member 1
	existingMember1 := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "member1@example.com",
		},
	}
	memberWriter.members["member-123"] = existingMember1
	memberWriter.customRevisions["member-123"] = 1

	// Setup existing member 2 with different email
	existingMember2 := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-456",
			CommitteeUID: "committee-123",
			Email:        "member2@example.com",
		},
	}
	memberWriter.members["member-456"] = existingMember2
	memberWriter.customRevisions["member-456"] = 1

	// Create lookup key for member 2's email
	lookupKey2 := existingMember2.BuildIndexKey(context.Background())
	memberWriter.keys[lookupKey2] = existingMember2.UID

	// Try to update member 1 to use member 2's email
	updatedMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-123",
			CommitteeUID: "committee-123",
			Email:        "member2@example.com", // Email already used by member-456
		},
	}

	ctx := context.Background()
	result, err := orchestrator.UpdateMember(ctx, updatedMember, 1, false, false)

	// Should fail with conflict error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Nil(t, result)
}

func TestCommitteeWriterOrchestrator_CreateMember_UsernameResolution(t *testing.T) {
	addCommittee := func(mockRepo *mock.MockRepository) {
		mockRepo.AddCommittee(&model.Committee{
			CommitteeBase: model.CommitteeBase{UID: "c-1", Name: "C", Category: "TC"},
			CommitteeSettings: &model.CommitteeSettings{
				UID: "c-1", BusinessEmailRequired: false,
			},
		})
	}

	t.Run("plain LFID overridden by username from email lookup", func(t *testing.T) {
		orchestrator, mockRepo, _ := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"alice@example.com": "alice"}}
		addCommittee(mockRepo)

		result, err := orchestrator.CreateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				CommitteeUID: "c-1",
				Email:        "alice@example.com",
				Username:     "plain-lfid",
				FirstName:    "Alice",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, false, false)

		require.NoError(t, err)
		assert.Equal(t, "alice", result.Username)
	})

	t.Run("username cleared when email present but lookup fails", func(t *testing.T) {
		orchestrator, mockRepo, _ := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{err: errs.NewServiceUnavailable("auth service down")}
		addCommittee(mockRepo)

		result, err := orchestrator.CreateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				CommitteeUID: "c-1",
				Email:        "alice@example.com",
				Username:     "plain-lfid",
				FirstName:    "Alice",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, false, false)

		require.NoError(t, err)
		assert.Empty(t, result.Username)
	})

	t.Run("skip enrichment persists caller-supplied username without email lookup", func(t *testing.T) {
		orchestrator, mockRepo, _ := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"alice@example.com": "other-lfid"}}
		addCommittee(mockRepo)

		result, err := orchestrator.CreateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				CommitteeUID: "c-1",
				Email:        "alice@example.com",
				Username:     "sync-lfid",
				FirstName:    "Alice",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, false, true)

		require.NoError(t, err)
		assert.Equal(t, "sync-lfid", result.Username)
	})
}

func TestCommitteeWriterOrchestrator_UpdateMember_UsernameResolution(t *testing.T) {
	addCommitteeAndMember := func(mockRepo *mock.MockRepository, memberWriter *TestMockCommitteeMemberWriter) {
		mockRepo.AddCommittee(&model.Committee{
			CommitteeBase:     model.CommitteeBase{UID: "c-1", Name: "C", Category: "TC"},
			CommitteeSettings: &model.CommitteeSettings{UID: "c-1"},
		})
		existing := &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID: "m-1", CommitteeUID: "c-1", Email: "old@example.com",
				Username: "old", FirstName: "Old",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
				CreatedAt:    time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
			},
		}
		mockRepo.AddCommitteeMember("c-1", existing)
		memberWriter.members["m-1"] = existing
		memberWriter.customRevisions["m-1"] = 1
	}

	t.Run("plain LFID overridden by username from email lookup", func(t *testing.T) {
		orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{"new@example.com": "new"}}
		addCommitteeAndMember(mockRepo, memberWriter)

		result, err := orchestrator.UpdateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID: "m-1", CommitteeUID: "c-1", Email: "new@example.com",
				Username: "plain-lfid", FirstName: "New",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, 1, false, false)

		require.NoError(t, err)
		assert.Equal(t, "new", result.Username)
	})

	t.Run("username cleared when email changes and lookup fails", func(t *testing.T) {
		orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{err: errs.NewServiceUnavailable("auth service down")}
		addCommitteeAndMember(mockRepo, memberWriter)

		result, err := orchestrator.UpdateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID: "m-1", CommitteeUID: "c-1", Email: "new@example.com",
				Username: "plain-lfid", FirstName: "New",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, 1, false, false)

		require.NoError(t, err)
		assert.Empty(t, result.Username)
	})

	t.Run("username cleared when email changes and lookup returns empty", func(t *testing.T) {
		orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{usernames: map[string]string{}}
		addCommitteeAndMember(mockRepo, memberWriter)

		result, err := orchestrator.UpdateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID: "m-1", CommitteeUID: "c-1", Email: "new@example.com",
				Username: "plain-lfid", FirstName: "New",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, 1, false, false)

		require.NoError(t, err)
		assert.Empty(t, result.Username)
	})

	t.Run("stored username kept when email unchanged and lookup fails", func(t *testing.T) {
		orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{err: errs.NewServiceUnavailable("auth service down")}
		addCommitteeAndMember(mockRepo, memberWriter)

		result, err := orchestrator.UpdateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID: "m-1", CommitteeUID: "c-1", Email: "old@example.com",
				Username: "plain-lfid", FirstName: "Old",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, 1, false, false)

		require.NoError(t, err)
		assert.Equal(t, "old", result.Username)
	})

	t.Run("skip enrichment persists accepted_by without email lookup", func(t *testing.T) {
		orchestrator, mockRepo, memberWriter := setupMemberWriterTest()
		orchestrator.userReader = &writerTestUserReader{err: errs.NewServiceUnavailable("auth service down")}
		addCommitteeAndMember(mockRepo, memberWriter)

		result, err := orchestrator.UpdateMember(context.Background(), &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID: "m-1", CommitteeUID: "c-1", Email: "new@example.com",
				Username: "accepted-lfid", FirstName: "New",
				Organization: model.CommitteeMemberOrganization{Name: "Org"},
			},
		}, 1, false, true)

		require.NoError(t, err)
		assert.Equal(t, "accepted-lfid", result.Username)
	})
}
