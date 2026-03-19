// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// Mock orchestrator for testing service layer
type mockCommitteeWriterOrchestrator struct {
	deleteError       error
	deleteCalls       []deleteCall
	updateMember      *model.CommitteeMember
	updateMemberErr   error
	updateMemberCalls []updateMemberCall
	createMember      *model.CommitteeMember
	createMemberErr   error
}

type updateMemberCall struct {
	member   *model.CommitteeMember
	revision uint64
}

type deleteCall struct {
	uid      string
	revision uint64
}

func (m *mockCommitteeWriterOrchestrator) Create(ctx context.Context, committee *model.Committee, sync bool) (*model.Committee, error) {
	return nil, errs.NewUnexpected("not implemented for test")
}

func (m *mockCommitteeWriterOrchestrator) Update(ctx context.Context, committee *model.Committee, revision uint64, sync bool) (*model.Committee, error) {
	return nil, errs.NewUnexpected("not implemented for test")
}

func (m *mockCommitteeWriterOrchestrator) UpdateSettings(ctx context.Context, settings *model.CommitteeSettings, revision uint64, sync bool) (*model.CommitteeSettings, error) {
	return nil, errs.NewUnexpected("not implemented for test")
}

func (m *mockCommitteeWriterOrchestrator) Delete(ctx context.Context, uid string, revision uint64, sync bool) error {
	return errs.NewUnexpected("not implemented for test")
}

func (m *mockCommitteeWriterOrchestrator) CreateMember(ctx context.Context, member *model.CommitteeMember, sync bool) (*model.CommitteeMember, error) {
	if m.createMemberErr != nil {
		return nil, m.createMemberErr
	}
	if m.createMember != nil {
		return m.createMember, nil
	}
	return nil, errs.NewUnexpected("not implemented for test")
}

func (m *mockCommitteeWriterOrchestrator) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64, sync bool) (*model.CommitteeMember, error) {
	m.updateMemberCalls = append(m.updateMemberCalls, updateMemberCall{member: member, revision: revision})
	if m.updateMemberErr != nil {
		return nil, m.updateMemberErr
	}
	return m.updateMember, nil
}

func (m *mockCommitteeWriterOrchestrator) DeleteMember(ctx context.Context, uid string, revision uint64, sync bool) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{uid: uid, revision: revision})
	return m.deleteError
}

func setupServiceTest() (*committeeServicesrvc, *mockCommitteeWriterOrchestrator) {
	mockOrchestrator := &mockCommitteeWriterOrchestrator{}
	mockRepo := mock.NewMockRepository()

	service := &committeeServicesrvc{
		committeeWriterOrchestrator: mockOrchestrator,
		committeeReaderOrchestrator: nil, // Not needed for delete member test
		auth:                        mock.NewMockAuthService(),
		storage:                     mock.NewMockCommitteeReaderWriter(mockRepo),
		publisher:                   mock.NewMockCommitteePublisher(),
	}

	return service, mockOrchestrator
}

// setupServiceTestWithRepo returns the service, mock orchestrator, AND the underlying mock repo
// so tests can seed invite/application/settings data.
func setupServiceTestWithRepo() (*committeeServicesrvc, *mockCommitteeWriterOrchestrator, *mock.MockRepository) {
	mockOrchestrator := &mockCommitteeWriterOrchestrator{}
	mockRepo := mock.NewMockRepository()

	svc := &committeeServicesrvc{
		committeeWriterOrchestrator: mockOrchestrator,
		committeeReaderOrchestrator: nil,
		auth:                        mock.NewMockAuthService(),
		storage:                     mock.NewMockCommitteeReaderWriter(mockRepo),
		publisher:                   mock.NewMockCommitteePublisher(),
	}

	return svc, mockOrchestrator, mockRepo
}

