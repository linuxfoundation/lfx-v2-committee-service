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
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	internalservice "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	authpkg "github.com/linuxfoundation/lfx-v2-committee-service/pkg/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
)

// testCtx builds a request context with the given principal (LFX username).
func testCtx(principal string) context.Context {
	return context.WithValue(context.Background(), constants.PrincipalContextID, principal)
}

// mockReaderForPrincipalEmail maps the principal's Auth0 sub to the caller's email for EmailsByAuthToken.
func mockReaderForPrincipalEmail(principal, email string) *mockUserReader {
	return newMockUserReader(authpkg.MapUsernameToAuthSub(principal), email)
}

// mockUserReader is a simple in-memory UserReader for tests.
// EmailsByAuthToken maps auth token → primary email.
type mockUserReader struct {
	emails      map[string]string              // auth token → primary email (for EmailsByAuthToken)
	usernames   map[string]string              // email → username (for UsernameByEmail)
	metadataMap map[string]*model.UserMetadata // username → metadata (for UserMetadataByPrincipal)
	metadataErr error                          // if set, returned by UserMetadataByPrincipal for all usernames
}

func newMockUserReader(pairs ...string) *mockUserReader {
	m := &mockUserReader{
		emails:      make(map[string]string),
		usernames:   make(map[string]string),
		metadataMap: make(map[string]*model.UserMetadata),
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.emails[pairs[i]] = pairs[i+1]
	}
	return m
}

// withUsernames populates the mock's email→username map and returns the same receiver for chaining.
func (m *mockUserReader) withUsernames(pairs ...string) *mockUserReader {
	for i := 0; i+1 < len(pairs); i += 2 {
		m.usernames[pairs[i]] = pairs[i+1]
	}
	return m
}

// withMetadata registers a UserMetadata response for a given username.
func (m *mockUserReader) withMetadata(username string, meta *model.UserMetadata) *mockUserReader {
	m.metadataMap[username] = meta
	return m
}

// withMetadataErr configures a global error returned by UserMetadataByPrincipal for all usernames.
func (m *mockUserReader) withMetadataErr(err error) *mockUserReader {
	m.metadataErr = err
	return m
}

func (m *mockUserReader) UsernameByEmail(ctx context.Context, email string) (string, error) {
	if username, ok := m.usernames[email]; ok {
		return username, nil
	}
	return "", errs.NewNotFound("mock: username not found for email: " + email)
}

func (m *mockUserReader) EmailsByAuthToken(_ context.Context, authToken string) (*model.UserEmails, error) {
	if authToken == "" {
		return nil, errs.NewValidation("mock: auth token is empty")
	}
	email, ok := m.emails[authToken]
	if !ok {
		return nil, errs.NewNotFound("mock: auth token not found: " + authToken)
	}
	return &model.UserEmails{PrimaryEmail: email}, nil
}

func (m *mockUserReader) UserMetadataByPrincipal(_ context.Context, sub string) (*model.UserMetadata, error) {
	if m.metadataErr != nil {
		return nil, m.metadataErr
	}
	if meta, ok := m.metadataMap[sub]; ok {
		return meta, nil
	}
	return nil, nil
}

// Mock orchestrator for testing service layer
type mockCommitteeWriterOrchestrator struct {
	deleteError       error
	deleteCalls       []deleteCall
	updateMember      *model.CommitteeMember
	updateMemberErr   error
	updateMemberCalls []updateMemberCall
	createMember      *model.CommitteeMember
	createMemberErr   error
	createMemberCalls []*model.CommitteeMember
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
	m.createMemberCalls = append(m.createMemberCalls, member)
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

func (m *mockCommitteeWriterOrchestrator) DeleteMember(ctx context.Context, uid string, revision uint64, sync bool, skipNotification bool) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{uid: uid, revision: revision})
	return m.deleteError
}

// ReassignMember mirrors the real orchestrator: create the new holder, delete the old, and roll back
// the created member (an extra delete) if the delete fails, so reassign tests can assert the calls.
func (m *mockCommitteeWriterOrchestrator) ReassignMember(ctx context.Context, oldMemberUID string, oldRevision uint64, newMember *model.CommitteeMember, sync bool) (*model.CommitteeMember, error) {
	created, err := m.CreateMember(ctx, newMember, sync)
	if err != nil {
		return nil, err
	}
	if errDelete := m.DeleteMember(ctx, oldMemberUID, oldRevision, sync, false); errDelete != nil {
		if created != nil && created.UID != "" {
			_ = m.DeleteMember(ctx, created.UID, 0, sync, false) // rollback attempt
		}
		return nil, errDelete
	}
	return created, nil
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
		userReader:                  newMockUserReader(),
	}

	return service, mockOrchestrator
}

// mockInviteSender records SendInvite calls and optionally returns a fixed error.
type mockInviteSender struct {
	calls  []inviteapi.SendInviteRequest
	retErr error
}

func (m *mockInviteSender) SendInvite(_ context.Context, req inviteapi.SendInviteRequest) (port.InviteResult, error) {
	m.calls = append(m.calls, req)
	if m.retErr != nil {
		return port.InviteResult{}, m.retErr
	}
	return port.InviteResult{InviteUID: "remote-invite-uid"}, nil
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
		inviteSender:                &mockInviteSender{},
		lfxSelfServeBaseURL:         "https://app.test.lfx.dev",
		userReader:                  newMockUserReader(),
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
				assert.Equal(t, "Technical Advisory Committee", *result.CommitteeName)
				assert.True(t, *result.OrganizationRequired)
			}
		})
	}
}

