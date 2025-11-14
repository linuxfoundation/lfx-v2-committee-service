// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// Mock orchestrator for testing service layer
type mockCommitteeWriterOrchestrator struct {
	deleteError       error
	deleteCalls       []deleteCall
	updateMember      *model.CommitteeMember
	updateMemberErr   error
	updateMemberCalls []updateMemberCall
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
	}

	return service, mockOrchestrator
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