func TestDeleteCommitteeMember(t *testing.T) {
	tests := []struct {
		name          string
		payload       *committeeservice.DeleteCommitteeMemberPayload
		setupMock     func(*mockCommitteeWriterOrchestrator)
		expectError   bool
		expectedError string
		validateCall  func(*testing.T, []deleteCall)
	}{
		{
			name: "successful deletion",
			payload: &committeeservice.DeleteCommitteeMemberPayload{
				UID:       "committee-123",
				MemberUID: "member-456",
				IfMatch:   stringPtr("1"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				mock.deleteError = nil
			},
			expectError: false,
			validateCall: func(t *testing.T, calls []deleteCall) {
				require.Len(t, calls, 1)
				assert.Equal(t, "member-456", calls[0].uid)
				assert.Equal(t, uint64(1), calls[0].revision)
			},
		},
		{
			name: "invalid etag",
			payload: &committeeservice.DeleteCommitteeMemberPayload{
				UID:       "committee-123",
				MemberUID: "member-456",
				IfMatch:   stringPtr("invalid"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				// Should not be called due to etag validation failure
			},
			expectError:   true,
			expectedError: "invalid ETag format",
			validateCall: func(t *testing.T, calls []deleteCall) {
				assert.Empty(t, calls, "DeleteMember should not be called with invalid etag")
			},
		},
		{
			name: "empty etag",
			payload: &committeeservice.DeleteCommitteeMemberPayload{
				UID:       "committee-123",
				MemberUID: "member-456",
				IfMatch:   nil,
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				// Should not be called due to etag validation failure
			},
			expectError:   true,
			expectedError: "ETag is required",
			validateCall: func(t *testing.T, calls []deleteCall) {
				assert.Empty(t, calls, "DeleteMember should not be called with empty etag")
			},
		},
		{
			name: "orchestrator returns error",
			payload: &committeeservice.DeleteCommitteeMemberPayload{
				UID:       "committee-123",
				MemberUID: "member-456",
				IfMatch:   stringPtr("1"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				mock.deleteError = errs.NewNotFound("member not found")
			},
			expectError:   true,
			expectedError: "member not found",
			validateCall: func(t *testing.T, calls []deleteCall) {
				require.Len(t, calls, 1)
				assert.Equal(t, "member-456", calls[0].uid)
				assert.Equal(t, uint64(1), calls[0].revision)
			},
		},
		{
			name: "revision conflict",
			payload: &committeeservice.DeleteCommitteeMemberPayload{
				UID:       "committee-123",
				MemberUID: "member-456",
				IfMatch:   stringPtr("2"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				mock.deleteError = errs.NewConflict("committee member has been modified by another process")
			},
			expectError:   true,
			expectedError: "committee member has been modified by another process",
			validateCall: func(t *testing.T, calls []deleteCall) {
				require.Len(t, calls, 1)
				assert.Equal(t, "member-456", calls[0].uid)
				assert.Equal(t, uint64(2), calls[0].revision)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, mockOrchestrator := setupServiceTest()
			tt.setupMock(mockOrchestrator)

			ctx := context.Background()
			err := service.DeleteCommitteeMember(ctx, tt.payload)

			if tt.expectError {
				require.Error(t, err)

				// Check if it's a GOA error type with Message field
				switch e := err.(type) {
				case *committeeservice.BadRequestError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.NotFoundError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.ConflictError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.InternalServerError:
					assert.Contains(t, e.Message, tt.expectedError)
				default:
					assert.Contains(t, err.Error(), tt.expectedError)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.validateCall != nil {
				tt.validateCall(t, mockOrchestrator.deleteCalls)
			}
		})
	}
}

func TestDeleteCommitteeMember_ETagValidation(t *testing.T) {
	tests := []struct {
		name          string
		etag          string
		expectError   bool
		expectedError string
	}{
		{
			name:        "valid numeric etag",
			etag:        "123",
			expectError: false,
		},
		{
			name:        "valid zero etag",
			etag:        "0",
			expectError: false,
		},
		{
			name:          "invalid non-numeric etag",
			etag:          "abc",
			expectError:   true,
			expectedError: "invalid ETag format",
		},
		{
			name:          "empty etag",
			etag:          "",
			expectError:   true,
			expectedError: "ETag is required",
		},
		{
			name:          "negative etag",
			etag:          "-1",
			expectError:   true,
			expectedError: "invalid ETag format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, mockOrchestrator := setupServiceTest()
			mockOrchestrator.deleteError = nil

			payload := &committeeservice.DeleteCommitteeMemberPayload{
				UID:       "committee-123",
				MemberUID: "member-456",
				IfMatch:   stringPtr(tt.etag),
			}

			ctx := context.Background()
			err := service.DeleteCommitteeMember(ctx, payload)

			if tt.expectError {
				require.Error(t, err)

				// Check if it's a GOA error type with Message field
				switch e := err.(type) {
				case *committeeservice.BadRequestError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.NotFoundError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.ConflictError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.InternalServerError:
					assert.Contains(t, e.Message, tt.expectedError)
				default:
					assert.Contains(t, err.Error(), tt.expectedError)
				}

				// Verify orchestrator was not called on validation error
				assert.Empty(t, mockOrchestrator.deleteCalls)
			} else {
				require.NoError(t, err)
				// Verify orchestrator was called
				assert.Len(t, mockOrchestrator.deleteCalls, 1)
			}
		})
	}
}

func TestUpdateCommitteeMember(t *testing.T) {
	tests := []struct {
		name           string
		payload        *committeeservice.UpdateCommitteeMemberPayload
		setupMock      func(*mockCommitteeWriterOrchestrator)
		expectError    bool
		expectedError  string
		validateCall   func(*testing.T, []updateMemberCall)
		validateResult func(*testing.T, *committeeservice.CommitteeMemberFullWithReadonlyAttributes)
	}{
		{
			name: "successful update",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Username:    stringPtr("testuser"),
				Email:       "test@example.com",
				FirstName:   stringPtr("John"),
				LastName:    stringPtr("Doe"),
				AppointedBy: "admin",
				Status:      "active",
				IfMatch:     stringPtr("1"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				mock.updateMember = &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-456",
						CommitteeUID: "committee-123",
						Username:     "testuser",
						Email:        "test@example.com",
						FirstName:    "John",
						LastName:     "Doe",
						AppointedBy:  "admin",
						Status:       "active",
					},
				}
				mock.updateMemberErr = nil
			},
			expectError: false,
			validateCall: func(t *testing.T, calls []updateMemberCall) {
				require.Len(t, calls, 1)
				assert.Equal(t, "member-456", calls[0].member.UID)
				assert.Equal(t, "committee-123", calls[0].member.CommitteeUID)
				assert.Equal(t, "test@example.com", calls[0].member.Email)
				assert.Equal(t, uint64(1), calls[0].revision)
			},
			validateResult: func(t *testing.T, result *committeeservice.CommitteeMemberFullWithReadonlyAttributes) {
				require.NotNil(t, result)
				assert.Equal(t, "member-456", *result.UID)
				assert.Equal(t, "committee-123", *result.CommitteeUID)
				assert.Equal(t, "test@example.com", *result.Email)
			},
		},
		{
			name: "invalid etag",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Email:       "test@example.com",
				AppointedBy: "admin",
				Status:      "active",
				IfMatch:     stringPtr("invalid"),
			},
			setupMock:     func(mock *mockCommitteeWriterOrchestrator) {},
			expectError:   true,
			expectedError: "invalid syntax",
			validateCall: func(t *testing.T, calls []updateMemberCall) {
				assert.Empty(t, calls)
			},
		},
		{
			name: "missing etag",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Email:       "test@example.com",
				AppointedBy: "admin",
				Status:      "active",
				IfMatch:     nil,
			},
			setupMock:     func(mock *mockCommitteeWriterOrchestrator) {},
			expectError:   true,
			expectedError: "ETag is required",
			validateCall: func(t *testing.T, calls []updateMemberCall) {
				assert.Empty(t, calls)
			},
		},
		{
			name: "orchestrator error - not found",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Email:       "test@example.com",
				AppointedBy: "admin",
				Status:      "active",
				IfMatch:     stringPtr("1"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				mock.updateMemberErr = errs.NewNotFound("committee member not found")
			},
			expectError:   true,
			expectedError: "committee member not found",
			validateCall: func(t *testing.T, calls []updateMemberCall) {
				require.Len(t, calls, 1)
			},
		},
		{
			name: "orchestrator error - conflict",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Email:       "test@example.com",
				AppointedBy: "admin",
				Status:      "active",
				IfMatch:     stringPtr("1"),
			},
			setupMock: func(mock *mockCommitteeWriterOrchestrator) {
				mock.updateMemberErr = errs.NewConflict("committee member has been modified by another process")
			},
			expectError:   true,
			expectedError: "modified by another process",
			validateCall: func(t *testing.T, calls []updateMemberCall) {
				require.Len(t, calls, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, mockOrchestrator := setupServiceTest()
			tt.setupMock(mockOrchestrator)

			result, err := service.UpdateCommitteeMember(context.Background(), tt.payload)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)

				// Check if it's a GOA error type with Message field
				switch e := err.(type) {
				case *committeeservice.BadRequestError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.NotFoundError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.ConflictError:
					assert.Contains(t, e.Message, tt.expectedError)
				case *committeeservice.InternalServerError:
					assert.Contains(t, e.Message, tt.expectedError)
				default:
					assert.Contains(t, err.Error(), tt.expectedError)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if tt.validateResult != nil {
					tt.validateResult(t, result)
				}
			}

			if tt.validateCall != nil {
				tt.validateCall(t, mockOrchestrator.updateMemberCalls)
			}
		})
	}
}