func TestGetInvite_SettingsFailurePreservesOrganizationRequired(t *testing.T) {
	// When GetSettings fails (committee has no settings), enrichInviteFromCommittee must
	// leave the existing OrganizationRequired value intact rather than clobbering it with
	// a value derived from nil settings (which would incorrectly evaluate to false).
	svc, _, repo := setupServiceTestWithRepo()

	// Add a committee with no settings — GetSettings will return NotFound.
	repo.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:          "no-settings-committee",
			ProjectUID:   "proj-1",
			Name:         "No Settings Committee",
			EnableVoting: false,
		},
		CommitteeSettings: nil,
	})
	// Seed an invite with OrganizationRequired already set to true.
	repo.AddCommitteeInvite(&model.CommitteeInvite{
		UID:                  "invite-no-settings",
		CommitteeUID:         "no-settings-committee",
		InviteeEmail:         "test@example.com",
		Status:               "pending",
		OrganizationRequired: true,
	})

	result, err := svc.GetInvite(context.Background(), &committeeservice.GetInvitePayload{
		UID:       "no-settings-committee",
		InviteUID: "invite-no-settings",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// OrganizationRequired must be preserved despite the settings failure.
	require.NotNil(t, result.OrganizationRequired)
	assert.True(t, *result.OrganizationRequired, "settings failure must not clobber a correctly-stored OrganizationRequired=true")
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
			sender := svc.inviteSender.(*mockInviteSender)

			result, err := svc.CreateInvite(context.Background(), tt.payload)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Empty(t, sender.calls)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.NotEmpty(t, *result.UID)
				assert.Equal(t, tt.payload.UID, *result.CommitteeUID)
				assert.Equal(t, tt.payload.InviteeEmail, *result.InviteeEmail)
				assert.Equal(t, "pending", result.Status)
				assert.Equal(t, "Technical Advisory Committee", *result.CommitteeName)
				assert.True(t, *result.OrganizationRequired)

				require.Len(t, sender.calls, 1)
				call := sender.calls[0]
				require.NotNil(t, call.Recipient)
				assert.Equal(t, tt.payload.InviteeEmail, call.Recipient.Email)
				require.NotNil(t, call.Resource)
				assert.Equal(t, tt.payload.UID, call.Resource.UID)
				assert.Equal(t, "Technical Advisory Committee", call.Resource.Name)
				assert.Equal(t, "group", call.Resource.Type)
				assert.Equal(t, "https://app.test.lfx.dev/project/groups/"+tt.payload.UID, call.ReturnURL)
			}
		})
	}
}

func TestCreateInvite_Organization(t *testing.T) {
	t.Run("optional without organization on org-gated committee", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()

		result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: "no-org@example.com",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Nil(t, result.Organization)
	})

	t.Run("stores organization from payload", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()

		result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: "with-org@example.com",
			Organization: sampleInviteOrganizationPayload(),
		})
		require.NoError(t, err)
		require.NotNil(t, result.Organization)
		require.NotNil(t, result.Organization.Name)
		assert.Equal(t, "The Linux Foundation", *result.Organization.Name)
		require.NotNil(t, result.Organization.Website)
		assert.Equal(t, "https://linuxfoundation.org", *result.Organization.Website)
	})

	t.Run("optional on open committee", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()

		result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-2",
			InviteeEmail: "open@example.com",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Nil(t, result.Organization)
	})

	t.Run("reinstate preserves stored organization", func(t *testing.T) {
		svc, _, repo := setupServiceTestWithRepo()

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "revoked-with-org",
			CommitteeUID: "committee-1",
			InviteeEmail: "reinvite@example.com",
			Status:       "revoked",
			Organization: &model.CommitteeMemberOrganization{
				Name:    "Stored Org",
				Website: "https://stored.org",
			},
			CreatedAt: time.Now(),
		})

		result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: "reinvite@example.com",
			Role:         stringPtr("chair"),
		})
		require.NoError(t, err)
		require.NotNil(t, result.Organization)
		require.NotNil(t, result.Organization.Name)
		assert.Equal(t, "Stored Org", *result.Organization.Name)
		require.NotNil(t, result.Organization.Website)
		assert.Equal(t, "https://stored.org", *result.Organization.Website)
	})
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

func TestCreateInvite_RevokedInviteReinstated(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	// Seed a revoked invite
	revoked := &model.CommitteeInvite{
		UID:          "revoked-invite",
		CommitteeUID: "committee-1",
		InviteeEmail: "reinvite@example.com",
		Status:       "revoked",
		CreatedAt:    time.Now(),
	}
	repo.AddCommitteeInvite(revoked)

	// Re-invite the same person
	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "reinvite@example.com",
		Role:         stringPtr("chair"),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// Should return the existing invite reinstated, not a new one
	assert.Equal(t, revoked.UID, *result.UID)
	assert.Equal(t, "pending", result.Status)
	require.NotNil(t, result.Role)
	assert.Equal(t, "chair", *result.Role)

	sender := svc.inviteSender.(*mockInviteSender)
	require.Len(t, sender.calls, 1)
	require.NotNil(t, sender.calls[0].Recipient)
	assert.Equal(t, "reinvite@example.com", sender.calls[0].Recipient.Email)
	// SendInviteRequest.Role uses the invite-service permission vocabulary
	// ("Member"), not the committee role ("chair") which lives on the persisted
	// invite record and is applied on acceptance.
	assert.Equal(t, "Member", sender.calls[0].Role)
}

func TestCreateInvite_InviteSenderFailureDoesNotFailRequest(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()
	sender := &mockInviteSender{retErr: assert.AnError}
	svc.inviteSender = sender

	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "besteffort@example.com",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "pending", result.Status)
	// Sender must actually be invoked — otherwise this test would still pass
	// if dispatch were accidentally removed or short-circuited.
	require.Len(t, sender.calls, 1)
	require.NotNil(t, sender.calls[0].Recipient)
	assert.Equal(t, "besteffort@example.com", sender.calls[0].Recipient.Email)
}