// ==================== Invite Endpoint Tests ====================

func TestGetInvite(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(repo *mock.MockRepository)
		payload     *committeeservice.GetInvitePayload
		expectError bool
		errType     string
	}{
		{
			name: "successful get invite",
			setup: func(repo *mock.MockRepository) {
				repo.AddCommitteeInvite(&model.CommitteeInvite{
					UID:          "get-invite-001",
					CommitteeUID: "committee-1",
					InviteeEmail: "getinvite@example.com",
					Role:         "member",
					Status:       "pending",
					CreatedAt:    time.Now().UTC(),
				})
			},
			payload: &committeeservice.GetInvitePayload{
				UID:       "committee-1",
				InviteUID: "get-invite-001",
			},
		},
		{
			name:  "invite not found",
			setup: func(repo *mock.MockRepository) {},
			payload: &committeeservice.GetInvitePayload{
				UID:       "committee-1",
				InviteUID: "non-existent-invite",
			},
			expectError: true,
			errType:     "not_found",
		},
		{
			name: "invite in different committee",
			setup: func(repo *mock.MockRepository) {
				repo.AddCommitteeInvite(&model.CommitteeInvite{
					UID:          "get-invite-002",
					CommitteeUID: "committee-2",
					InviteeEmail: "other@example.com",
					Role:         "member",
					Status:       "pending",
					CreatedAt:    time.Now().UTC(),
				})
			},
			payload: &committeeservice.GetInvitePayload{
				UID:       "committee-1",
				InviteUID: "get-invite-002",
			},
			expectError: true,
			errType:     "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, repo := setupServiceTestWithRepo()
			tt.setup(repo)

			result, err := service.GetInvite(context.Background(), tt.payload)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
				if tt.errType == "not_found" {
					var nfErr *committeeservice.NotFoundError
					require.ErrorAs(t, err, &nfErr)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, tt.payload.InviteUID, *result.UID)
			}
		})
	}
}

func TestCreateInvite(t *testing.T) {
	tests := []struct {
		name        string
		payload     *committeeservice.CreateInvitePayload
		expectError bool
		errContains string
	}{
		{
			name: "successful invite creation",
			payload: &committeeservice.CreateInvitePayload{
				UID:          "committee-1",
				InviteeEmail: "newinvitee@example.com",
				Role:         stringPtr("member"),
				XSync:        false,
			},
			expectError: false,
		},
		{
			name: "invite for non-existent committee",
			payload: &committeeservice.CreateInvitePayload{
				UID:          "non-existent-committee",
				InviteeEmail: "someone@example.com",
			},
			expectError: true,
			errContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := setupServiceTestWithRepo()

			result, err := svc.CreateInvite(context.Background(), tt.payload)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.NotEmpty(t, *result.UID)
				assert.Equal(t, tt.payload.UID, *result.CommitteeUID)
				assert.Equal(t, tt.payload.InviteeEmail, *result.InviteeEmail)
				assert.Equal(t, "pending", result.Status)
			}
		})
	}
}

func TestCreateInvite_DuplicateRejected(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	// Seed an existing invite
	existing := &model.CommitteeInvite{
		UID:          "existing-invite",
		CommitteeUID: "committee-1",
		InviteeEmail: "dup@example.com",
		Status:       "pending",
		CreatedAt:    time.Now(),
	}
	repo.AddCommitteeInvite(existing)

	// Attempt to create a duplicate
	_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "dup@example.com",
	})

	require.Error(t, err)
	var conflictErr *committeeservice.ConflictError
	require.ErrorAs(t, err, &conflictErr)
}