func TestCreateInvite_NilInviteSenderSkipsDispatch(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()
	svc.inviteSender = nil

	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "nosender@example.com",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "pending", result.Status)
}

func TestCreateInvite_RecipientNameResolution(t *testing.T) {
	// Each subtest uses a unique email to avoid uniqueness conflicts in the global mock repo.
	const username = "knownuser"

	t.Run("invitee has LFID — name from metadata GivenName+FamilyName", func(t *testing.T) {
		const email = "name-givenname@example.com"
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = newMockUserReader().
			withUsernames(email, username).
			withMetadata(username, &model.UserMetadata{GivenName: "Jane", FamilyName: "Smith"})

		_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: email,
		})
		require.NoError(t, err)

		sender := svc.inviteSender.(*mockInviteSender)
		require.Len(t, sender.calls, 1)
		require.NotNil(t, sender.calls[0].Recipient)
		assert.Equal(t, "Jane Smith", sender.calls[0].Recipient.Name)
	})

	t.Run("invitee has LFID with only combined Name — falls back to meta.Name", func(t *testing.T) {
		const email = "name-combined@example.com"
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = newMockUserReader().
			withUsernames(email, username).
			withMetadata(username, &model.UserMetadata{Name: "Jane Smith"})

		_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: email,
		})
		require.NoError(t, err)

		sender := svc.inviteSender.(*mockInviteSender)
		require.Len(t, sender.calls, 1)
		assert.Equal(t, "Jane Smith", sender.calls[0].Recipient.Name)
	})

	t.Run("invitee has no LFID — Recipient.Name is empty, invite still sends", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		// Default mockUserReader returns NotFound for any UsernameByEmail call.
		svc.userReader = newMockUserReader()

		_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: "name-nolfid@example.com",
		})
		require.NoError(t, err)

		sender := svc.inviteSender.(*mockInviteSender)
		require.Len(t, sender.calls, 1)
		assert.Empty(t, sender.calls[0].Recipient.Name, "name should be blank when invitee has no LFID")
	})

	t.Run("metadata lookup error — Recipient.Name is empty, invite still sends", func(t *testing.T) {
		const email = "name-metaerr@example.com"
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = newMockUserReader().
			withUsernames(email, username).
			withMetadataErr(assert.AnError)

		_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: email,
		})
		require.NoError(t, err)

		sender := svc.inviteSender.(*mockInviteSender)
		require.Len(t, sender.calls, 1)
		assert.Empty(t, sender.calls[0].Recipient.Name, "name should be blank when metadata lookup errors")
	})

	t.Run("reinstated revoked invite also carries resolved name", func(t *testing.T) {
		const email = "name-reinstate@example.com"
		svc, _, repo := setupServiceTestWithRepo()
		svc.userReader = newMockUserReader().
			withUsernames(email, username).
			withMetadata(username, &model.UserMetadata{GivenName: "Jane", FamilyName: "Smith"})
		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "revoked-invite-name",
			CommitteeUID: "committee-1",
			InviteeEmail: email,
			Status:       "revoked",
			CreatedAt:    time.Now(),
		})

		_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-1",
			InviteeEmail: email,
		})
		require.NoError(t, err)

		sender := svc.inviteSender.(*mockInviteSender)
		require.Len(t, sender.calls, 1)
		assert.Equal(t, "Jane Smith", sender.calls[0].Recipient.Name)
	})
}

func TestCreateInvite_NonRevokedDuplicateRejected(t *testing.T) {
	for _, status := range []string{"pending", "declined", "accepted"} {
		t.Run(status, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()

			existing := &model.CommitteeInvite{
				UID:          "existing-invite",
				CommitteeUID: "committee-1",
				InviteeEmail: "dup@example.com",
				Status:       status,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeInvite(existing)

			_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
				UID:          "committee-1",
				InviteeEmail: "dup@example.com",
			})

			require.Error(t, err)
			var conflictErr *committeeservice.ConflictError
			require.ErrorAs(t, err, &conflictErr)
		})
	}
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
			name:        "successful revocation of declined invite",
			seedStatus:  "declined",
			expectError: false,
		},
		{
			name:        "cannot revoke already accepted invite",
			seedStatus:  "accepted",
			expectError: true,
			errContains: "already been processed",
		},
		{
			name:        "cannot revoke already revoked invite",
			seedStatus:  "revoked",
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
			name:        "successful accept of previously declined invite",
			seedStatus:  "declined",
			principal:   "accept@example.com",
			expectError: false,
		},
		{
			name:        "cannot accept already accepted invite",
			seedStatus:  "accepted",
			principal:   "accept@example.com",
			expectError: true,
		},
		{
			name:        "cannot accept revoked invite",
			seedStatus:  "revoked",
			principal:   "accept@example.com",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()
			svc.userReader = mockReaderForPrincipalEmail(tt.principal, tt.principal)

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

			ctx := testCtx(tt.principal)
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
	svc.userReader = mockReaderForPrincipalEmail("attacker@example.com", "attacker@example.com")
	ctx := testCtx("attacker@example.com")
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

func TestAcceptInvite_Organization(t *testing.T) {
	t.Run("empty payload allowed", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-empty-body",
			CommitteeUID: "committee-1",
			InviteeEmail: "accept@example.com",
			Status:       "pending",
			CreatedAt:    time.Now(),
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-1",
				CommitteeUID: "committee-1",
				Email:        "accept@example.com",
				Status:       "Active",
			},
		}

		_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-empty-body",
		})
		require.NoError(t, err)
	})

	t.Run("uses payload organization when ID present", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-org-payload",
			CommitteeUID: "committee-1",
			InviteeEmail: "accept@example.com",
			Status:       "pending",
			Organization: &model.CommitteeMemberOrganization{
				Name:    "Invite Org",
				Website: "https://invite.org",
			},
			CreatedAt: time.Now(),
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-1",
				CommitteeUID: "committee-1",
				Email:        "accept@example.com",
				Status:       "Active",
			},
		}

		orgID := "org-123456"
		payloadName := "Payload Org"
		payloadWebsite := "https://payload.org"
		_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-org-payload",
			Body: &committeeservice.AcceptInviteOptionalBody{
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					ID:      &orgID,
					Name:    &payloadName,
					Website: &payloadWebsite,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		assert.Equal(t, "org-123456", member.Organization.ID)
		assert.Equal(t, "Payload Org", member.Organization.Name)
		assert.Equal(t, "https://payload.org", member.Organization.Website)
	})

	t.Run("ignores partial payload without organization ID", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-org-merge",
			CommitteeUID: "committee-1",
			InviteeEmail: "accept@example.com",
			Status:       "pending",
			Organization: &model.CommitteeMemberOrganization{
				Name:    "Invite Org",
				Website: "https://invite.org",
			},
			CreatedAt: time.Now(),
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-1",
				CommitteeUID: "committee-1",
				Email:        "accept@example.com",
				Status:       "Active",
			},
		}

		overrideName := "Payload Org"
		_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-org-merge",
			Body: &committeeservice.AcceptInviteOptionalBody{
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					Name: &overrideName,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		assert.Equal(t, "Invite Org", member.Organization.Name)
		assert.Equal(t, "https://invite.org", member.Organization.Website)
	})

	t.Run("falls back to invite organization", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-org-fallback",
			CommitteeUID: "committee-1",
			InviteeEmail: "accept@example.com",
			Status:       "pending",
			Organization: &model.CommitteeMemberOrganization{
				Name:    "Invite Org",
				Website: "https://invite.org",
			},
			CreatedAt: time.Now(),
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-1",
				CommitteeUID: "committee-1",
				Email:        "accept@example.com",
				Status:       "Active",
			},
		}

		_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-org-fallback",
		})
		require.NoError(t, err)
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		assert.Equal(t, "Invite Org", member.Organization.Name)
		assert.Equal(t, "https://invite.org", member.Organization.Website)
	})
}

func sampleInviteOrganizationPayload() *struct {
	ID      *string
	Name    *string
	Website *string
} {
	name := "The Linux Foundation"
	website := "https://linuxfoundation.org"
	return &struct {
		ID      *string
		Name    *string
		Website *string
	}{
		Name:    &name,
		Website: &website,
	}
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
		{
			name:        "cannot decline already declined invite",
			seedStatus:  "declined",
			principal:   "decline@example.com",
			expectError: true,
		},
		{
			name:        "cannot decline revoked invite",
			seedStatus:  "revoked",
			principal:   "decline@example.com",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()
			svc.userReader = mockReaderForPrincipalEmail(tt.principal, tt.principal)

			invite := &model.CommitteeInvite{
				UID:          "invite-decline-test",
				CommitteeUID: "committee-1",
				InviteeEmail: "decline@example.com",
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
			}
			repo.AddCommitteeInvite(invite)

			ctx := testCtx(tt.principal)
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
	svc.userReader = mockReaderForPrincipalEmail("attacker@example.com", "attacker@example.com")
	ctx := testCtx("attacker@example.com")
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
					UID:            "get-app-001",
					CommitteeUID:   "committee-1",
					ApplicantEmail: "get-app-unique@example.com",
					Message:        "I want to join",
					Status:         "pending",
					CreatedAt:      time.Now().UTC(),
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
					UID:            "get-app-002",
					CommitteeUID:   "committee-2",
					ApplicantEmail: "other-applicant@example.com",
					Message:        "Wrong committee",
					Status:         "pending",
					CreatedAt:      time.Now().UTC(),
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
			name:        "rejected when principal is empty",
			joinMode:    "application",
			principal:   "",
			expectError: true,
			errContains: "unable to determine user identity from token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()
			svc.userReader = mockReaderForPrincipalEmail(tt.principal, tt.principal)

			// Update committee-1 settings with the desired join_mode
			repo.SetJoinMode("committee-1", tt.joinMode)

			ctx := testCtx(tt.principal)
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

func TestSubmitApplication_RejectedAppReinstated(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	repo.SetJoinMode("committee-1", "application")

	// Seed a rejected application
	rejected := &model.CommitteeApplication{
		UID:            "rejected-app",
		CommitteeUID:   "committee-1",
		ApplicantEmail: "reapplicant@example.com",
		Status:         "rejected",
		ReviewerNotes:  "not a good fit",
		CreatedAt:      time.Now(),
	}
	repo.AddCommitteeApplication(rejected)

	svc.userReader = mockReaderForPrincipalEmail("reapplicant@example.com", "reapplicant@example.com")
	newMsg := "I've improved since last time"
	ctx := testCtx("reapplicant@example.com")
	result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
		UID:     "committee-1",
		Message: &newMsg,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// Should return the existing application reinstated, not a new one
	assert.Equal(t, rejected.UID, *result.UID)
	assert.Equal(t, "pending", result.Status)
	require.NotNil(t, result.Message)
	assert.Equal(t, newMsg, *result.Message)
}

func TestSubmitApplication_NonRejectedDuplicateRejected(t *testing.T) {
	for _, status := range []string{"pending", "approved"} {
		t.Run(status, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()
			repo.SetJoinMode("committee-1", "application")

			existing := &model.CommitteeApplication{
				UID:            "existing-app",
				CommitteeUID:   "committee-1",
				ApplicantEmail: "applicant@example.com",
				Status:         status,
				CreatedAt:      time.Now(),
			}
			repo.AddCommitteeApplication(existing)

			svc.userReader = mockReaderForPrincipalEmail("applicant@example.com", "applicant@example.com")
			ctx := testCtx("applicant@example.com")
			_, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
				UID: "committee-1",
			})

			require.Error(t, err)
			var conflictErr *committeeservice.ConflictError
			require.ErrorAs(t, err, &conflictErr)
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
			svc, mockOrch, repo := setupServiceTestWithRepo()

			app := &model.CommitteeApplication{
				UID:            "app-approve-test",
				CommitteeUID:   "committee-1",
				ApplicantEmail: "user@example.com",
				Status:         tt.seedStatus,
				CreatedAt:      time.Now(),
			}
			repo.AddCommitteeApplication(app)

			mockOrch.createMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-1",
					Email:        "user@example.com",
					Status:       "Active",
				},
			}

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
				assert.Equal(t, "Active", result.Status)
			}
		})
	}
}