func TestRevokeInvite(t *testing.T) {
	tests := []struct {
		name        string
		seedStatus  string
		expectError bool
		errContains string
	}{
		{
			name:        "successful revocation of pending invite",
			seedStatus:  "pending",
			expectError: false,
		},
		{
			name:        "cannot revoke already accepted invite",
			seedStatus:  "accepted",
			expectError: true,
			errContains: "already been processed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()

			invite := &model.CommitteeInvite{
				UID:          "invite-revoke-test",
				CommitteeUID: "committee-1",
				InviteeEmail: "revoke@example.com",
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeInvite(invite)

			err := svc.RevokeInvite(context.Background(), &committeeservice.RevokeInvitePayload{
				UID:       "committee-1",
				InviteUID: "invite-revoke-test",
			})

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRevokeInvite_WrongCommittee(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	invite := &model.CommitteeInvite{
		UID:          "invite-wrong-committee",
		CommitteeUID: "committee-1",
		InviteeEmail: "test@example.com",
		Status:       "pending",
		CreatedAt:    time.Now(),
	}
	repo.AddCommitteeInvite(invite)

	err := svc.RevokeInvite(context.Background(), &committeeservice.RevokeInvitePayload{
		UID:       "committee-2", // wrong committee
		InviteUID: "invite-wrong-committee",
	})

	require.Error(t, err)
	var nfErr *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nfErr)
	assert.Contains(t, nfErr.Message, "invite not found in this committee")
}

func TestRevokeInvite_NotFound(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()

	err := svc.RevokeInvite(context.Background(), &committeeservice.RevokeInvitePayload{
		UID:       "committee-1",
		InviteUID: "does-not-exist",
	})

	require.Error(t, err)
	var nfErr *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nfErr)
}

func TestAcceptInvite(t *testing.T) {
	tests := []struct {
		name        string
		seedStatus  string
		principal   string
		expectError bool
	}{
		{
			name:        "successful accept of pending invite",
			seedStatus:  "pending",
			principal:   "accept@example.com",
			expectError: false,
		},
		{
			name:        "cannot accept already revoked invite",
			seedStatus:  "revoked",
			principal:   "accept@example.com",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()

			invite := &model.CommitteeInvite{
				UID:          "invite-accept-test",
				CommitteeUID: "committee-1",
				InviteeEmail: "accept@example.com",
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeInvite(invite)

			mockOrch.createMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "new-member-uid",
					CommitteeUID: "committee-1",
					Email:        "accept@example.com",
					Status:       "Active",
				},
			}

			ctx := context.WithValue(context.Background(), constants.EmailContextID, tt.principal)
			result, err := svc.AcceptInvite(ctx, &committeeservice.AcceptInvitePayload{
				UID:       "committee-1",
				InviteUID: "invite-accept-test",
			})

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "Active", result.Status)
			}
		})
	}
}