func TestApproveApplication_WrongCommittee(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()

	app := &model.CommitteeApplication{
		UID:            "app-wrong-committee",
		CommitteeUID:   "committee-1",
		ApplicantEmail: "user@example.com",
		Status:         "pending",
		CreatedAt:      time.Now(),
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
				UID:            "app-reject-test",
				CommitteeUID:   "committee-1",
				ApplicantEmail: "user@example.com",
				Status:         tt.seedStatus,
				CreatedAt:      time.Now(),
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
		username    string
		email       string
		expectError bool
		errContains string
	}{
		{
			name:        "successful join when open",
			joinMode:    "open",
			username:    "joiner",
			email:       "joiner@example.com",
			expectError: false,
		},
		{
			name:        "rejected when join_mode is application",
			joinMode:    "application",
			username:    "joiner",
			email:       "joiner@example.com",
			expectError: true,
			errContains: "join_mode is not open",
		},
		{
			name:        "rejected when join_mode is empty (closed)",
			joinMode:    "",
			username:    "joiner",
			email:       "joiner@example.com",
			expectError: true,
			errContains: "join_mode is not open",
		},
		{
			name:        "rejected when username is empty",
			joinMode:    "open",
			username:    "",
			email:       "joiner@example.com",
			expectError: true,
			errContains: "unable to determine user username from identity",
		},
		{
			name:        "rejected when principal has no email",
			joinMode:    "open",
			username:    "joiner",
			email:       "",
			expectError: true,
			errContains: "principal not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()
			if tt.email != "" {
				svc.userReader = mockReaderForPrincipalEmail(tt.username, tt.email)
			}

			// Update committee-1 settings with the desired join_mode
			repo.SetJoinMode("committee-1", tt.joinMode)

			// Configure mock orchestrator to return a member on CreateMember
			mockOrch.createMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "new-member-uid",
					CommitteeUID: "committee-1",
					Email:        tt.email,
					Status:       "Active",
				},
			}

			ctx := testCtx(tt.username)

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
			name:        "empty principal",
			principal:   "",
			seedMember:  false,
			expectError: true,
			errContains: "unable to determine user identity from token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()
			if tt.principal != "" {
				svc.userReader = mockReaderForPrincipalEmail(tt.principal, tt.principal)
			}

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

			ctx := testCtx(tt.principal)

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

func setupUploadDocumentService() (*committeeServicesrvc, *mock.MockLinkRepository, *mock.MockDocumentRepository) {
	mockRepo := mock.NewMockRepository()
	linkRepo := mock.NewMockLinkRepository()
	docRepo := mock.NewMockDocumentRepository()

	svc := &committeeServicesrvc{
		auth:      mock.NewMockAuthService(),
		storage:   mock.NewMockCommitteeReaderWriter(mockRepo),
		publisher: mock.NewMockCommitteePublisher(),
		linkReader: internalservice.NewLinkReaderOrchestrator(
			internalservice.WithLinkReader(linkRepo),
		),
		docWriter: internalservice.NewDocumentWriterOrchestrator(
			internalservice.WithDocumentWriter(docRepo),
			internalservice.WithDocumentReaderForWriter(docRepo),
		),
		docReader: internalservice.NewDocumentReaderOrchestrator(
			internalservice.WithDocumentReader(docRepo),
		),
	}
	return svc, linkRepo, docRepo
}

func TestUploadCommitteeDocument_FolderUID(t *testing.T) {
	const committeeUID = "committee-1"
	const otherCommitteeUID = "committee-2"
	fileData := []byte("hello world file content")

	tests := []struct {
		name         string
		folderUID    *string
		seedFolder   func(*mock.MockLinkRepository)
		expectError  bool
		expectBadReq bool
		checkResult  func(*testing.T, *committeeservice.CommitteeDocumentWithReadonlyAttributes)
	}{
		{
			name:        "no folder_uid — lands at root",
			folderUID:   nil,
			seedFolder:  func(_ *mock.MockLinkRepository) {},
			expectError: false,
			checkResult: func(t *testing.T, res *committeeservice.CommitteeDocumentWithReadonlyAttributes) {
				assert.Nil(t, res.FolderUID)
			},
		},
		{
			name:      "valid folder_uid — document nested in folder",
			folderUID: strPtr("folder-aaa"),
			seedFolder: func(repo *mock.MockLinkRepository) {
				_ = repo.CreateLinkFolder(context.Background(), &model.CommitteeLinkFolder{
					UID:          "folder-aaa",
					CommitteeUID: committeeUID,
					Name:         "Governance Docs",
				})
			},
			expectError: false,
			checkResult: func(t *testing.T, res *committeeservice.CommitteeDocumentWithReadonlyAttributes) {
				require.NotNil(t, res.FolderUID)
				assert.Equal(t, "folder-aaa", *res.FolderUID)
			},
		},
		{
			name:         "non-existent folder_uid — returns 400",
			folderUID:    strPtr("folder-does-not-exist"),
			seedFolder:   func(_ *mock.MockLinkRepository) {},
			expectError:  true,
			expectBadReq: true,
		},
		{
			name:      "folder_uid from different committee — returns 400",
			folderUID: strPtr("folder-bbb"),
			seedFolder: func(repo *mock.MockLinkRepository) {
				_ = repo.CreateLinkFolder(context.Background(), &model.CommitteeLinkFolder{
					UID:          "folder-bbb",
					CommitteeUID: otherCommitteeUID,
					Name:         "Other Committee Folder",
				})
			},
			expectError:  true,
			expectBadReq: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, linkRepo, _ := setupUploadDocumentService()
			tt.seedFolder(linkRepo)

			ctx := testCtx("testuser")
			payload := &committeeservice.UploadCommitteeDocumentPayload{
				UID:         committeeUID,
				Name:        "Test Document",
				FileName:    "test.pdf",
				ContentType: "application/pdf",
				File:        fileData,
				FolderUID:   tt.folderUID,
			}

			res, err := svc.UploadCommitteeDocument(ctx, payload)

			if tt.expectError {
				require.Error(t, err)
				if tt.expectBadReq {
					var badReq *committeeservice.BadRequestError
					assert.ErrorAs(t, err, &badReq, "expected a 400 bad-request error")
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, res)
			if tt.checkResult != nil {
				tt.checkResult(t, res)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestEnrichAllRoleFields_UpdateCommitteeSettings(t *testing.T) {
	basePayload := func() *committeeservice.UpdateCommitteeSettingsPayload {
		return &committeeservice.UpdateCommitteeSettingsPayload{
			UID:     strPtr("committee-uid-1"),
			IfMatch: strPtr("1"),
		}
	}

	tests := []struct {
		name         string
		payload      func() *committeeservice.UpdateCommitteeSettingsPayload
		usernames    []string                // email, username pairs
		setupReader  func(r *mockUserReader) // optional extra reader configuration (metadata, errors)
		useErrReader bool                    // use errUserReader (transport error) instead of mockUserReader
		wantErr      bool
		validate     func(t *testing.T, svc *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload)
	}{
		{
			name: "caller-supplied username replaced with resolved LFID",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("UNTRUSTED"), Email: strPtr("alice@example.com"), Name: strPtr("Alice")},
				}
				return p
			},
			usernames: []string{"alice@example.com", "alice-lfid"},
			validate: func(t *testing.T, _ *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				require.Len(t, p.Writers, 1)
				assert.Equal(t, "alice-lfid", *p.Writers[0].Username)
			},
		},
		{
			name: "unknown email — username cleared, stale LFID not persisted",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("ghost"), Email: strPtr("ghost@example.com"), Name: strPtr("Ghost")},
				}
				return p
			},
			// no usernames configured → NotFound → Username cleared; entry kept (converter only drops when both username and email are empty)
			validate: func(t *testing.T, _ *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				require.Len(t, p.Writers, 1)
				assert.Equal(t, "", *p.Writers[0].Username)
			},
		},
		{
			name: "missing email — username kept as-is (username-only entry is valid)",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Auditors = []*committeeservice.CommitteeUser{
					{Username: strPtr("bob"), Name: strPtr("Bob")}, // no email — username preserved
				}
				return p
			},
			validate: func(t *testing.T, _ *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				require.Len(t, p.Auditors, 1)
				// no email → enrichAllRoleFields skips; username is left untouched
				assert.Equal(t, "bob", *p.Auditors[0].Username)
			},
		},
		{
			name: "duplicate email across roles — looked up once, applied to both",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("bad1"), Email: strPtr("carol@example.com"), Name: strPtr("Carol W")},
				}
				p.Auditors = []*committeeservice.CommitteeUser{
					{Username: strPtr("bad2"), Email: strPtr("carol@example.com"), Name: strPtr("Carol A")},
				}
				return p
			},
			usernames: []string{"carol@example.com", "carol-lfid"},
			validate: func(t *testing.T, _ *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				assert.Equal(t, "carol-lfid", *p.Writers[0].Username)
				assert.Equal(t, "carol-lfid", *p.Auditors[0].Username)
			},
		},
		{
			name: "email case-normalised before lookup",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("x"), Email: strPtr("  Dave@Example.COM  "), Name: strPtr("Dave")},
				}
				return p
			},
			usernames: []string{"dave@example.com", "dave-lfid"},
			validate: func(t *testing.T, _ *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				assert.Equal(t, "dave-lfid", *p.Writers[0].Username)
			},
		},
		{
			name: "multiple distinct emails — each resolved independently",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("bad-w"), Email: strPtr("alice@example.com"), Name: strPtr("Alice")},
				}
				p.Auditors = []*committeeservice.CommitteeUser{
					{Username: strPtr("bad-a"), Email: strPtr("bob@example.com"), Name: strPtr("Bob")},
				}
				return p
			},
			usernames: []string{"alice@example.com", "alice-lfid", "bob@example.com", "bob-lfid"},
			validate: func(t *testing.T, _ *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				require.Len(t, p.Writers, 1)
				require.Len(t, p.Auditors, 1)
				assert.Equal(t, "alice-lfid", *p.Writers[0].Username)
				assert.Equal(t, "bob-lfid", *p.Auditors[0].Username)
			},
		},
		{
			name: "transport error from UsernameByEmail fails the request",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("x"), Email: strPtr("fail@example.com"), Name: strPtr("Fail")},
				}
				return p
			},
			useErrReader: true,
			wantErr:      true,
		},
		{
			name: "metadata enriched — name and avatar overwritten with auth-service values",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("UNTRUSTED"), Email: strPtr("carol@example.com"), Name: strPtr("Carol Old"), Avatar: strPtr("old-avatar.png")},
				}
				return p
			},
			usernames: []string{"carol@example.com", "carol-lfid"},
			setupReader: func(r *mockUserReader) {
				r.withMetadata("carol-lfid", &model.UserMetadata{
					Name:    "Carol Real Name",
					Picture: "https://auth.example.com/carol.png",
				})
			},
			validate: func(t *testing.T, svc *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				require.Len(t, p.Writers, 1)
				assert.Equal(t, "carol-lfid", *p.Writers[0].Username)
				assert.Equal(t, "Carol Real Name", *p.Writers[0].Name)
				assert.Equal(t, "https://auth.example.com/carol.png", *p.Writers[0].Avatar)
			},
		},
		{
			name: "metadata lookup fails — request still succeeds, name and avatar unchanged",
			payload: func() *committeeservice.UpdateCommitteeSettingsPayload {
				p := basePayload()
				p.Writers = []*committeeservice.CommitteeUser{
					{Username: strPtr("UNTRUSTED"), Email: strPtr("dave@example.com"), Name: strPtr("Dave"), Avatar: strPtr("")},
				}
				return p
			},
			usernames: []string{"dave@example.com", "dave-lfid"},
			setupReader: func(r *mockUserReader) {
				r.withMetadataErr(errs.NewUnexpected("nats: metadata timeout"))
			},
			validate: func(t *testing.T, svc *committeeServicesrvc, p *committeeservice.UpdateCommitteeSettingsPayload) {
				require.Len(t, p.Writers, 1)
				assert.Equal(t, "dave-lfid", *p.Writers[0].Username)
				assert.Equal(t, "Dave", *p.Writers[0].Name)
				assert.Equal(t, "", *p.Writers[0].Avatar)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := setupServiceTest()
			if tt.useErrReader {
				svc.userReader = &errUserReader{}
			} else {
				reader := newMockUserReader().withUsernames(tt.usernames...)
				if tt.setupReader != nil {
					tt.setupReader(reader)
				}
				svc.userReader = reader
			}
			p := tt.payload()
			err := svc.enrichAllRoleFields(context.Background(), p.Writers, p.Auditors)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, svc, p)
				}
			}
		})
	}
}

// errUserReader always returns a transport error from UsernameByEmail (not a NotFound).
type errUserReader struct{}

func (e *errUserReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	return "", errs.NewUnexpected("nats: connection timeout")
}

func (e *errUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, errs.NewUnexpected("nats: connection timeout")
}

func (e *errUserReader) UserMetadataByPrincipal(_ context.Context, _ string) (*model.UserMetadata, error) {
	return nil, errs.NewUnexpected("nats: connection timeout")
}

// TestUpdateCommitteeSettings_LFIDOnlyEntry verifies that passing a CommitteeUser with only a
// caller-supplied Username (no email) is accepted by enrichAllRoleFields — the username is left
// untouched and validateIdentityFields allows it through.
func TestUpdateCommitteeSettings_LFIDOnlyEntry(t *testing.T) {
	svc, _ := setupServiceTest()
	svc.userReader = newMockUserReader()

	writers := []*committeeservice.CommitteeUser{
		{Username: strPtr("project_super_admin")}, // no email — username-only is valid
	}

	err := svc.enrichAllRoleFields(context.Background(), writers)
	require.NoError(t, err, "username-only entry should not cause enrichment to fail")

	require.NotNil(t, writers[0].Username)
	assert.Equal(t, "project_super_admin", *writers[0].Username, "username should be left untouched")

	err = validateIdentityFields(writers, nil)
	require.NoError(t, err, "username-only entry should pass validateIdentityFields")
}