func TestAcceptInvite_OwnershipCheck(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	invite := &model.CommitteeInvite{
		UID:          "invite-ownership-accept",
		CommitteeUID: "committee-1",
		InviteeEmail: "real-invitee@example.com",
		Status:       "pending",
		CreatedAt:    time.Now(),
	}
	repo.AddCommitteeInvite(invite)

	// Different user tries to accept someone else's invite
	ctx := context.WithValue(context.Background(), constants.EmailContextID, "attacker@example.com")
	result, err := svc.AcceptInvite(ctx, &committeeservice.AcceptInvitePayload{
		UID:       "committee-1",
		InviteUID: "invite-ownership-accept",
	})

	require.Error(t, err)
	assert.Nil(t, result)
	var forbiddenErr *committeeservice.ForbiddenError
	require.ErrorAs(t, err, &forbiddenErr)
	assert.Contains(t, forbiddenErr.Message, "you are not the invitee")
}

func TestDeclineInvite(t *testing.T) {
	tests := []struct {
		name        string
		seedStatus  string
		principal   string
		expectError bool
	}{
		{
			name:        "successful decline of pending invite",
			seedStatus:  "pending",
			principal:   "decline@example.com",
			expectError: false,
		},
		{
			name:        "cannot decline already accepted invite",
			seedStatus:  "accepted",
			principal:   "decline@example.com",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()

			invite := &model.CommitteeInvite{
				UID:          "invite-decline-test",
				CommitteeUID: "committee-1",
				InviteeEmail: "decline@example.com",
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeInvite(invite)

			ctx := context.WithValue(context.Background(), constants.EmailContextID, tt.principal)
			result, err := svc.DeclineInvite(ctx, &committeeservice.DeclineInvitePayload{
				UID:       "committee-1",
				InviteUID: "invite-decline-test",
			})

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "declined", result.Status)
			}
		})
	}
}

func TestDeclineInvite_OwnershipCheck(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	invite := &model.CommitteeInvite{
		UID:          "invite-ownership-decline",
		CommitteeUID: "committee-1",
		InviteeEmail: "real-invitee@example.com",
		Status:       "pending",
		CreatedAt:    time.Now(),
	}
	repo.AddCommitteeInvite(invite)

	// Different user tries to decline someone else's invite
	ctx := context.WithValue(context.Background(), constants.EmailContextID, "attacker@example.com")
	result, err := svc.DeclineInvite(ctx, &committeeservice.DeclineInvitePayload{
		UID:       "committee-1",
		InviteUID: "invite-ownership-decline",
	})

	require.Error(t, err)
	assert.Nil(t, result)
	var forbiddenErr *committeeservice.ForbiddenError
	require.ErrorAs(t, err, &forbiddenErr)
	assert.Contains(t, forbiddenErr.Message, "you are not the invitee")
}

// ==================== Application Endpoint Tests ====================