// TestEnrichAllRoleFields_M2MClientUsernamePreserved verifies that an Auth0 M2M client principal
// (username like "abc123@clients", no email) is left completely untouched by enrichAllRoleFields —
// no UsernameByEmail lookup is attempted and the username survives.
// Regression test for LFXV2-2133.
func TestEnrichAllRoleFields_M2MClientUsernamePreserved(t *testing.T) {
	svc, _ := setupServiceTest()
	// errUserReader causes any UsernameByEmail call to return a transport error —
	// if enrichAllRoleFields incorrectly attempts a lookup the test will fail.
	svc.userReader = &errUserReader{}

	username := "abc123@clients"
	writers := []*committeeservice.CommitteeUser{
		{Username: &username}, // no Email — M2M client with only a username
	}

	err := svc.enrichAllRoleFields(context.Background(), writers)
	require.NoError(t, err, "M2M username-only entry must not cause enrichment to fail")

	require.NotNil(t, writers[0].Username, "Username must not be nil after enrichment")
	assert.Equal(t, username, *writers[0].Username, "M2M username must be preserved unchanged")
	assert.Nil(t, writers[0].Email, "Email must remain nil — enrichment must not populate it")
	assert.Nil(t, writers[0].Name, "Name must remain nil — enrichment must not overwrite it")
	assert.Nil(t, writers[0].Avatar, "Avatar must remain nil — enrichment must not overwrite it")

	err = validateIdentityFields(writers, nil)
	require.NoError(t, err, "M2M username-only entry must pass validateIdentityFields")
}

func TestEnrichMember(t *testing.T) {
	tests := []struct {
		name        string
		member      func() *model.CommitteeMember
		setupReader func(r *mockUserReader)
		ctx         func() context.Context
		validate    func(t *testing.T, m *model.CommitteeMember)
	}{
		{
			name: "email-only — username resolved and profile enriched",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email: "alice@example.com",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("alice@example.com", "alice-lfid")
				r.withMetadata("alice-lfid", &model.UserMetadata{
					GivenName:  "Alice",
					FamilyName: "Smith",
					Picture:    "https://example.com/alice.png",
				})
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "alice-lfid", m.Username)
				assert.Equal(t, "Alice", m.FirstName)
				assert.Equal(t, "Smith", m.LastName)
				assert.Equal(t, "https://example.com/alice.png", m.Avatar)
			},
		},
		{
			name: "caller-supplied plain LFID overridden by sub when email is present",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email:    "alice@example.com",
						Username: "existing-lfid",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("alice@example.com", "other-lfid")
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "other-lfid", m.Username)
			},
		},
		{
			name: "email not found — username stays empty, no error",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email: "ghost@example.com",
					},
				}
			},
			// no usernames configured → UsernameByEmail returns NotFound
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Empty(t, m.Username)
				assert.Empty(t, m.FirstName)
			},
		},
		{
			name: "caller-supplied FirstName preserved when metadata also has name",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email:     "bob@example.com",
						FirstName: "Bobby",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("bob@example.com", "bob-lfid")
				r.withMetadata("bob-lfid", &model.UserMetadata{
					GivenName:  "Robert",
					FamilyName: "Jones",
				})
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "bob-lfid", m.Username)
				assert.Equal(t, "Bobby", m.FirstName) // caller value preserved
				assert.Equal(t, "Jones", m.LastName)  // auth value set (was empty)
			},
		},
		{
			name: "metadata lookup fails — username still set, profile unchanged",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email: "carol@example.com",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("carol@example.com", "carol-lfid")
				r.withMetadataErr(errs.NewUnexpected("nats: metadata timeout"))
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "carol-lfid", m.Username)
				assert.Empty(t, m.FirstName)
				assert.Empty(t, m.LastName)
			},
		},
		{
			name: "metadata lookup fails — pre-existing avatar left untouched (fail-soft)",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email:  "dave@example.com",
						Avatar: "https://example.com/old-dave.png",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("dave@example.com", "dave-lfid")
				r.withMetadataErr(errs.NewUnexpected("nats: metadata timeout"))
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "dave-lfid", m.Username)
				assert.Equal(t, "https://example.com/old-dave.png", m.Avatar)
			},
		},
		{
			name: "metadata returns no picture — stale avatar cleared",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email:  "erin@example.com",
						Avatar: "https://example.com/old-erin.png",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("erin@example.com", "erin-lfid")
				r.withMetadata("erin-lfid", &model.UserMetadata{GivenName: "Erin"})
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "erin-lfid", m.Username)
				assert.Empty(t, m.Avatar)
			},
		},
		{
			name: "empty email — nothing happens",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{}
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Empty(t, m.Username)
			},
		},
		{
			name: "skip-enrichment context preserves caller-supplied username",
			member: func() *model.CommitteeMember {
				return &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						Email:    "ghost@example.com",
						Username: "sync-lfid",
					},
				}
			},
			setupReader: func(r *mockUserReader) {
				r.withUsernames("ghost@example.com", "other-lfid")
			},
			validate: func(t *testing.T, m *model.CommitteeMember) {
				assert.Equal(t, "sync-lfid", m.Username)
			},
			ctx: func() context.Context {
				return internalservice.ContextWithSkipMemberEnrichment(context.Background())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := setupServiceTest()
			reader := newMockUserReader()
			if tt.setupReader != nil {
				tt.setupReader(reader)
			}
			svc.userReader = reader
			m := tt.member()
			ctx := context.Background()
			if tt.ctx != nil {
				ctx = tt.ctx()
			}
			svc.enrichMember(ctx, m)
			tt.validate(t, m)
		})
	}
}

func TestOrgSeatFromMember_AvatarUsername(t *testing.T) {
	t.Run("avatar and username mapped when present", func(t *testing.T) {
		seat := orgSeatFromMember(&model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:      "m-1",
				Username: "alice-lfid",
				Avatar:   "https://example.com/alice.png",
				Email:    "alice@example.com",
			},
		})
		if assert.NotNil(t, seat.Avatar) {
			assert.Equal(t, "https://example.com/alice.png", *seat.Avatar)
		}
		if assert.NotNil(t, seat.Username) {
			assert.Equal(t, "alice-lfid", *seat.Username)
		}
	})

	t.Run("avatar and username omitted when empty", func(t *testing.T) {
		seat := orgSeatFromMember(&model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{UID: "m-2", Email: "bob@example.com"},
		})
		assert.Nil(t, seat.Avatar)
		assert.Nil(t, seat.Username)
	})
}

func TestEnrichMemberOrganization_AuthServiceMetadata(t *testing.T) {
	principal := "user@corp.com"
	authSub := authpkg.MapUsernameToAuthSub(principal)
	reader := &metadataByKeyReader{
		metadata: map[string]*model.UserMetadata{
			authSub: {Organization: "Example Corp"},
		},
	}
	svc := &committeeServicesrvc{userReader: reader}
	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, principal)
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			Email: "user@corp.com",
		},
	}

	svc.enrichMemberOrganization(ctx, member)
	assert.Equal(t, "Example Corp", member.Organization.Name)
	assert.Empty(t, member.Organization.Website)
}

type metadataByKeyReader struct {
	metadata map[string]*model.UserMetadata
}

func (r *metadataByKeyReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (r *metadataByKeyReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (r *metadataByKeyReader) UserMetadataByPrincipal(_ context.Context, key string) (*model.UserMetadata, error) {
	if meta, ok := r.metadata[key]; ok {
		return meta, nil
	}
	return nil, nil
}

func TestEnrichAllRoleFields_NilUserReader(t *testing.T) {
	svc, _ := setupServiceTest()
	svc.userReader = nil
	p := &committeeservice.UpdateCommitteeSettingsPayload{
		UID:     strPtr("committee-uid-1"),
		IfMatch: strPtr("1"),
		Writers: []*committeeservice.CommitteeUser{
			{Username: strPtr("x"), Email: strPtr("alice@example.com"), Name: strPtr("Alice")},
		},
	}
	err := svc.enrichAllRoleFields(context.Background(), p.Writers, p.Auditors)
	assert.Error(t, err)
}