func TestGetApplication(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(repo *mock.MockRepository)
		payload     *committeeservice.GetApplicationPayload
		expectError bool
		errType     string
	}{
		{
			name: "successful get application",
			setup: func(repo *mock.MockRepository) {
				repo.AddCommitteeApplication(&model.CommitteeApplication{
					UID:          "get-app-001",
					CommitteeUID: "committee-1",
					ApplicantUID: "get-app-unique@example.com",
					Message:      "I want to join",
					Status:       "pending",
					CreatedAt:    time.Now().UTC(),
				})
			},
			payload: &committeeservice.GetApplicationPayload{
				UID:            "committee-1",
				ApplicationUID: "get-app-001",
			},
		},
		{
			name:  "application not found",
			setup: func(repo *mock.MockRepository) {},
			payload: &committeeservice.GetApplicationPayload{
				UID:            "committee-1",
				ApplicationUID: "non-existent-app",
			},
			expectError: true,
			errType:     "not_found",
		},
		{
			name: "application in different committee",
			setup: func(repo *mock.MockRepository) {
				repo.AddCommitteeApplication(&model.CommitteeApplication{
					UID:          "get-app-002",
					CommitteeUID: "committee-2",
					ApplicantUID: "other-applicant@example.com",
					Message:      "Wrong committee",
					Status:       "pending",
					CreatedAt:    time.Now().UTC(),
				})
			},
			payload: &committeeservice.GetApplicationPayload{
				UID:            "committee-1",
				ApplicationUID: "get-app-002",
			},
			expectError: true,
			errType:     "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, repo := setupServiceTestWithRepo()
			tt.setup(repo)

			result, err := service.GetApplication(context.Background(), tt.payload)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
				if tt.errType == "not_found" {
					var nfErr *committeeservice.NotFoundError
					require.ErrorAs(t, err, &nfErr)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, tt.payload.ApplicationUID, *result.UID)
			}
		})
	}
}

func TestSubmitApplication(t *testing.T) {
	tests := []struct {
		name        string
		joinMode    string
		principal   string
		expectError bool
		errContains string
	}{
		{
			name:        "successful application when join_mode is application",
			joinMode:    "application",
			principal:   "applicant@example.com",
			expectError: false,
		},
		{
			name:        "rejected when join_mode is open",
			joinMode:    "open",
			principal:   "applicant@example.com",
			expectError: true,
			errContains: "does not accept applications",
		},
		{
			name:        "rejected when join_mode is empty (closed)",
			joinMode:    "",
			principal:   "applicant@example.com",
			expectError: true,
			errContains: "does not accept applications",
		},
		{
			name:        "rejected when join_mode is closed",
			joinMode:    "closed",
			principal:   "applicant@example.com",
			expectError: true,
			errContains: "does not accept applications",
		},
		{
			name:        "rejected when email is empty",
			joinMode:    "application",
			principal:   "",
			expectError: true,
			errContains: "unable to determine user email from identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()

			// Update committee-1 settings with the desired join_mode
			repo.SetJoinMode("committee-1", tt.joinMode)

			ctx := context.WithValue(context.Background(), constants.EmailContextID, tt.principal)
			msg := "I'd like to join"

			result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
				UID:     "committee-1",
				Message: &msg,
			})

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.NotEmpty(t, *result.UID)
				assert.Equal(t, "committee-1", *result.CommitteeUID)
				assert.Equal(t, "pending", result.Status)
			}
		})
	}
}

func TestApproveApplication(t *testing.T) {
	tests := []struct {
		name        string
		seedStatus  string
		expectError bool
	}{
		{
			name:        "successful approval of pending application",
			seedStatus:  "pending",
			expectError: false,
		},
		{
			name:        "cannot approve already rejected application",
			seedStatus:  "rejected",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()

			app := &model.CommitteeApplication{
				UID:          "app-approve-test",
				CommitteeUID: "committee-1",
				ApplicantUID: "user@example.com",
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeApplication(app)

			notes := "Welcome aboard"
			result, err := svc.ApproveApplication(context.Background(), &committeeservice.ApproveApplicationPayload{
				UID:            "committee-1",
				ApplicationUID: "app-approve-test",
				ReviewerNotes:  &notes,
			})

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "approved", result.Status)
			}
		})
	}
}

func TestApproveApplication_WrongCommittee(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	app := &model.CommitteeApplication{
		UID:          "app-wrong-committee",
		CommitteeUID: "committee-1",
		ApplicantUID: "user@example.com",
		Status:       "pending",
		CreatedAt:    time.Now(),
	}
	repo.AddCommitteeApplication(app)

	_, err := svc.ApproveApplication(context.Background(), &committeeservice.ApproveApplicationPayload{
		UID:            "committee-2", // wrong committee
		ApplicationUID: "app-wrong-committee",
	})

	require.Error(t, err)
	var nfErr *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nfErr)
	assert.Contains(t, nfErr.Message, "application not found in this committee")
}

func TestRejectApplication(t *testing.T) {
	tests := []struct {
		name        string
		seedStatus  string
		expectError bool
	}{
		{
			name:        "successful rejection of pending application",
			seedStatus:  "pending",
			expectError: false,
		},
		{
			name:        "cannot reject already approved application",
			seedStatus:  "approved",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()

			app := &model.CommitteeApplication{
				UID:          "app-reject-test",
				CommitteeUID: "committee-1",
				ApplicantUID: "user@example.com",
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeApplication(app)

			notes := "Not a fit"
			result, err := svc.RejectApplication(context.Background(), &committeeservice.RejectApplicationPayload{
				UID:            "committee-1",
				ApplicationUID: "app-reject-test",
				ReviewerNotes:  &notes,
			})

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "rejected", result.Status)
			}
		})
	}
}

// ==================== Join/Leave Endpoint Tests ====================

func TestJoinCommittee(t *testing.T) {
	tests := []struct {
		name        string
		joinMode    string
		principal   string
		expectError bool
		errContains string
	}{
		{
			name:        "successful join when open",
			joinMode:    "open",
			principal:   "joiner@example.com",
			expectError: false,
		},
		{
			name:        "rejected when join_mode is application",
			joinMode:    "application",
			principal:   "joiner@example.com",
			expectError: true,
			errContains: "join_mode is not open",
		},
		{
			name:        "rejected when join_mode is empty (closed)",
			joinMode:    "",
			principal:   "joiner@example.com",
			expectError: true,
			errContains: "join_mode is not open",
		},
		{
			name:        "rejected when email is empty",
			joinMode:    "open",
			principal:   "",
			expectError: true,
			errContains: "unable to determine user email from identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()

			// Update committee-1 settings with the desired join_mode
			repo.SetJoinMode("committee-1", tt.joinMode)

			// Configure mock orchestrator to return a member on CreateMember
			mockOrch.createMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "new-member-uid",
					CommitteeUID: "committee-1",
					Email:        tt.principal,
					Status:       "Active",
				},
			}

			ctx := context.WithValue(context.Background(), constants.EmailContextID, tt.principal)

			result, err := svc.JoinCommittee(ctx, &committeeservice.JoinCommitteePayload{
				UID:   "committee-1",
				XSync: false,
			})

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}
		})
	}
}

func TestLeaveCommittee(t *testing.T) {
	tests := []struct {
		name          string
		principal     string
		seedMember    bool
		seedMemberUID string
		expectError   bool
		errContains   string
	}{
		{
			name:          "successful leave",
			principal:     "leave-test-unique@example.com",
			seedMember:    true,
			seedMemberUID: "leave-member-uid",
			expectError:   false,
		},
		{
			name:        "not a member",
			principal:   "notamember@example.com",
			seedMember:  false,
			expectError: true,
			errContains: "you are not a member",
		},
		{
			name:        "empty email",
			principal:   "",
			seedMember:  false,
			expectError: true,
			errContains: "unable to determine user email from identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()

			if tt.seedMember {
				repo.AddCommitteeMember("committee-1", &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          tt.seedMemberUID,
						CommitteeUID: "committee-1",
						Email:        tt.principal,
						Status:       "Active",
					},
				})
				mockOrch.deleteError = nil
			}

			ctx := context.WithValue(context.Background(), constants.EmailContextID, tt.principal)

			err := svc.LeaveCommittee(ctx, &committeeservice.LeaveCommitteePayload{
				UID:   "committee-1",
				XSync: true,
			})

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				// Verify the orchestrator was called with the correct member UID
				require.Len(t, mockOrch.deleteCalls, 1)
				assert.Equal(t, tt.seedMemberUID, mockOrch.deleteCalls[0].uid)
			}
		})
	}
}
