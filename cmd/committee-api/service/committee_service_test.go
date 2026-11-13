// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	internalservice "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
)

// testCtx builds a request context with the given principal (LFX username).
func testCtx(principal string) context.Context {
	return context.WithValue(context.Background(), constants.PrincipalContextID, principal)
}

// mockReaderForPrincipalEmail maps the principal to the caller's email for EmailsByAuthToken.
func mockReaderForPrincipalEmail(principal, email string) *mockUserReader {
	return newMockUserReader(strings.TrimSpace(principal), email)
}

// mockUserReader is a simple in-memory UserReader for tests.
// EmailsByAuthToken maps auth token → primary email.
type mockUserReader struct {
	emails      map[string]string              // auth token → primary email (for EmailsByAuthToken)
	usernames   map[string]string              // email → username (for UsernameByEmail)
	metadataMap map[string]*model.UserMetadata // username → metadata (for UserMetadataByPrincipal)
	metadataErr error                          // if set, returned by UserMetadataByPrincipal for all usernames
	emailsErr   error                          // if set, returned by EmailsByAuthToken for all calls

	usernameByEmailCalls atomic.Int32 // number of UsernameByEmail calls
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
	m.usernameByEmailCalls.Add(1)
	if username, ok := m.usernames[email]; ok {
		return username, nil
	}
	return "", errs.NewNotFound("mock: username not found for email: " + email)
}

func (m *mockUserReader) EmailsByAuthToken(_ context.Context, authToken string) (*model.UserEmails, error) {
	if m.emailsErr != nil {
		return nil, m.emailsErr
	}
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
	deleteError          error
	deleteCalls          []deleteCall
	updateMember         *model.CommitteeMember
	updateMemberErr      error
	updateMemberCalls    []updateMemberCall
	createMember         *model.CommitteeMember
	createMemberErr      error
	createMemberCalls    []*model.CommitteeMember
	createMemberSyncArgs []bool
	// createMemberValidator optionally applies domain validation to the member,
	// mirroring the real orchestrator's member.Validate gate in CreateMember.
	createMemberValidator func(*model.CommitteeMember) error
	createMembersCalls    []createMembersCall
	createMembersResults  []*model.BulkMemberResult
	createMembersErr      error
}

type createMembersCall struct {
	committeeUID string
	members      []*model.CommitteeMember
	opts         internalservice.BulkMemberCreateOptions
}

type updateMemberCall struct {
	member         *model.CommitteeMember
	revision       uint64
	sync           bool
	skipEnrichment bool
}

type deleteCall struct {
	committeeUID string
	uid          string
	revision     uint64
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

func (m *mockCommitteeWriterOrchestrator) CreateMember(ctx context.Context, member *model.CommitteeMember, sync bool, skipEnrichment bool) (*model.CommitteeMember, error) {
	m.createMemberCalls = append(m.createMemberCalls, member)
	m.createMemberSyncArgs = append(m.createMemberSyncArgs, sync)
	if m.createMemberValidator != nil {
		if err := m.createMemberValidator(member); err != nil {
			return nil, err
		}
	}
	if m.createMemberErr != nil {
		return nil, m.createMemberErr
	}
	if m.createMember != nil {
		return m.createMember, nil
	}
	return nil, errs.NewUnexpected("not implemented for test")
}

func (m *mockCommitteeWriterOrchestrator) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64, sync bool, skipEnrichment bool) (*model.CommitteeMember, error) {
	m.updateMemberCalls = append(m.updateMemberCalls, updateMemberCall{member: member, revision: revision, sync: sync, skipEnrichment: skipEnrichment})
	if m.updateMemberErr != nil {
		return nil, m.updateMemberErr
	}
	return m.updateMember, nil
}

func (m *mockCommitteeWriterOrchestrator) DeleteMember(ctx context.Context, committeeUID, uid string, revision uint64, sync bool, skipNotification bool) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{committeeUID: committeeUID, uid: uid, revision: revision})
	return m.deleteError
}

// CreateMembers records the call, runs opts.Enrich on every row as the real orchestrator does for
// the rows it creates, and returns the configured results.
func (m *mockCommitteeWriterOrchestrator) CreateMembers(ctx context.Context, committeeUID string, members []*model.CommitteeMember, opts internalservice.BulkMemberCreateOptions) ([]*model.BulkMemberResult, error) {
	m.createMembersCalls = append(m.createMembersCalls, createMembersCall{committeeUID: committeeUID, members: members, opts: opts})
	if m.createMembersErr != nil {
		return nil, m.createMembersErr
	}
	if opts.Enrich != nil {
		for _, member := range members {
			opts.Enrich(ctx, member)
		}
	}
	return m.createMembersResults, nil
}

// ReassignMember mirrors the real orchestrator: create the new holder, delete the old, and roll back
// the created member (an extra delete) if the delete fails, so reassign tests can assert the calls.
func (m *mockCommitteeWriterOrchestrator) ReassignMember(ctx context.Context, oldMemberUID string, oldRevision uint64, newMember *model.CommitteeMember, sync bool) (*model.CommitteeMember, error) {
	created, err := m.CreateMember(ctx, newMember, sync, false)
	if err != nil {
		return nil, err
	}
	if errDelete := m.DeleteMember(ctx, newMember.CommitteeUID, oldMemberUID, oldRevision, sync, false); errDelete != nil {
		if created != nil && created.UID != "" {
			_ = m.DeleteMember(ctx, newMember.CommitteeUID, created.UID, 0, sync, false) // rollback attempt
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

func TestPublishInviteAccessControlMessage(t *testing.T) {
	invite := &model.CommitteeInvite{
		UID:          "invite-1",
		CommitteeUID: "committee-1",
		InviteeEmail: "invitee@example.com",
	}
	tests := []struct {
		name              string
		action            model.MessageAction
		sync              bool
		userReader        *mockUserReader
		identity          *model.IdentityLookup // member addition's lookup handed to the publisher
		wantLookupCalls   int32                 // checked only when identity is set
		wantRelation      map[string][]string
		wantExclusions    []string
		wantUpdateAccess  int
		wantDeleteAccess  int
		deleteAccessError error
	}{
		{
			name:             "resolved username",
			action:           model.ActionCreated,
			userReader:       newMockUserReader().withUsernames(invite.InviteeEmail, "invitee"),
			wantRelation:     map[string][]string{constants.RelationInvitee: {"invitee"}},
			wantUpdateAccess: 1,
		},
		{
			name:             "missing username",
			action:           model.ActionCreated,
			userReader:       newMockUserReader().withUsernames(invite.InviteeEmail, ""),
			wantExclusions:   []string{constants.RelationInvitee},
			wantUpdateAccess: 1,
		},
		{
			name:             "username lookup failure",
			action:           model.ActionCreated,
			userReader:       newMockUserReader(),
			wantExclusions:   []string{constants.RelationInvitee},
			wantUpdateAccess: 1,
		},
		{
			name:             "reuses a found identity for the invitee email",
			action:           model.ActionUpdated,
			userReader:       newMockUserReader().withUsernames(invite.InviteeEmail, "invitee-live"),
			identity:         model.NewIdentityLookup(invite.InviteeEmail, "invitee", nil),
			wantLookupCalls:  0,
			wantRelation:     map[string][]string{constants.RelationInvitee: {"invitee"}},
			wantUpdateAccess: 1,
		},
		{
			name:             "reuses a confirmed absence for the invitee email",
			action:           model.ActionUpdated,
			userReader:       newMockUserReader().withUsernames(invite.InviteeEmail, "invitee-live"),
			identity:         model.NewIdentityLookup(invite.InviteeEmail, "", errs.NewNotFound("user not found")),
			wantLookupCalls:  0,
			wantExclusions:   []string{constants.RelationInvitee},
			wantUpdateAccess: 1,
		},
		{
			name:             "unresolved identity looks up again",
			action:           model.ActionUpdated,
			userReader:       newMockUserReader().withUsernames(invite.InviteeEmail, "invitee"),
			identity:         model.NewIdentityLookup(invite.InviteeEmail, "", errs.NewServiceUnavailable("auth service down")),
			wantLookupCalls:  1,
			wantRelation:     map[string][]string{constants.RelationInvitee: {"invitee"}},
			wantUpdateAccess: 1,
		},
		{
			name:             "identity for another email looks up again",
			action:           model.ActionUpdated,
			userReader:       newMockUserReader().withUsernames(invite.InviteeEmail, "invitee"),
			identity:         model.NewIdentityLookup("other.last@example.com", "other-last", nil),
			wantLookupCalls:  1,
			wantRelation:     map[string][]string{constants.RelationInvitee: {"invitee"}},
			wantUpdateAccess: 1,
		},
		{
			name:             "defensive delete action with sync enabled",
			action:           model.ActionDeleted,
			sync:             true,
			userReader:       newMockUserReader(),
			wantDeleteAccess: 1,
		},
		{
			name:             "defensive delete action with sync disabled",
			action:           model.ActionDeleted,
			sync:             false,
			userReader:       newMockUserReader(),
			wantDeleteAccess: 1,
		},
		{
			name:              "defensive delete publish failure remains best effort",
			action:            model.ActionDeleted,
			sync:              true,
			userReader:        newMockUserReader(),
			wantDeleteAccess:  1,
			deleteAccessError: stderrors.New("delete_access publish failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := &mock.MockCommitteePublisher{
				DeleteAccessError: tt.deleteAccessError,
			}
			svc := &committeeServicesrvc{
				publisher:  publisher,
				userReader: tt.userReader,
			}

			svc.publishInviteAccessControlMessage(context.Background(), tt.action, invite, tt.sync, tt.identity)

			if tt.identity != nil {
				assert.Equal(t, tt.wantLookupCalls, tt.userReader.usernameByEmailCalls.Load(), "UsernameByEmail calls")
			}
			assert.Equal(t, tt.wantUpdateAccess, publisher.UpdateAccessCallCount)
			assert.Equal(t, tt.wantDeleteAccess, publisher.DeleteAccessCallCount)
			assert.Zero(t, publisher.MemberPutCallCount)
			assert.Zero(t, publisher.MemberRemoveCallCount)
			if tt.action == model.ActionDeleted {
				msg, ok := publisher.LastDeleteAccessMessage.(fgatypes.GenericFGAMessage)
				require.True(t, ok)
				assert.Equal(t, "committee_invite", msg.ObjectType)
				assert.Equal(t, "delete_access", msg.Operation)
				assert.Equal(t, fgatypes.GenericDeleteData{UID: invite.UID}, msg.Data)
				return
			}

			msg, ok := publisher.LastUpdateAccessMessage.(fgatypes.GenericFGAMessage)
			require.True(t, ok)
			assert.Equal(t, "committee_invite", msg.ObjectType)
			assert.Equal(t, "update_access", msg.Operation)
			data, ok := msg.Data.(fgatypes.GenericAccessData)
			require.True(t, ok)
			assert.Equal(t, invite.UID, data.UID)
			assert.Equal(t, map[string][]string{
				constants.RelationCommittee: {invite.CommitteeUID},
			}, data.References)
			assert.Equal(t, tt.wantRelation, data.Relations)
			assert.Equal(t, tt.wantExclusions, data.ExcludeRelations)
		})
	}
}

func TestPublishApplicationAccessControlMessage(t *testing.T) {
	application := &model.CommitteeApplication{
		UID:            "app-1",
		CommitteeUID:   "committee-1",
		ApplicantEmail: "first.last@example.com",
	}
	tests := []struct {
		name             string
		userReader       *mockUserReader
		wantRelation     map[string][]string
		wantExclusions   []string
		wantUpdateAccess int
	}{
		{
			name:             "resolved username",
			userReader:       newMockUserReader().withUsernames(application.ApplicantEmail, "first-last"),
			wantRelation:     map[string][]string{constants.RelationApplicant: {"first-last"}},
			wantUpdateAccess: 1,
		},
		{
			name:             "missing username",
			userReader:       newMockUserReader().withUsernames(application.ApplicantEmail, ""),
			wantExclusions:   []string{constants.RelationApplicant},
			wantUpdateAccess: 1,
		},
		{
			name:             "username lookup failure",
			userReader:       newMockUserReader(), // no mapping → NotFound
			wantExclusions:   []string{constants.RelationApplicant},
			wantUpdateAccess: 1,
		},
		{
			name:             "nil userReader",
			userReader:       nil,
			wantExclusions:   []string{constants.RelationApplicant},
			wantUpdateAccess: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := &mock.MockCommitteePublisher{}
			// Explicitly set to nil interface (not nil typed pointer) when no reader is provided,
			// otherwise a nil *mockUserReader assigned to a port.UserReader field is a non-nil interface.
			var userReaderForTest port.UserReader
			if tt.userReader != nil {
				userReaderForTest = tt.userReader
			}
			svc := &committeeServicesrvc{
				publisher:  publisher,
				userReader: userReaderForTest,
			}

			svc.publishApplicationAccessControlMessage(context.Background(), application, nil)

			assert.Equal(t, tt.wantUpdateAccess, publisher.UpdateAccessCallCount)
			assert.Zero(t, publisher.DeleteAccessCallCount)
			assert.Zero(t, publisher.MemberPutCallCount)
			assert.Zero(t, publisher.MemberRemoveCallCount)

			msg, ok := publisher.LastUpdateAccessMessage.(fgatypes.GenericFGAMessage)
			require.True(t, ok)
			assert.Equal(t, "committee_application", msg.ObjectType)
			assert.Equal(t, "update_access", msg.Operation)
			data, ok := msg.Data.(fgatypes.GenericAccessData)
			require.True(t, ok)
			assert.Equal(t, application.UID, data.UID)
			assert.Equal(t, map[string][]string{
				constants.RelationCommittee: {application.CommitteeUID},
			}, data.References)
			assert.Equal(t, tt.wantRelation, data.Relations)
			assert.Equal(t, tt.wantExclusions, data.ExcludeRelations)
		})
	}
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
				assert.Equal(t, "committee-123", calls[0].committeeUID)
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

// identityTestUserReader is a UserReader whose UsernameByEmail calls can be counted.
type identityTestUserReader interface {
	port.UserReader
	lookupCalls() int32
}

func (m *mockUserReader) lookupCalls() int32 { return m.usernameByEmailCalls.Load() }

func (e *errUserReader) lookupCalls() int32 { return e.usernameByEmailCalls.Load() }

func TestUpdateCommitteeMember_IdentityLookup(t *testing.T) {
	tests := []struct {
		name           string
		reader         identityTestUserReader
		skipEnrichment bool
		sync           bool
		wantCalls      int32
		wantStatus     model.IdentityLookupStatus
		wantUsername   string
	}{
		{
			name:         "existing account is resolved once and handed to the orchestrator",
			reader:       newMockUserReader().withUsernames("first.last@example.com", "first-last"),
			sync:         true,
			wantCalls:    1,
			wantStatus:   model.IdentityLookupFound,
			wantUsername: "first-last",
		},
		{
			name:       "missing account is confirmed absent once",
			reader:     newMockUserReader(),
			wantCalls:  1,
			wantStatus: model.IdentityLookupAbsent,
		},
		{
			name:       "lookup failure is handed over unresolved",
			reader:     &errUserReader{},
			wantCalls:  1,
			wantStatus: model.IdentityLookupUnresolved,
		},
		{
			name:           "skip enrichment makes no lookup and keeps the caller username",
			reader:         newMockUserReader().withUsernames("first.last@example.com", "first-last"),
			skipEnrichment: true,
			wantCalls:      0,
			wantUsername:   "first-last-accepted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch := setupServiceTest()
			svc.userReader = tt.reader
			mockOrch.updateMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-identity-lookup",
					CommitteeUID: "committee-1",
					Email:        "first.last@example.com",
				},
			}

			_, err := svc.UpdateCommitteeMember(context.Background(), &committeeservice.UpdateCommitteeMemberPayload{
				UID:            "committee-1",
				MemberUID:      "member-identity-lookup",
				Email:          "First.Last@example.com",
				Username:       stringPtr("first-last-accepted"),
				FirstName:      stringPtr("First"),
				LastName:       stringPtr("Last"),
				IfMatch:        stringPtr("3"),
				XSync:          tt.sync,
				SkipEnrichment: tt.skipEnrichment,
			})
			require.NoError(t, err)

			assert.Equal(t, tt.wantCalls, tt.reader.lookupCalls(), "UsernameByEmail calls")
			require.Len(t, mockOrch.updateMemberCalls, 1)
			call := mockOrch.updateMemberCalls[0]
			assert.Equal(t, uint64(3), call.revision)
			assert.Equal(t, tt.sync, call.sync)
			assert.Equal(t, tt.skipEnrichment, call.skipEnrichment)
			assert.Equal(t, tt.wantUsername, call.member.Username)
			assert.Equal(t, "First", call.member.FirstName)
			assert.Equal(t, "Last", call.member.LastName)
			if tt.skipEnrichment {
				assert.Nil(t, call.member.IdentityLookup)
				return
			}
			require.NotNil(t, call.member.IdentityLookup)
			assert.Equal(t, tt.wantStatus, call.member.IdentityLookup.Status)
			assert.Equal(t, "first.last@example.com", call.member.IdentityLookup.Email)
		})
	}
}

// setupRealWriterUpdateTest wires the API handler to the real committee writer orchestrator over
// the shared mock repository, with the same counting UserReader on both layers, and seeds one
// committee and member that are removed when the test ends.
func setupRealWriterUpdateTest(t *testing.T, reader port.UserReader, storedEmail, storedUsername string) (*committeeServicesrvc, port.CommitteeReader) {
	t.Helper()
	const committeeUID, memberUID = "committee-1391-real-writer", "member-1391-real-writer"
	repo := mock.NewMockRepository()
	repo.AddCommittee(&model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: committeeUID, Name: "Committee 1391", Category: "TC"},
		CommitteeSettings: &model.CommitteeSettings{UID: committeeUID},
	})
	repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID: memberUID, CommitteeUID: committeeUID, Email: storedEmail, Username: storedUsername,
			FirstName: "First", LastName: "Last", AppointedBy: "Community", Status: "Active",
			Organization: model.CommitteeMemberOrganization{Name: "Org"},
		},
	})
	committeeReader := mock.NewMockCommitteeReader(repo)
	t.Cleanup(func() {
		repoWriter := mock.NewMockCommitteeWriter(repo)
		require.NoError(t, repoWriter.DeleteMember(context.Background(), memberUID, 0))
		require.NoError(t, repoWriter.Delete(context.Background(), committeeUID, 0))
	})

	svc, _ := setupServiceTest()
	svc.userReader = reader
	svc.committeeWriterOrchestrator = internalservice.NewCommitteeWriterOrchestrator(
		internalservice.WithCommitteeWriter(mock.NewMockCommitteeWriter(repo)),
		internalservice.WithCommitteeRetriever(committeeReader),
		internalservice.WithProjectRetriever(mock.NewMockProjectRetriever(repo)),
		internalservice.WithCommitteePublisher(mock.NewMockCommitteePublisher()),
		internalservice.WithUserReader(reader),
	)
	return svc, committeeReader
}

// realWriterUpdatePayload builds an update for the member seeded by setupRealWriterUpdateTest.
func realWriterUpdatePayload(email, username, revision string, skipEnrichment bool) *committeeservice.UpdateCommitteeMemberPayload {
	payload := &committeeservice.UpdateCommitteeMemberPayload{
		UID:            "committee-1391-real-writer",
		MemberUID:      "member-1391-real-writer",
		Email:          email,
		FirstName:      stringPtr("First"),
		LastName:       stringPtr("Last"),
		AppointedBy:    "Community",
		Status:         "Active",
		IfMatch:        stringPtr(revision),
		SkipEnrichment: skipEnrichment,
	}
	payload.Organization = &struct {
		ID      *string
		Name    *string
		Website *string
	}{Name: stringPtr("Org")}
	if username != "" {
		payload.Username = stringPtr(username)
	}
	return payload
}

// TestUpdateCommitteeMember_RealWriterIdentityLookup drives the API handler through the real writer
// orchestrator so the account-check count covers the whole edit, not just the handler.
func TestUpdateCommitteeMember_RealWriterIdentityLookup(t *testing.T) {
	const (
		storedEmail  = "first.last@example.com"
		changedEmail = "first.last2@example.com"
	)
	tests := []struct {
		name              string
		reader            identityTestUserReader
		storedUsername    string
		email             string
		submittedUsername string
		skipEnrichment    bool
		wantCalls         int32
		wantUsername      string
	}{
		{
			name:              "existing account with the same email is checked once",
			reader:            newMockUserReader().withUsernames(storedEmail, "first-last"),
			storedUsername:    "first-last",
			email:             storedEmail,
			submittedUsername: "first-last-forged",
			wantCalls:         1,
			wantUsername:      "first-last",
		},
		{
			name:           "existing account with a new verified email is checked once",
			reader:         newMockUserReader().withUsernames(changedEmail, "first-last2"),
			storedUsername: "first-last",
			email:          changedEmail,
			wantCalls:      1,
			wantUsername:   "first-last2",
		},
		{
			name:           "email-only member with no account is checked once and stays email-only",
			reader:         newMockUserReader(),
			storedUsername: "",
			email:          storedEmail,
			wantCalls:      1,
			wantUsername:   "",
		},
		{
			name:           "persistent lookup failure is retried once and keeps the stored username",
			reader:         &errUserReader{},
			storedUsername: "first-last",
			email:          storedEmail,
			wantCalls:      2,
			wantUsername:   "first-last",
		},
		{
			name:           "persistent lookup failure after an email change clears the username",
			reader:         &errUserReader{},
			storedUsername: "first-last",
			email:          changedEmail,
			wantCalls:      2,
			wantUsername:   "",
		},
		{
			name:              "skip enrichment makes no lookup and persists the caller username",
			reader:            newMockUserReader().withUsernames(changedEmail, "first-last2"),
			storedUsername:    "first-last",
			email:             changedEmail,
			submittedUsername: "first-last-accepted",
			skipEnrichment:    true,
			wantCalls:         0,
			wantUsername:      "first-last-accepted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, committeeReader := setupRealWriterUpdateTest(t, tt.reader, storedEmail, tt.storedUsername)

			result, err := svc.UpdateCommitteeMember(context.Background(),
				realWriterUpdatePayload(tt.email, tt.submittedUsername, "1", tt.skipEnrichment))
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.wantCalls, tt.reader.lookupCalls(), "UsernameByEmail calls across handler and writer")
			assert.Equal(t, tt.wantUsername, stringValue(result.Username))
			stored, _, err := committeeReader.GetMember(context.Background(), "member-1391-real-writer")
			require.NoError(t, err)
			assert.Equal(t, tt.wantUsername, stored.Username, "stored username")
			assert.Equal(t, tt.email, stored.Email)
			assert.Equal(t, "First", stored.FirstName)
			assert.Equal(t, "Last", stored.LastName)
			assert.Equal(t, "Community", stored.AppointedBy)
			assert.Equal(t, "Active", stored.Status)
			assert.Equal(t, "Org", stored.Organization.Name)
		})
	}

	t.Run("a later edit never reuses an earlier edit's lookup", func(t *testing.T) {
		reader := newMockUserReader().withUsernames(storedEmail, "first-last", changedEmail, "first-last2")
		svc, committeeReader := setupRealWriterUpdateTest(t, reader, storedEmail, "first-last")

		_, err := svc.UpdateCommitteeMember(context.Background(), realWriterUpdatePayload(storedEmail, "", "1", false))
		require.NoError(t, err)
		assert.Equal(t, int32(1), reader.lookupCalls(), "first edit")
		_, revision, err := committeeReader.GetMember(context.Background(), "member-1391-real-writer")
		require.NoError(t, err)

		result, err := svc.UpdateCommitteeMember(context.Background(),
			realWriterUpdatePayload(changedEmail, "", strconv.FormatUint(revision, 10), false))
		require.NoError(t, err)
		assert.Equal(t, int32(2), reader.lookupCalls(), "second edit makes its own single check")
		assert.Equal(t, "first-last2", stringValue(result.Username))
	})
}

// stringValue dereferences an optional response string, treating nil as empty.
func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
				assert.Equal(t, *result.UID, call.CustomClaims["committee_invite_uid"], "CustomClaims must carry the persisted invite UID so the BFF can accept unambiguously")
				assert.Equal(t, "true", call.CustomClaims["organization_required"], "CustomClaims must carry organization_required so the BFF can prompt for org without an email fetch")
				assert.Equal(t, "Technical Advisory Committee", call.CustomClaims["committee_name"], "CustomClaims must carry committee_name for the org-collection dialog header")
				assert.Contains(t, call.CustomClaims, "organization_name", "organization_name key must be present even when empty")
				assert.Equal(t, "", call.CustomClaims["organization_name"], "CustomClaims organization_name must be empty when no org is pre-filled")
				assert.Contains(t, call.CustomClaims, "organization_id", "organization_id key must be present even when empty")
				assert.Equal(t, "", call.CustomClaims["organization_id"], "CustomClaims organization_id must be empty when no org is pre-filled")
				assert.Contains(t, call.CustomClaims, "organization_website", "organization_website key must be present even when empty")
				assert.Equal(t, "", call.CustomClaims["organization_website"], "CustomClaims organization_website must be empty when no org is pre-filled")
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
		sender := svc.inviteSender.(*mockInviteSender)

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

		// Verify that org fields are embedded in the JWT claims for the BFF to read.
		require.Len(t, sender.calls, 1)
		call := sender.calls[0]
		assert.Equal(t, "true", call.CustomClaims["organization_required"])
		assert.Equal(t, "The Linux Foundation", call.CustomClaims["organization_name"], "pre-filled org name must be embedded in CustomClaims")
		assert.Equal(t, "", call.CustomClaims["organization_id"], "org ID is nil in sampleInviteOrganizationPayload so claim must be empty")
		assert.Equal(t, "https://linuxfoundation.org", call.CustomClaims["organization_website"], "pre-filled org website must be embedded in CustomClaims")
	})

	t.Run("optional on open committee", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		sender := svc.inviteSender.(*mockInviteSender)

		result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
			UID:          "committee-2",
			InviteeEmail: "open@example.com",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Nil(t, result.Organization)

		// committee-2 has BusinessEmailRequired=false and EnableVoting=false, so organization
		// is not required; the claim must reflect that so the BFF accepts without prompting.
		require.Len(t, sender.calls, 1)
		call := sender.calls[0]
		assert.Equal(t, "false", call.CustomClaims["organization_required"], "open committee must emit organization_required=false")
		assert.Equal(t, "Security Committee", call.CustomClaims["committee_name"])
		assert.Contains(t, call.CustomClaims, "organization_name")
		assert.Equal(t, "", call.CustomClaims["organization_name"])
		assert.Contains(t, call.CustomClaims, "organization_id")
		assert.Equal(t, "", call.CustomClaims["organization_id"])
		assert.Contains(t, call.CustomClaims, "organization_website")
		assert.Equal(t, "", call.CustomClaims["organization_website"])
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
	assert.Equal(t, revoked.UID, sender.calls[0].CustomClaims["committee_invite_uid"], "reinstated invite must carry its own UID in CustomClaims")
	assert.Equal(t, "true", sender.calls[0].CustomClaims["organization_required"], "reinstated invite must carry organization_required in CustomClaims")
	assert.Equal(t, "Technical Advisory Committee", sender.calls[0].CustomClaims["committee_name"], "reinstated invite must carry committee_name in CustomClaims")
	assert.Contains(t, sender.calls[0].CustomClaims, "organization_name", "organization_name key must be present even when empty")
	assert.Equal(t, "", sender.calls[0].CustomClaims["organization_name"], "reinstated invite must carry empty organization_name when no org pre-filled")
	assert.Contains(t, sender.calls[0].CustomClaims, "organization_id", "organization_id key must be present even when empty")
	assert.Equal(t, "", sender.calls[0].CustomClaims["organization_id"], "reinstated invite must carry empty organization_id when no org pre-filled")
	assert.Contains(t, sender.calls[0].CustomClaims, "organization_website", "organization_website key must be present even when empty")
	assert.Equal(t, "", sender.calls[0].CustomClaims["organization_website"], "reinstated invite must carry empty organization_website when no org pre-filled")
}

// TestCreateInvite_ReinstateByStatus covers what a new POST does when an invite for the same email
// already exists: revoked, declined, expired pending and accepted-without-a-member invites are
// reinstated to pending in one call, and everything else stays a conflict.
func TestCreateInvite_ReinstateByStatus(t *testing.T) {
	const committeeUID = "committee-1"
	now := time.Now().UTC()
	memberIn := func(committee, email string) *model.CommitteeMember {
		return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
			UID: "member-1", CommitteeUID: committee, Email: email, FirstName: "First", LastName: "Last", Status: "Active",
		}}
	}
	member := func(email string) *model.CommitteeMember { return memberIn(committeeUID, email) }

	tests := []struct {
		name           string
		status         string
		expiresAt      time.Time
		members        []*model.CommitteeMember
		listMembersErr error  // injected failure when the members with the invitee's email are looked up
		storedPadding  string // spaces around the email stored on the existing invite
		wantReinstate  bool
		wantConflict   bool
	}{
		{name: "revoked is reinstated", status: "revoked", wantReinstate: true},
		{name: "declined is reinstated", status: "declined", expiresAt: now.Add(24 * time.Hour), wantReinstate: true},
		{name: "declined with spaces around the stored email is reinstated", status: "declined", storedPadding: "  ", wantReinstate: true},
		{name: "declined and expired is reinstated", status: "declined", expiresAt: now.Add(-24 * time.Hour), wantReinstate: true},
		{name: "pending past its expiry is reinstated", status: "pending", expiresAt: now.Add(-time.Minute), wantReinstate: true},
		{name: "pending before its expiry is a conflict", status: "pending", expiresAt: now.Add(time.Minute), wantConflict: true},
		{name: "pending with no expiry (a legacy record) is never expired", status: "pending", wantConflict: true},
		{name: "accepted with no member left is reinstated", status: "accepted", wantReinstate: true},
		{name: "accepted when another member has a different email is reinstated", status: "accepted", members: []*model.CommitteeMember{member("someone.else@example.com")}, wantReinstate: true},
		{name: "accepted when only another committee has a member with that email is reinstated", status: "accepted", members: []*model.CommitteeMember{memberIn("committee-2", "{email}")}, wantReinstate: true},
		{name: "accepted while the member exists is a conflict", status: "accepted", members: []*model.CommitteeMember{member("{email}")}, wantConflict: true},
		{name: "accepted while the member exists, whatever the spacing of the email, is a conflict", status: "accepted", members: []*model.CommitteeMember{member("  {email}  ")}, wantConflict: true},
		{name: "accepted while the member exists, whatever the case of the email, is a conflict", status: "accepted", members: []*model.CommitteeMember{member("{EMAIL}")}, wantConflict: true},
		{name: "a status that is not known is a conflict", status: "unknown", wantConflict: true},
		{name: "accepted when the members cannot be listed is not reinstated", status: "accepted", listMembersErr: stderrors.New("storage unavailable")},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The mock repository is shared by every test in the package, so each case gets its own invitee,
			// invite and member, and the injected failure is cleared when the case ends.
			svc, _, repo := setupServiceTestWithRepo()
			inviteUID := fmt.Sprintf("reinstate-invite-%d", i)
			email := fmt.Sprintf("reinstate-%d@example.com", i)
			storedEmail := tt.storedPadding + email + tt.storedPadding
			repo.AddCommitteeInvite(&model.CommitteeInvite{
				UID: inviteUID, CommitteeUID: committeeUID, InviteeEmail: storedEmail,
				Status: tt.status, CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: tt.expiresAt, Role: "member",
			})
			for _, m := range tt.members {
				m.UID = fmt.Sprintf("reinstate-member-%d", i)
				m.Email = strings.ReplaceAll(strings.ReplaceAll(m.Email, "{email}", email), "{EMAIL}", strings.ToUpper(email))
				repo.AddCommitteeMember(m.CommitteeUID, m)
			}
			repo.ListMembersByEmailError = tt.listMembersErr
			t.Cleanup(func() { repo.ListMembersByEmailError = nil })

			result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
				// Padded and capitalized: the conflict key trims and lowercases, so the invite must still be found.
				UID: committeeUID, InviteeEmail: "  " + strings.ToUpper(email[:1]) + email[1:] + "  ", Role: stringPtr("chair"),
			})

			sender := svc.inviteSender.(*mockInviteSender)
			stored, _, getErr := repo.GetInvite(context.Background(), inviteUID)
			require.NoError(t, getErr)
			invites, listErr := repo.ListInvites(context.Background(), committeeUID)
			require.NoError(t, listErr)
			forInvitee := 0
			for _, inv := range invites {
				if strings.EqualFold(strings.TrimSpace(inv.InviteeEmail), email) {
					forInvitee++
				}
			}
			assert.Equal(t, 1, forInvitee, "an invite is reinstated in place and never duplicated")

			if tt.wantReinstate {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, inviteUID, *result.UID, "the existing record is reinstated, not a new one")
				assert.Equal(t, "pending", result.Status)
				assert.Equal(t, "pending", stored.Status)
				assert.Equal(t, "chair", stored.Role, "the role of the new invite is applied")
				assert.WithinDuration(t, now.Add(model.InviteDefaultTTL), stored.ExpiresAt, time.Minute, "a fresh 30 day window starts now")
				require.Len(t, sender.calls, 1, "a fresh invite email is sent")
				assert.Equal(t, email, sender.calls[0].Recipient.Email, "the email goes to the address stored on the reinstated invite, without surrounding spaces")
				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.status, stored.Status, "an invite that is not reinstated is left as it was")
			assert.Equal(t, tt.expiresAt, stored.ExpiresAt, "its expiry is left as it was")
			assert.Empty(t, sender.calls, "no email is sent")
			var conflictErr *committeeservice.ConflictError
			if tt.wantConflict {
				require.ErrorAs(t, err, &conflictErr)
			} else {
				assert.False(t, stderrors.As(err, &conflictErr), "a storage failure is not reported as a conflict")
			}
		})
	}
}

// staleInviteListStorage lists a snapshot of the invites, as storage did a moment before another
// request changed one of them, and reads everything else from the wrapped storage.
type staleInviteListStorage struct {
	port.CommitteeReaderWriter
	listed []*model.CommitteeInvite
}

func (s staleInviteListStorage) ListInvites(context.Context, string) ([]*model.CommitteeInvite, error) {
	return s.listed, nil
}

// TestCreateInvite_ReinstateDecidesOnTheStoredInvite covers a race: the invite was declined when it
// was listed, and the invitee accepts it before the reinstate reads it. The reinstate must go by
// the stored invite, which is now accepted with a member, and leave it alone.
func TestCreateInvite_ReinstateDecidesOnTheStoredInvite(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	const committeeUID, email = "committee-1", "stale-snapshot@example.com"

	repo.AddCommitteeInvite(&model.CommitteeInvite{
		UID: "stale-snapshot-invite", CommitteeUID: committeeUID, InviteeEmail: email,
		Status: "accepted", CreatedAt: time.Now().Add(-time.Hour),
	})
	repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "stale-snapshot-member", CommitteeUID: committeeUID, Email: email, Status: "Active",
	}})
	svc.storage = staleInviteListStorage{
		CommitteeReaderWriter: svc.storage,
		listed: []*model.CommitteeInvite{{
			UID: "stale-snapshot-invite", CommitteeUID: committeeUID, InviteeEmail: email, Status: "declined",
		}},
	}

	_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{UID: committeeUID, InviteeEmail: email})

	var conflictErr *committeeservice.ConflictError
	require.ErrorAs(t, err, &conflictErr, "the stored invite is accepted and its member exists")
	stored, _, getErr := repo.GetInvite(context.Background(), "stale-snapshot-invite")
	require.NoError(t, getErr)
	assert.Equal(t, "accepted", stored.Status, "the accepted invite is not overwritten with the stale copy")
	assert.Empty(t, svc.inviteSender.(*mockInviteSender).calls, "no email is sent")
}

// TestCreateInvite_ReinstateWritesTheStoredInvite covers the same race when the stored invite is still
// reinstatable: what is written back is the stored invite, with what the new request changes, and
// nothing taken from the copy that was listed before another request changed it.
func TestCreateInvite_ReinstateWritesTheStoredInvite(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	const committeeUID, email = "committee-1", "stored-copy@example.com"

	repo.AddCommitteeInvite(&model.CommitteeInvite{
		UID: "stored-copy-invite", CommitteeUID: committeeUID, InviteeEmail: email,
		Status: "declined", Role: "stored-role", CreatedAt: time.Now().Add(-time.Hour),
	})
	svc.storage = staleInviteListStorage{
		CommitteeReaderWriter: svc.storage,
		listed: []*model.CommitteeInvite{{
			UID: "stored-copy-invite", CommitteeUID: committeeUID, InviteeEmail: email,
			Status: "declined", Role: "role-from-the-stale-copy",
		}},
	}

	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{UID: committeeUID, InviteeEmail: email})

	require.NoError(t, err)
	assert.Equal(t, "pending", result.Status)
	stored, _, getErr := repo.GetInvite(context.Background(), "stored-copy-invite")
	require.NoError(t, getErr)
	assert.Equal(t, "stored-role", stored.Role, "the role comes from the stored invite, since the request does not change it")
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

// TestCreateInvite_LiveDuplicateRejected covers the invites that cannot be re-invited: one that is
// still pending, whether or not it has an expiry, and an accepted one whose member is still in the
// committee. (Declined, expired and member-less accepted invites are reinstated; see
// TestCreateInvite_ReinstateByStatus.)
func TestCreateInvite_LiveDuplicateRejected(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		expiresAt    time.Time
		memberExists bool
	}{
		{name: "pending without an expiry", status: "pending"},
		{name: "pending before its expiry", status: "pending", expiresAt: time.Now().Add(time.Hour)},
		{name: "accepted with its member in the committee", status: "accepted", memberExists: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, repo := setupServiceTestWithRepo()
			email := fmt.Sprintf("dup-%d@example.com", i)

			repo.AddCommitteeInvite(&model.CommitteeInvite{
				UID: fmt.Sprintf("dup-invite-%d", i), CommitteeUID: "committee-1", InviteeEmail: email,
				Status: tt.status, CreatedAt: time.Now(), ExpiresAt: tt.expiresAt,
			})
			if tt.memberExists {
				repo.AddCommitteeMember("committee-1", &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
					UID: fmt.Sprintf("dup-member-%d", i), CommitteeUID: "committee-1", Email: email, Status: "Active",
				}})
			}

			_, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
				UID:          "committee-1",
				InviteeEmail: email,
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
		name                     string
		seedStatus               string
		seedMember               *model.CommitteeMember // pre-existing member to seed (for idempotent accepted case)
		principal                string
		inviteeEmail             string    // overrides principal for invite InviteeEmail; defaults to principal when empty
		expiresAt                time.Time // invite expiry; zero means unset (legacy record)
		expectError              bool
		expectResult             bool // false means nil result is acceptable
		expectCreateMemberCalled bool // whether AcceptInvite reaches the CreateMember call at all
	}{
		{
			name:                     "successful accept of pending invite",
			seedStatus:               "pending",
			principal:                "accept@example.com",
			expectError:              false,
			expectResult:             true,
			expectCreateMemberCalled: true,
		},
		{
			name:                     "successful accept of previously declined invite",
			seedStatus:               "declined",
			principal:                "accept@example.com",
			expectError:              false,
			expectResult:             true,
			expectCreateMemberCalled: true,
		},
		{
			name:       "already accepted invite returns success (idempotent) — member found",
			seedStatus: "accepted",
			seedMember: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "existing-member-uid",
					CommitteeUID: "committee-1",
					Email:        "accept@example.com",
					Status:       "Active",
				},
			},
			principal:    "accept@example.com",
			expectError:  false,
			expectResult: true,
		},
		{
			// Use a distinct email to avoid matching the member seeded by the previous sub-test
			// (the global mock repo is a singleton, so state leaks between subtests).
			name:         "already accepted invite with no member record returns conflict",
			seedStatus:   "accepted",
			principal:    "no-member-accept@example.com",
			inviteeEmail: "no-member-accept@example.com",
			expectError:  true,
		},
		{
			name:        "cannot accept revoked invite",
			seedStatus:  "revoked",
			principal:   "accept@example.com",
			expectError: true,
		},
		{
			name:                     "accepts pending invite with a future expiry",
			seedStatus:               "pending",
			principal:                "future-expiry@example.com",
			inviteeEmail:             "future-expiry@example.com",
			expiresAt:                time.Now().Add(24 * time.Hour),
			expectError:              false,
			expectResult:             true,
			expectCreateMemberCalled: true,
		},
		{
			name:         "cannot accept an expired pending invite",
			seedStatus:   "pending",
			principal:    "expired@example.com",
			inviteeEmail: "expired@example.com",
			expiresAt:    time.Now().Add(-time.Hour),
			expectError:  true,
		},
		{
			// Expiry is checked after the accepted-status short-circuit, so an already-accepted
			// invite still returns its member idempotently even when past expiry.
			name:       "already accepted invite past expiry is still idempotent",
			seedStatus: "accepted",
			seedMember: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "existing-member-expired-uid",
					CommitteeUID: "committee-1",
					Email:        "idempotent-expired@example.com",
					Status:       "Active",
				},
			},
			principal:    "idempotent-expired@example.com",
			inviteeEmail: "idempotent-expired@example.com",
			expiresAt:    time.Now().Add(-time.Hour),
			expectError:  false,
			expectResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()
			svc.userReader = mockReaderForPrincipalEmail(tt.principal, tt.principal)

			inviteeEmail := tt.inviteeEmail
			if inviteeEmail == "" {
				inviteeEmail = tt.principal
			}
			invite := &model.CommitteeInvite{
				UID:          "invite-accept-test",
				CommitteeUID: "committee-1",
				InviteeEmail: inviteeEmail,
				Status:       tt.seedStatus,
				CreatedAt:    time.Now(),
				ExpiresAt:    tt.expiresAt,
			}
			repo.AddCommitteeInvite(invite)

			if tt.seedMember != nil {
				repo.AddCommitteeMember(tt.seedMember.CommitteeUID, tt.seedMember)
			}

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
				if tt.expectResult {
					require.NotNil(t, result)
					assert.Equal(t, "Active", result.Status)
				}
			}

			// AcceptInvite must always pass sync=false to CreateMember (LFXV2-2856): membership
			// FGA publishing is asynchronous-only, and a regression to sync=true would reintroduce
			// the request/reply indexer timeout this change removed. Idempotent/error paths
			// short-circuit before reaching CreateMember, so assert the call happens exactly when
			// expected rather than only inspecting the last recorded arg.
			if tt.expectCreateMemberCalled {
				require.Len(t, mockOrch.createMemberSyncArgs, 1)
				assert.False(t, mockOrch.createMemberSyncArgs[0], "AcceptInvite must call CreateMember with sync=false")
			} else {
				assert.Empty(t, mockOrch.createMemberSyncArgs, "AcceptInvite must not call CreateMember on this path")
			}
		})
	}

	t.Run("identity lookup is resolved once and handed to the orchestrator", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		reader := mockReaderForPrincipalEmail("first-last", "first.last@example.com").
			withUsernames("first.last@example.com", "first-last")
		svc.userReader = reader
		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-identity-lookup",
			CommitteeUID: "committee-1",
			InviteeEmail: "first.last@example.com",
			Status:       "pending",
			CreatedAt:    time.Now(),
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-identity-lookup",
				CommitteeUID: "committee-1",
				Email:        "first.last@example.com",
				Status:       "Active",
			},
		}

		_, err := svc.AcceptInvite(testCtx("first-last"), &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-identity-lookup",
		})
		require.NoError(t, err)

		assert.Equal(t, int32(1), reader.usernameByEmailCalls.Load(), "UsernameByEmail calls")
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		require.NotNil(t, member.IdentityLookup)
		assert.True(t, member.IdentityLookup.ResolvedFor(member.Email))
		assert.Equal(t, model.IdentityLookupFound, member.IdentityLookup.Status)
		assert.Equal(t, "first-last", member.Username)
	})
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

	t.Run("uses payload name and website without organization ID", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-org-name-website",
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

		payloadName := "Payload Org"
		payloadWebsite := "https://payload.org"
		_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-org-name-website",
			Body: &committeeservice.AcceptInviteOptionalBody{
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					Name:    &payloadName,
					Website: &payloadWebsite,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		assert.Equal(t, "Payload Org", member.Organization.Name)
		assert.Equal(t, "https://payload.org", member.Organization.Website)
		assert.Empty(t, member.Organization.ID)
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

	t.Run("identity lookup is resolved once and handed to the orchestrator", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		reader := newMockUserReader()
		svc.userReader = reader
		application := &model.CommitteeApplication{
			UID:            "app-identity-lookup",
			CommitteeUID:   "committee-1",
			ApplicantEmail: "first.last@example.com",
			Status:         "pending",
			CreatedAt:      time.Now(),
		}
		repo.AddCommitteeApplication(application)
		t.Cleanup(func() {
			require.NoError(t, svc.storage.DeleteApplication(context.Background(), application))
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				CommitteeUID: "committee-1",
				Email:        "first.last@example.com",
				Status:       "Active",
			},
		}

		_, err := svc.ApproveApplication(context.Background(), &committeeservice.ApproveApplicationPayload{
			UID:            "committee-1",
			ApplicationUID: "app-identity-lookup",
		})
		require.NoError(t, err)

		assert.Equal(t, int32(1), reader.usernameByEmailCalls.Load(), "UsernameByEmail calls")
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		require.NotNil(t, member.IdentityLookup)
		assert.True(t, member.IdentityLookup.ResolvedFor(member.Email))
		assert.Equal(t, model.IdentityLookupAbsent, member.IdentityLookup.Status)
		assert.Empty(t, member.Username)
	})
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

func TestSubmitApplication_WithOrganization(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	repo.SetJoinMode("committee-1", "application")
	svc.userReader = mockReaderForPrincipalEmail("applicant-with-org@example.com", "applicant-with-org@example.com")

	orgID := "0012400001ABCDEF"
	orgName := "Acme Corp"
	orgWebsite := "https://acme.com"
	msg := "I'd like to join"

	ctx := testCtx("applicant-with-org@example.com")
	result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
		UID:     "committee-1",
		Message: &msg,
		Organization: &struct {
			ID      *string
			Name    *string
			Website *string
		}{
			ID:      &orgID,
			Name:    &orgName,
			Website: &orgWebsite,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	stored, _, err := repo.GetApplication(ctx, *result.UID)
	require.NoError(t, err)
	require.NotNil(t, stored.Organization, "organization should be stored on the application record")
	assert.Equal(t, orgID, stored.Organization.ID)
	assert.Equal(t, orgName, stored.Organization.Name)
	assert.Equal(t, orgWebsite, stored.Organization.Website)
}

func TestSubmitApplication_WithoutOrganization(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	repo.SetJoinMode("committee-1", "application")
	svc.userReader = mockReaderForPrincipalEmail("applicant-no-org@example.com", "applicant-no-org@example.com")

	ctx := testCtx("applicant-no-org@example.com")
	result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
		UID: "committee-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	stored, _, err := repo.GetApplication(ctx, *result.UID)
	require.NoError(t, err)
	assert.Nil(t, stored.Organization, "organization should be nil when not provided")
}

func TestSubmitApplication_UsernameStoredOnApplication(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	repo.SetJoinMode("committee-1", "application")
	svc.userReader = mockReaderForPrincipalEmail("first-last", "first.last@example.com").
		withUsernames("first.last@example.com", "first-last")

	ctx := testCtx("first-last")
	result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
		UID: "committee-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	stored, _, err := repo.GetApplication(ctx, *result.UID)
	require.NoError(t, err)
	assert.Equal(t, "first-last", stored.ApplicantUsername, "canonical LFID username should be stored")
}

func TestSubmitApplication_RejectedApp_UsernameUpdatedOnReinstatement(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	repo.SetJoinMode("committee-1", "application")

	// Seed with a stale username to prove the reinstatement path overwrites it.
	rejected := &model.CommitteeApplication{
		UID:               "rejected-app-stale-username",
		CommitteeUID:      "committee-1",
		ApplicantEmail:    "first.last@example.com",
		ApplicantUsername: "old-username",
		Status:            "rejected",
		CreatedAt:         time.Now(),
	}
	repo.AddCommitteeApplication(rejected)

	svc.userReader = mockReaderForPrincipalEmail("first-last", "first.last@example.com").
		withUsernames("first.last@example.com", "first-last")
	ctx := testCtx("first-last")
	result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
		UID: "committee-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, rejected.UID, *result.UID, "should reinstate the existing application")

	stored, _, err := repo.GetApplication(ctx, rejected.UID)
	require.NoError(t, err)
	assert.Equal(t, "first-last", stored.ApplicantUsername, "stale username should be overwritten on reinstatement")
}

func TestSubmitApplication_RejectedApp_OrgReplaced(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	repo.SetJoinMode("committee-1", "application")

	originalOrg := &model.CommitteeMemberOrganization{ID: "old-sfid", Name: "OldCorp", Website: "https://old.com"}
	rejected := &model.CommitteeApplication{
		UID:            "rejected-app-with-org",
		CommitteeUID:   "committee-1",
		ApplicantEmail: "reapplicant-org-replace@example.com",
		Status:         "rejected",
		Organization:   originalOrg,
		CreatedAt:      time.Now(),
	}
	repo.AddCommitteeApplication(rejected)

	svc.userReader = mockReaderForPrincipalEmail("reapplicant-org-replace@example.com", "reapplicant-org-replace@example.com")
	newOrgID := "0012400001NEWID0"
	newOrgName := "NewCorp"
	newOrgWebsite := "https://newcorp.com"
	newMsg := "I've improved since last time"
	ctx := testCtx("reapplicant-org-replace@example.com")

	result, err := svc.SubmitApplication(ctx, &committeeservice.SubmitApplicationPayload{
		UID:     "committee-1",
		Message: &newMsg,
		Organization: &struct {
			ID      *string
			Name    *string
			Website *string
		}{
			ID:      &newOrgID,
			Name:    &newOrgName,
			Website: &newOrgWebsite,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, rejected.UID, *result.UID, "should reinstate the existing application")
	assert.Equal(t, "pending", result.Status)

	stored, _, err := repo.GetApplication(ctx, rejected.UID)
	require.NoError(t, err)
	require.NotNil(t, stored.Organization, "organization should be replaced on reinstatement")
	assert.Equal(t, newOrgID, stored.Organization.ID)
	assert.Equal(t, newOrgName, stored.Organization.Name)
	assert.Equal(t, newOrgWebsite, stored.Organization.Website)
}

func TestApproveApplication_OrganizationPropagatedToMember(t *testing.T) {
	tests := []struct {
		name        string
		appUID      string
		appEmail    string
		appOrg      *model.CommitteeMemberOrganization
		wantOrgID   string
		wantOrgName string
	}{
		{
			name:        "organization from application is seeded onto created member",
			appUID:      "app-org-propagation-with-org",
			appEmail:    "org-propagation-with@example.com",
			appOrg:      &model.CommitteeMemberOrganization{ID: "0012400001SFIDXX", Name: "Acme Corp", Website: "https://acme.com"},
			wantOrgID:   "0012400001SFIDXX",
			wantOrgName: "Acme Corp",
		},
		{
			name:        "no organization on application leaves member organization empty",
			appUID:      "app-org-propagation-no-org",
			appEmail:    "org-propagation-without@example.com",
			appOrg:      nil,
			wantOrgID:   "",
			wantOrgName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch, repo := setupServiceTestWithRepo()

			app := &model.CommitteeApplication{
				UID:            tt.appUID,
				CommitteeUID:   "committee-1",
				ApplicantEmail: tt.appEmail,
				Status:         "pending",
				Organization:   tt.appOrg,
				CreatedAt:      time.Now(),
			}
			repo.AddCommitteeApplication(app)

			mockOrch.createMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-1",
					Email:        tt.appEmail,
					Status:       "Active",
				},
			}

			notes := "Welcome aboard"
			_, err := svc.ApproveApplication(context.Background(), &committeeservice.ApproveApplicationPayload{
				UID:            "committee-1",
				ApplicationUID: tt.appUID,
				ReviewerNotes:  &notes,
			})

			require.NoError(t, err)
			require.Len(t, mockOrch.createMemberCalls, 1)
			createdMember := mockOrch.createMemberCalls[0]
			assert.Equal(t, tt.wantOrgID, createdMember.Organization.ID)
			assert.Equal(t, tt.wantOrgName, createdMember.Organization.Name)
		})
	}
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

	t.Run("identity lookup is resolved once and handed to the orchestrator", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		reader := mockReaderForPrincipalEmail("first-last", "first.last@example.com").
			withUsernames("first.last@example.com", "first-last")
		svc.userReader = reader
		repo.SetJoinMode("committee-1", "open")
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-identity-lookup",
				CommitteeUID: "committee-1",
				Email:        "first.last@example.com",
				Status:       "Active",
			},
		}

		_, err := svc.JoinCommittee(testCtx("first-last"), &committeeservice.JoinCommitteePayload{UID: "committee-1"})
		require.NoError(t, err)

		assert.Equal(t, int32(1), reader.usernameByEmailCalls.Load(), "UsernameByEmail calls")
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		require.NotNil(t, member.IdentityLookup)
		assert.True(t, member.IdentityLookup.ResolvedFor(member.Email))
		assert.Equal(t, model.IdentityLookupFound, member.IdentityLookup.Status)
		assert.Equal(t, "first-last", member.Username)
	})
}

func TestJoinCommittee_Organization(t *testing.T) {
	// domainMemberValidator wires the real domain membership gate into the mock
	// orchestrator so these subtests exercise organization validation exactly as the
	// production orchestrator applies it in CreateMember (Step 2b).
	domainMemberValidator := func(t *testing.T, repo *mock.MockRepository) func(*model.CommitteeMember) error {
		return func(member *model.CommitteeMember) error {
			base, _, err := repo.GetBase(context.Background(), member.CommitteeUID)
			require.NoError(t, err)
			return member.Validate(&model.Committee{
				CommitteeBase:     *base,
				CommitteeSettings: repo.GetSettingsPtr(member.CommitteeUID),
			})
		}
	}

	t.Run("payload organization stored on voting-enabled committee join", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("first-last", "first.last@example.com")

		// committee-1 is gated: enable_voting=true and business_email_required=true.
		repo.SetJoinMode("committee-1", "open")
		mockOrch.createMemberValidator = domainMemberValidator(t, repo)
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "new-member-uid",
				CommitteeUID: "committee-1",
				Email:        "first.last@example.com",
				Status:       "Active",
			},
		}

		payloadName := "Payload Org"
		payloadWebsite := "https://payload.org"
		result, err := svc.JoinCommittee(testCtx("first-last"), &committeeservice.JoinCommitteePayload{
			UID: "committee-1",
			Body: &committeeservice.JoinCommitteeOptionalBody{
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					Name:    &payloadName,
					Website: &payloadWebsite,
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		assert.Equal(t, "Payload Org", member.Organization.Name)
		assert.Equal(t, "https://payload.org", member.Organization.Website)
		assert.Empty(t, member.Organization.ID)
	})

	t.Run("no body on voting-enabled committee still rejected", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("first-last", "first.last@example.com")

		repo.SetJoinMode("committee-1", "open")
		mockOrch.createMemberValidator = domainMemberValidator(t, repo)

		result, err := svc.JoinCommittee(testCtx("first-last"), &committeeservice.JoinCommitteePayload{
			UID: "committee-1",
		})
		require.Error(t, err)
		assert.Nil(t, result)
		var badReq *committeeservice.BadRequestError
		assert.ErrorAs(t, err, &badReq, "expected a 400 bad-request error")
		assert.Contains(t, badReq.Message, "organization id or organization name and domain")
	})

	t.Run("no body on non-gated open committee succeeds", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("first-last", "first.last@example.com")

		// committee-2 is non-gated: enable_voting=false and business_email_required=false.
		repo.SetJoinMode("committee-2", "open")
		mockOrch.createMemberValidator = domainMemberValidator(t, repo)
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "new-member-uid",
				CommitteeUID: "committee-2",
				Email:        "first.last@example.com",
				Status:       "Active",
			},
		}

		result, err := svc.JoinCommittee(testCtx("first-last"), &committeeservice.JoinCommitteePayload{
			UID: "committee-2",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, mockOrch.createMemberCalls, 1)
		member := mockOrch.createMemberCalls[0]
		assert.Empty(t, member.Organization.ID)
		assert.Empty(t, member.Organization.Name)
		assert.Empty(t, member.Organization.Website)
	})

	t.Run("already a member returns conflict", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail("first-last", "first.last@example.com")

		repo.SetJoinMode("committee-2", "open")
		mockOrch.createMemberErr = errs.NewConflict("requester is already a member of this committee")

		result, err := svc.JoinCommittee(testCtx("first-last"), &committeeservice.JoinCommitteePayload{
			UID: "committee-2",
		})
		require.Error(t, err)
		assert.Nil(t, result)
		var conflictErr *committeeservice.ConflictError
		assert.ErrorAs(t, err, &conflictErr, "expected a 409 conflict error")
	})
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
				assert.Equal(t, "committee-1", mockOrch.deleteCalls[0].committeeUID)
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
type errUserReader struct {
	usernameByEmailCalls atomic.Int32 // number of UsernameByEmail calls
}

func (e *errUserReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	e.usernameByEmailCalls.Add(1)
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

func TestCreateCommitteeMember_IdentityLookup(t *testing.T) {
	tests := []struct {
		name           string
		usernames      []string
		skipEnrichment bool
		wantCalls      int32
		wantStatus     model.IdentityLookupStatus
		wantUsername   string
	}{
		{
			name:         "existing account is resolved once and handed to the orchestrator",
			usernames:    []string{"first.last@example.com", "first-last"},
			wantCalls:    1,
			wantStatus:   model.IdentityLookupFound,
			wantUsername: "first-last",
		},
		{
			name:       "missing account is confirmed absent once",
			wantCalls:  1,
			wantStatus: model.IdentityLookupAbsent,
		},
		{
			name:           "skip enrichment makes no lookup and records none",
			usernames:      []string{"first.last@example.com", "first-last"},
			skipEnrichment: true,
			wantCalls:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockOrch := setupServiceTest()
			reader := newMockUserReader().withUsernames(tt.usernames...)
			svc.userReader = reader
			mockOrch.createMember = &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-identity-lookup",
					CommitteeUID: "committee-1",
					Email:        "first.last@example.com",
				},
			}
			firstName, lastName := "First", "Last"

			_, err := svc.CreateCommitteeMember(context.Background(), &committeeservice.CreateCommitteeMemberPayload{
				UID:            "committee-1",
				Email:          "first.last@example.com",
				FirstName:      &firstName,
				LastName:       &lastName,
				SkipEnrichment: tt.skipEnrichment,
			})
			require.NoError(t, err)

			assert.Equal(t, tt.wantCalls, reader.usernameByEmailCalls.Load(), "UsernameByEmail calls")
			require.Len(t, mockOrch.createMemberCalls, 1)
			member := mockOrch.createMemberCalls[0]
			if tt.skipEnrichment {
				assert.Nil(t, member.IdentityLookup)
				return
			}
			require.NotNil(t, member.IdentityLookup)
			assert.True(t, member.IdentityLookup.ResolvedFor(member.Email))
			assert.Equal(t, tt.wantStatus, member.IdentityLookup.Status)
			assert.Equal(t, tt.wantUsername, member.Username)
		})
	}
}

func TestBulkCreateCommitteeMembers(t *testing.T) {
	username, firstName, lastName, orgName, website := "first-last", "First", "Last", "Example", "https://example.com"
	fullRow := &committeeservice.CommitteeMemberBulkCreateRow{
		Username:    &username,
		Email:       "first.last+1@example.com",
		FirstName:   &firstName,
		LastName:    &lastName,
		AppointedBy: "Community",
		Status:      "Active",
		Role: &struct {
			Name      string
			StartDate *string
			EndDate   *string
		}{Name: "Chair"},
		Voting: &struct {
			Status    string
			StartDate *string
			EndDate   *string
		}{Status: "Voting Rep"},
		Organization: &struct {
			ID      *string
			Name    *string
			Website *string
		}{Name: &orgName, Website: &website},
	}
	twoRows := func() []*committeeservice.CommitteeMemberBulkCreateRow {
		second := *fullRow
		second.Email = "first.last+2@example.com"
		first := *fullRow
		return []*committeeservice.CommitteeMemberBulkCreateRow{&first, &second}
	}
	added := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: "member-added", CommitteeUID: "committee-1", Email: "first.last+1@example.com",
	}}

	tests := []struct {
		name    string
		payload *committeeservice.BulkCreateCommitteeMembersPayload
		setup   func(*mockCommitteeWriterOrchestrator)
		check   func(t *testing.T, svc *committeeServicesrvc, orch *mockCommitteeWriterOrchestrator, reader *mockUserReader, res *committeeservice.CommitteeMemberBulkCreateResult, err error)
	}{
		{
			name:    "converts each row exactly like a single create and enriches every row",
			payload: &committeeservice.BulkCreateCommitteeMembersPayload{UID: "committee-1", XSync: true, SkipNotification: true, Members: twoRows()},
			setup: func(orch *mockCommitteeWriterOrchestrator) {
				orch.createMembersResults = []*model.BulkMemberResult{}
			},
			check: func(t *testing.T, svc *committeeServicesrvc, orch *mockCommitteeWriterOrchestrator, reader *mockUserReader, _ *committeeservice.CommitteeMemberBulkCreateResult, err error) {
				require.NoError(t, err)
				require.Len(t, orch.createMembersCalls, 1)
				call := orch.createMembersCalls[0]
				assert.Equal(t, "committee-1", call.committeeUID)
				assert.True(t, call.opts.Sync)
				assert.False(t, call.opts.SkipEnrichment)
				require.NotNil(t, call.opts.Enrich)
				assert.EqualValues(t, 2, reader.usernameByEmailCalls.Load(), "one identity lookup per row")

				single := svc.convertMemberPayloadToDomain(&committeeservice.CreateCommitteeMemberPayload{
					UID: "committee-1", SkipNotification: true, Username: fullRow.Username, Email: fullRow.Email,
					FirstName: fullRow.FirstName, LastName: fullRow.LastName, AppointedBy: fullRow.AppointedBy,
					Status: fullRow.Status, Role: fullRow.Role, Voting: fullRow.Voting, Organization: fullRow.Organization,
				})
				assert.Equal(t, single, svc.convertBulkMemberRowToDomain("committee-1", true, fullRow))
			},
		},
		{
			name:    "skip enrichment makes no identity lookup",
			payload: &committeeservice.BulkCreateCommitteeMembersPayload{UID: "committee-1", SkipEnrichment: true, Members: twoRows()},
			check: func(t *testing.T, _ *committeeServicesrvc, orch *mockCommitteeWriterOrchestrator, reader *mockUserReader, _ *committeeservice.CommitteeMemberBulkCreateResult, err error) {
				require.NoError(t, err)
				require.Len(t, orch.createMembersCalls, 1)
				assert.True(t, orch.createMembersCalls[0].opts.SkipEnrichment)
				assert.Nil(t, orch.createMembersCalls[0].opts.Enrich)
				assert.Zero(t, reader.usernameByEmailCalls.Load())
				assert.Equal(t, "first-last", orch.createMembersCalls[0].members[0].Username, "the submitted username is kept")
			},
		},
		{
			name:    "maps row outcomes and counts, with the member only on added rows",
			payload: &committeeservice.BulkCreateCommitteeMembersPayload{UID: "committee-1", SkipEnrichment: true, Members: append(twoRows(), fullRow)},
			setup: func(orch *mockCommitteeWriterOrchestrator) {
				orch.createMembersResults = []*model.BulkMemberResult{
					{Index: 0, Email: added.Email, Status: model.BulkMemberAdded, MemberUID: added.UID, Member: added},
					{Index: 1, Email: "first.last+2@example.com", Status: model.BulkMemberSkipped, Reason: model.BulkReasonAlreadyMember, MemberUID: "member-existing", Message: "member already exists in the committee"},
					{Index: 2, Email: added.Email, Status: model.BulkMemberFailed, Reason: model.BulkReasonTemporary, Retryable: true, Message: "temporary failure; retry later"},
				}
			},
			check: func(t *testing.T, _ *committeeServicesrvc, _ *mockCommitteeWriterOrchestrator, _ *mockUserReader, res *committeeservice.CommitteeMemberBulkCreateResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, 1, res.Added)
				assert.Equal(t, 1, res.Skipped)
				assert.Equal(t, 1, res.Failed)
				require.Len(t, res.Results, 3)

				assert.Equal(t, "added", res.Results[0].Status)
				assert.Nil(t, res.Results[0].Reason)
				require.NotNil(t, res.Results[0].Member)
				assert.Equal(t, "member-added", *res.Results[0].Member.UID)

				assert.Equal(t, "skipped", res.Results[1].Status)
				assert.Equal(t, "already_member", *res.Results[1].Reason)
				assert.Equal(t, "member-existing", *res.Results[1].MemberUID)
				assert.Nil(t, res.Results[1].Member)

				assert.Equal(t, 2, res.Results[2].Index)
				assert.Equal(t, "temporary", *res.Results[2].Reason)
				assert.True(t, res.Results[2].Retryable)
				assert.Nil(t, res.Results[2].Member)
			},
		},
		{
			name:    "maps a missing committee to not found",
			payload: &committeeservice.BulkCreateCommitteeMembersPayload{UID: "committee-1", Members: twoRows()},
			setup: func(orch *mockCommitteeWriterOrchestrator) {
				orch.createMembersErr = errs.NewNotFound("committee not found")
			},
			check: func(t *testing.T, _ *committeeServicesrvc, _ *mockCommitteeWriterOrchestrator, _ *mockUserReader, res *committeeservice.CommitteeMemberBulkCreateResult, err error) {
				var notFound *committeeservice.NotFoundError
				require.ErrorAs(t, err, &notFound)
				assert.Nil(t, res)
			},
		},
		{
			name: "rejects a nil payload",
			check: func(t *testing.T, _ *committeeServicesrvc, orch *mockCommitteeWriterOrchestrator, _ *mockUserReader, res *committeeservice.CommitteeMemberBulkCreateResult, err error) {
				var badRequest *committeeservice.BadRequestError
				require.ErrorAs(t, err, &badRequest)
				assert.Nil(t, res)
				assert.Empty(t, orch.createMembersCalls)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, orch := setupServiceTest()
			reader := newMockUserReader()
			svc.userReader = reader
			if tt.setup != nil {
				tt.setup(orch)
			}

			res, err := svc.BulkCreateCommitteeMembers(context.Background(), tt.payload)

			tt.check(t, svc, orch, reader, res, err)
		})
	}
}

func TestEnrichMember(t *testing.T) {
	tests := []struct {
		name        string
		member      func() *model.CommitteeMember
		setupReader func(r *mockUserReader)
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
			svc.enrichMember(context.Background(), m)
			tt.validate(t, m)
		})
	}

	identityTests := []struct {
		name         string
		reader       func() (port.UserReader, func() int32)
		wantStatus   model.IdentityLookupStatus
		wantUsername string
	}{
		{
			name: "found account records a found lookup",
			reader: func() (port.UserReader, func() int32) {
				r := newMockUserReader().withUsernames("first.last@example.com", "first-last")
				return r, r.usernameByEmailCalls.Load
			},
			wantStatus:   model.IdentityLookupFound,
			wantUsername: "first-last",
		},
		{
			name: "NotFound reply records a confirmed absence",
			reader: func() (port.UserReader, func() int32) {
				r := newMockUserReader()
				return r, r.usernameByEmailCalls.Load
			},
			wantStatus: model.IdentityLookupAbsent,
		},
		{
			name: "transport error records an unresolved lookup",
			reader: func() (port.UserReader, func() int32) {
				r := &errUserReader{}
				return r, r.usernameByEmailCalls.Load
			},
			wantStatus: model.IdentityLookupUnresolved,
		},
	}
	for _, tt := range identityTests {
		t.Run("identity lookup — "+tt.name, func(t *testing.T) {
			svc, _ := setupServiceTest()
			reader, calls := tt.reader()
			svc.userReader = reader
			m := &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{Email: " First.Last@example.com "},
			}

			svc.enrichMember(context.Background(), m)

			assert.Equal(t, int32(1), calls(), "UsernameByEmail calls")
			require.NotNil(t, m.IdentityLookup)
			assert.Equal(t, tt.wantStatus, m.IdentityLookup.Status)
			assert.Equal(t, tt.wantUsername, m.IdentityLookup.Username)
			assert.Equal(t, tt.wantStatus != model.IdentityLookupUnresolved, m.IdentityLookup.ResolvedFor(m.Email))
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
	reader := &metadataByKeyReader{
		metadata: map[string]*model.UserMetadata{
			principal: {Organization: "Example Corp"},
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

func TestSafeClaimValue(t *testing.T) {
	ctx := context.Background()

	t.Run("short value passes through unchanged", func(t *testing.T) {
		s := "hello"
		assert.Equal(t, s, safeClaimValue(ctx, s, "test_claim", "invite-1"))
	})

	t.Run("empty string passes through", func(t *testing.T) {
		assert.Equal(t, "", safeClaimValue(ctx, "", "test_claim", "invite-1"))
	})

	t.Run("exactly 1024 bytes passes through", func(t *testing.T) {
		s := string(make([]byte, 1024))
		result := safeClaimValue(ctx, s, "test_claim", "invite-1")
		assert.Equal(t, s, result)
	})

	t.Run("1025 bytes returns empty string", func(t *testing.T) {
		s := string(make([]byte, 1025))
		result := safeClaimValue(ctx, s, "test_claim", "invite-1")
		assert.Equal(t, "", result, "oversized value must be omitted, not truncated")
	})

	t.Run("multibyte rune crossing byte 1024 returns empty string", func(t *testing.T) {
		// Build a string that is exactly 1023 ASCII bytes followed by a 3-byte UTF-8 rune
		// (U+4E2D '中'). Total length is 1026 bytes — over limit.
		prefix := string(make([]byte, 1023))
		s := prefix + "中" // "中" is 3 UTF-8 bytes
		assert.Greater(t, len(s), 1024)
		result := safeClaimValue(ctx, s, "test_claim", "invite-1")
		assert.Equal(t, "", result, "value with multibyte rune crossing the limit must be omitted")
	})
}

// testCtxWithEmail builds a context with both principal and email context values,
// simulating what JWTAuth populates after parsing a Heimdall JWT with an email claim.
func testCtxWithEmail(principal, email string) context.Context {
	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, principal)
	return context.WithValue(ctx, constants.EmailContextID, email)
}

// withEmailsErr configures the error returned by EmailsByAuthToken for all calls and returns the receiver for chaining.
func (m *mockUserReader) withEmailsErr(err error) *mockUserReader {
	m.emailsErr = err
	return m
}

// mockUserReaderNotFound returns a mockUserReader that returns a NotFound error for all
// EmailsByAuthToken calls, simulating an auth-service propagation delay for new LFID users.
func mockUserReaderNotFound() *mockUserReader {
	return newMockUserReader().withEmailsErr(errs.NewNotFound("the user does not exist"))
}

// TestAcceptInvite_NewUserEmailFallback tests the propagation-race fallback: when
// auth-service returns NOT_FOUND for a new user, resolveCallerEmail uses the email
// claim stored in the request context from the Heimdall JWT.
func TestAcceptInvite_NewUserEmailFallback(t *testing.T) {
	const inviteeEmail = "newuser@example.com"
	const principal = "newuser"

	t.Run("NOT_FOUND + JWT email matches invitee — accept succeeds", func(t *testing.T) {
		svc, mockOrch, repo := setupServiceTestWithRepo()
		svc.userReader = mockUserReaderNotFound()

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-new-user",
			CommitteeUID: "committee-1",
			InviteeEmail: inviteeEmail,
			Status:       "pending",
			CreatedAt:    time.Now(),
		})
		mockOrch.createMember = &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "new-member-uid",
				CommitteeUID: "committee-1",
				Email:        inviteeEmail,
				Status:       "Active",
			},
		}

		ctx := testCtxWithEmail(principal, inviteeEmail)
		result, err := svc.AcceptInvite(ctx, &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-new-user",
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "Active", result.Status)
	})

	t.Run("NOT_FOUND + no JWT email in context — returns error", func(t *testing.T) {
		svc, _, repo := setupServiceTestWithRepo()
		svc.userReader = mockUserReaderNotFound()

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-no-email",
			CommitteeUID: "committee-1",
			InviteeEmail: inviteeEmail,
			Status:       "pending",
			CreatedAt:    time.Now(),
		})

		// Context has principal but no email — simulates M2M or old-format token
		ctx := testCtx(principal)
		result, err := svc.AcceptInvite(ctx, &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-no-email",
		})

		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("non-NOT_FOUND error + JWT email present — returns original error (no fallback)", func(t *testing.T) {
		svc, _, repo := setupServiceTestWithRepo()
		unexpectedErr := errs.NewServiceUnavailable("NATS timeout")
		svc.userReader = newMockUserReader().withEmailsErr(unexpectedErr)

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-nats-err",
			CommitteeUID: "committee-1",
			InviteeEmail: inviteeEmail,
			Status:       "pending",
			CreatedAt:    time.Now(),
		})

		ctx := testCtxWithEmail(principal, inviteeEmail)
		result, err := svc.AcceptInvite(ctx, &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-nats-err",
		})

		require.Error(t, err)
		assert.Nil(t, result)
		// Must be the original upstream error, not a fallback or forbidden
		assert.NotNil(t, err)
		var forbiddenErr *committeeservice.ForbiddenError
		assert.False(t, stderrors.As(err, &forbiddenErr), "non-NOT_FOUND error must not become Forbidden")
	})

	t.Run("NOT_FOUND + JWT email does not match invitee — returns Forbidden", func(t *testing.T) {
		svc, _, repo := setupServiceTestWithRepo()
		svc.userReader = mockUserReaderNotFound()

		repo.AddCommitteeInvite(&model.CommitteeInvite{
			UID:          "invite-wrong-email",
			CommitteeUID: "committee-1",
			InviteeEmail: inviteeEmail,
			Status:       "pending",
			CreatedAt:    time.Now(),
		})

		// JWT email belongs to a different user
		ctx := testCtxWithEmail(principal, "someone-else@example.com")
		result, err := svc.AcceptInvite(ctx, &committeeservice.AcceptInvitePayload{
			UID:       "committee-1",
			InviteUID: "invite-wrong-email",
		})

		require.Error(t, err)
		assert.Nil(t, result)
		var forbiddenErr *committeeservice.ForbiddenError
		require.ErrorAs(t, err, &forbiddenErr)
		assert.Contains(t, forbiddenErr.Message, "you are not the invitee")
	})
}

// TestResolveCallerEmail tests resolveCallerEmail directly, covering the primary auth-service
// path and the NOT_FOUND JWT email fallback without going through a full AcceptInvite call.
func TestResolveCallerEmail(t *testing.T) {
	const principal = "testuser"
	const primaryEmail = "testuser@example.com"

	t.Run("auth-service returns primary email", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = mockReaderForPrincipalEmail(principal, primaryEmail)

		ctx := testCtx(principal)
		got, err := svc.resolveCallerEmail(ctx)
		require.NoError(t, err)
		assert.Equal(t, primaryEmail, got)
	})

	t.Run("NOT_FOUND + verified JWT email in context — returns JWT email", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = mockUserReaderNotFound()

		ctx := testCtxWithEmail(principal, primaryEmail)
		got, err := svc.resolveCallerEmail(ctx)
		require.NoError(t, err)
		assert.Equal(t, primaryEmail, got)
	})

	t.Run("NOT_FOUND + no JWT email in context — propagates NOT_FOUND", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = mockUserReaderNotFound()

		ctx := testCtx(principal)
		_, err := svc.resolveCallerEmail(ctx)
		require.Error(t, err)
		var notFoundErr errs.NotFound
		assert.True(t, stderrors.As(err, &notFoundErr), "expected NotFound, got %T: %v", err, err)
	})

	t.Run("non-NOT_FOUND error — not suppressed by fallback", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = newMockUserReader().withEmailsErr(errs.NewServiceUnavailable("NATS timeout"))

		ctx := testCtxWithEmail(principal, primaryEmail)
		_, err := svc.resolveCallerEmail(ctx)
		require.Error(t, err)
		var unavailErr errs.ServiceUnavailable
		assert.True(t, stderrors.As(err, &unavailErr), "non-NOT_FOUND error must propagate unchanged, got %T: %v", err, err)
		assert.Contains(t, unavailErr.Error(), "NATS timeout")
	})

	t.Run("missing principal — returns validation error before any reader call", func(t *testing.T) {
		svc, _, _ := setupServiceTestWithRepo()
		svc.userReader = newMockUserReader()

		ctx := context.Background() // no principal in context
		_, err := svc.resolveCallerEmail(ctx)
		require.Error(t, err)
	})
}

// stubAuthenticator is a test-only Authenticator that returns fixed values.
type stubAuthenticator struct {
	principal string
	email     string
	err       error
}

func (s *stubAuthenticator) ParsePrincipal(_ context.Context, _ string, _ *slog.Logger) (string, string, error) {
	return s.principal, s.email, s.err
}

// TestJWTAuth_ContextPropagation verifies that JWTAuth stores both the principal and the
// email claim returned by ParsePrincipal into the request context.
func TestJWTAuth_ContextPropagation(t *testing.T) {
	t.Run("principal and email both stored in context", func(t *testing.T) {
		svc := &committeeServicesrvc{
			auth: &stubAuthenticator{principal: "testuser", email: "testuser@example.com"},
		}
		ctx, err := svc.JWTAuth(context.Background(), "token", nil)
		require.NoError(t, err)
		assert.Equal(t, "testuser", ctx.Value(constants.PrincipalContextID))
		assert.Equal(t, "testuser@example.com", ctx.Value(constants.EmailContextID))
	})

	t.Run("empty email (M2M token) stored as empty string in context", func(t *testing.T) {
		svc := &committeeServicesrvc{
			auth: &stubAuthenticator{principal: "svc-account", email: ""},
		}
		ctx, err := svc.JWTAuth(context.Background(), "token", nil)
		require.NoError(t, err)
		assert.Equal(t, "svc-account", ctx.Value(constants.PrincipalContextID))
		assert.Equal(t, "", ctx.Value(constants.EmailContextID))
	})

	t.Run("auth error propagated — context unchanged", func(t *testing.T) {
		svc := &committeeServicesrvc{
			auth: &stubAuthenticator{err: errs.NewValidation("bad token")},
		}
		// Seed a pre-existing value to verify JWTAuth doesn't wipe caller context on error.
		type callerKey struct{}
		seedCtx := context.WithValue(context.Background(), callerKey{}, "seed-value")
		returnedCtx, err := svc.JWTAuth(seedCtx, "bad", nil)
		require.Error(t, err)
		assert.Equal(t, "seed-value", returnedCtx.Value(callerKey{}), "pre-existing context values must be preserved on auth error")
		assert.Nil(t, returnedCtx.Value(constants.PrincipalContextID), "principal must not be set in context on auth error")
		assert.Nil(t, returnedCtx.Value(constants.EmailContextID), "email must not be set in context on auth error")
	})
}

func TestCreateInvite_InviterNameResolution(t *testing.T) {
	tests := []struct {
		name            string
		principal       string
		inviteeEmail    string
		userMeta        *model.UserMetadata
		metaErr         error
		wantInviterName string
	}{
		{
			name:            "resolves inviter name from meta.Name",
			principal:       "inviter-name-first",
			inviteeEmail:    "invitee-for-first@example.com",
			userMeta:        &model.UserMetadata{Name: "First Last"},
			wantInviterName: "First Last",
		},
		{
			name:            "resolves from GivenName+FamilyName when Name is empty",
			principal:       "inviter-name-second",
			inviteeEmail:    "invitee-for-second@example.com",
			userMeta:        &model.UserMetadata{GivenName: "Second", FamilyName: "Person"},
			wantInviterName: "Second Person",
		},
		{
			name:            "empty inviter name when lookup fails",
			principal:       "inviter-name-charlie",
			inviteeEmail:    "invitee-for-charlie@example.com",
			metaErr:         stderrors.New("auth service unavailable"),
			wantInviterName: "",
		},
		{
			name:            "empty inviter name when no principal in context",
			principal:       "",
			inviteeEmail:    "invitee-no-principal@example.com",
			wantInviterName: "",
		},
		{
			name:            "trims whitespace-only GivenName, uses FamilyName only",
			principal:       "inviter-name-whitespace",
			inviteeEmail:    "invitee-for-whitespace@example.com",
			userMeta:        &model.UserMetadata{GivenName: "  ", FamilyName: "Smith"},
			wantInviterName: "Smith",
		},
		{
			name:            "empty inviter name when both GivenName and FamilyName are whitespace-only",
			principal:       "inviter-name-all-whitespace",
			inviteeEmail:    "invitee-for-all-whitespace@example.com",
			userMeta:        &model.UserMetadata{GivenName: "  ", FamilyName: "  "},
			wantInviterName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := setupServiceTestWithRepo()
			reader := newMockUserReader()
			if tt.metaErr != nil {
				reader = reader.withMetadataErr(tt.metaErr)
			} else if tt.userMeta != nil {
				reader = reader.withMetadata(tt.principal, tt.userMeta)
			}
			svc.userReader = reader

			sender := svc.inviteSender.(*mockInviteSender)
			ctx := testCtx(tt.principal)
			_, err := svc.CreateInvite(ctx, &committeeservice.CreateInvitePayload{
				UID:          "committee-1",
				InviteeEmail: tt.inviteeEmail,
			})
			require.NoError(t, err)
			require.Len(t, sender.calls, 1)
			if tt.principal == "" {
				// No authenticated principal — the inviter cannot be resolved, so the email
				// is sent without an inviter object (rather than an empty one).
				assert.Nil(t, sender.calls[0].Inviter)
				return
			}
			require.NotNil(t, sender.calls[0].Inviter)
			assert.Equal(t, tt.wantInviterName, sender.calls[0].Inviter.Name)
			// Username is always the principal, even when profile/email lookups fail.
			assert.Equal(t, tt.principal, sender.calls[0].Inviter.Username)
		})
	}
}

// TestCreateInvite_PersistsInviterAndExpiry verifies the full inviter object
// (name/username/email/avatar) and the 30-day expiry are resolved and surfaced on the
// created invite so they can be indexed and shown in-app to the invitee.
func TestCreateInvite_PersistsInviterAndExpiry(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()
	principal := "inviter-principal-1"
	reader := newMockUserReader(principal, "first.last@example.com").
		withMetadata(principal, &model.UserMetadata{Name: "First Last", Picture: "https://cdn.example.com/avatar.png"})
	svc.userReader = reader

	ctx := testCtx(principal)
	before := time.Now().UTC()
	resp, err := svc.CreateInvite(ctx, &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "invitee-full@example.com",
	})
	after := time.Now().UTC()
	require.NoError(t, err)

	require.NotNil(t, resp.Inviter)
	require.NotNil(t, resp.Inviter.Name)
	assert.Equal(t, "First Last", *resp.Inviter.Name)
	require.NotNil(t, resp.Inviter.Username)
	assert.Equal(t, principal, *resp.Inviter.Username)
	require.NotNil(t, resp.Inviter.Email)
	assert.Equal(t, "first.last@example.com", *resp.Inviter.Email)
	require.NotNil(t, resp.Inviter.Avatar)
	assert.Equal(t, "https://cdn.example.com/avatar.png", *resp.Inviter.Avatar)

	require.NotNil(t, resp.ExpiresAt)
	expires, perr := time.Parse(time.RFC3339, *resp.ExpiresAt)
	require.NoError(t, perr)
	// created_at + 30 days, allowing 1s of slack for second-precision formatting.
	assert.False(t, expires.Before(before.Add(model.InviteDefaultTTL).Add(-time.Second)), "expiry earlier than created_at+30d")
	assert.False(t, expires.After(after.Add(model.InviteDefaultTTL).Add(time.Second)), "expiry later than created_at+30d")
}

// TestCreateInvite_InviterUsernameOnlyWhenNoUserReader verifies that when a principal is present
// but profile enrichment is unavailable (no user reader), the inviter is still persisted with the
// username — attribution is dropped only when there is no principal at all.
func TestCreateInvite_InviterUsernameOnlyWhenNoUserReader(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()
	svc.userReader = nil

	resp, err := svc.CreateInvite(testCtx("inviter-principal"), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "invitee-no-reader@example.com",
	})
	require.NoError(t, err)

	require.NotNil(t, resp.Inviter)
	require.NotNil(t, resp.Inviter.Username)
	assert.Equal(t, "inviter-principal", *resp.Inviter.Username)
	assert.Nil(t, resp.Inviter.Name)
	assert.Nil(t, resp.Inviter.Email)
	assert.Nil(t, resp.Inviter.Avatar)
}

// TestCreateInvite_ReinstatePersistsInviterAndExpiry verifies the revoked -> pending reinstate
// path refreshes the inviter to the re-inviting principal and starts a fresh 30-day expiry window
// (rather than carrying the stale values from the original invite).
func TestCreateInvite_ReinstatePersistsInviterAndExpiry(t *testing.T) {
	svc, _, repo := setupServiceTestWithRepo()
	principal := "inviter-principal-2"
	svc.userReader = newMockUserReader(principal, "first.last@example.com").
		withMetadata(principal, &model.UserMetadata{Name: "First Last", Picture: "https://cdn.example.com/avatar.png"})

	const email = "reinstate-invitee@example.com"
	// Seed a revoked invite with a long-past expiry to prove the reinstate refreshes it.
	repo.AddCommitteeInvite(&model.CommitteeInvite{
		UID:          "revoked-invite-reinstate",
		CommitteeUID: "committee-1",
		InviteeEmail: email,
		Status:       "revoked",
		CreatedAt:    time.Now().Add(-100 * 24 * time.Hour),
		ExpiresAt:    time.Now().Add(-70 * 24 * time.Hour),
	})

	before := time.Now().UTC()
	resp, err := svc.CreateInvite(testCtx(principal), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: email,
	})
	after := time.Now().UTC()
	require.NoError(t, err)
	assert.Equal(t, "pending", resp.Status)

	require.NotNil(t, resp.Inviter)
	require.NotNil(t, resp.Inviter.Name)
	assert.Equal(t, "First Last", *resp.Inviter.Name)
	require.NotNil(t, resp.Inviter.Username)
	assert.Equal(t, principal, *resp.Inviter.Username)
	require.NotNil(t, resp.Inviter.Email)
	assert.Equal(t, "first.last@example.com", *resp.Inviter.Email)
	require.NotNil(t, resp.Inviter.Avatar)
	assert.Equal(t, "https://cdn.example.com/avatar.png", *resp.Inviter.Avatar)

	require.NotNil(t, resp.ExpiresAt)
	expires, perr := time.Parse(time.RFC3339, *resp.ExpiresAt)
	require.NoError(t, perr)
	// Refreshed to now + 30 days (not the seeded past expiry), with 1s slack for second-precision formatting.
	assert.False(t, expires.Before(before.Add(model.InviteDefaultTTL).Add(-time.Second)), "expiry not refreshed to now+30d")
	assert.False(t, expires.After(after.Add(model.InviteDefaultTTL).Add(time.Second)), "expiry later than now+30d")

	// Read the invite back from storage to prove the refreshed fields were persisted, not just
	// reflected in the response (which is built from the same in-memory object passed to UpdateInvite).
	stored, _, getErr := repo.GetInvite(context.Background(), "revoked-invite-reinstate")
	require.NoError(t, getErr)
	require.NotNil(t, stored.Inviter)
	assert.Equal(t, "First Last", stored.Inviter.Name)
	assert.Equal(t, principal, stored.Inviter.Username)
	assert.Equal(t, "first.last@example.com", stored.Inviter.Email)
	assert.Equal(t, "https://cdn.example.com/avatar.png", stored.Inviter.Avatar)
	assert.False(t, stored.ExpiresAt.Before(before.Add(model.InviteDefaultTTL).Add(-time.Second)), "persisted expiry not refreshed to now+30d")
	assert.False(t, stored.ExpiresAt.After(after.Add(model.InviteDefaultTTL).Add(time.Second)), "persisted expiry later than now+30d")
}

func TestDomainGroupWeeklyBriefToGoa_ErrorReason(t *testing.T) {
	windowStart := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 5, 16, 23, 59, 59, 999999999, time.UTC)

	base := func(state model.GroupWeeklyBriefState, errorReason string) *model.GroupWeeklyBrief {
		return &model.GroupWeeklyBrief{
			UID:          "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			CommitteeUID: "7cad5a8d-19d0-41a4-81a6-043453daf9ee",
			WindowStart:  windowStart,
			WindowEnd:    windowEnd,
			State:        state,
			ErrorReason:  errorReason,
		}
	}

	tests := []struct {
		name            string
		brief           *model.GroupWeeklyBrief
		wantErrorReason *string
	}{
		{
			name:            "error state with non-empty reason exposes it",
			brief:           base(model.GroupWeeklyBriefStateError, "no_sources"),
			wantErrorReason: func() *string { s := "no_sources"; return &s }(),
		},
		{
			name:            "generated state with stale error_reason omits it",
			brief:           base(model.GroupWeeklyBriefStateGenerated, "no_sources"),
			wantErrorReason: nil,
		},
		{
			name:            "error state with empty reason omits it",
			brief:           base(model.GroupWeeklyBriefStateError, ""),
			wantErrorReason: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domainGroupWeeklyBriefToGoa(tt.brief)
			if tt.wantErrorReason == nil {
				assert.Nil(t, got.ErrorReason)
			} else {
				require.NotNil(t, got.ErrorReason)
				assert.Equal(t, *tt.wantErrorReason, *got.ErrorReason)
			}
		})
	}
}

// setupUpdateLinkService builds the service over an in-memory link repository holding one folder and one link of
// committee-1, the link at the root, created by a user other than the one the tests act as.
func setupUpdateLinkService(t *testing.T) (*committeeServicesrvc, *mock.MockLinkRepository) {
	t.Helper()
	ctx := context.Background()
	linkRepo := mock.NewMockLinkRepository()
	require.NoError(t, linkRepo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "folder-aaa", CommitteeUID: "committee-1", Name: "Governance"}))
	require.NoError(t, linkRepo.CreateLink(ctx, &model.CommitteeLink{
		UID:          "link-1",
		CommitteeUID: "committee-1",
		Name:         "Old name",
		URL:          "https://example.com/old",
		Description:  "Old description",
		CreatedBy:    &model.CommitteeUser{Username: "seed-creator"},
		UpdatedBy:    &model.CommitteeUser{Username: "seed-creator"},
	}))

	svc := &committeeServicesrvc{
		linkReader: internalservice.NewLinkReaderOrchestrator(internalservice.WithLinkReader(linkRepo)),
		linkWriter: internalservice.NewLinkWriterOrchestrator(
			internalservice.WithLinkWriter(linkRepo),
			internalservice.WithLinkReaderForWriter(linkRepo),
			internalservice.WithDocumentListerForWriter(mock.NewMockDocumentRepository()),
		),
	}
	return svc, linkRepo
}

func TestUpdateCommitteeLink(t *testing.T) {
	newPayload := func(mutate func(*committeeservice.UpdateCommitteeLinkPayload)) *committeeservice.UpdateCommitteeLinkPayload {
		p := &committeeservice.UpdateCommitteeLinkPayload{
			IfMatch: strPtr("1"),
			UID:     strPtr("committee-1"),
			LinkUID: strPtr("link-1"),
			Name:    "New name",
			URL:     "https://example.com/new",
		}
		if mutate != nil {
			mutate(p)
		}
		return p
	}

	t.Run("replaces the link, moves it into the folder and stamps who changed it", func(t *testing.T) {
		svc, linkRepo := setupUpdateLinkService(t)

		res, err := svc.UpdateCommitteeLink(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeLinkPayload) {
			p.Description = strPtr("New description")
			p.FolderUID = strPtr("folder-aaa")
		}))

		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "link-1", *res.UID)
		assert.Equal(t, "New name", *res.Name)
		assert.Equal(t, "https://example.com/new", *res.URL)
		assert.Equal(t, "New description", *res.Description)
		require.NotNil(t, res.FolderUID)
		assert.Equal(t, "folder-aaa", *res.FolderUID)
		require.NotNil(t, res.UpdatedBy)
		assert.Equal(t, "first-last", *res.UpdatedBy.Username)
		require.NotNil(t, res.CreatedBy)
		assert.Equal(t, "seed-creator", *res.CreatedBy.Username, "who created the link must not change")

		stored, revision, err := linkRepo.GetLink(context.Background(), "committee-1", "link-1")
		require.NoError(t, err)
		assert.Equal(t, "New name", stored.Name)
		assert.Equal(t, uint64(2), revision)
	})

	t.Run("an omitted description is cleared and an omitted folder moves the link to the root", func(t *testing.T) {
		svc, _ := setupUpdateLinkService(t)
		_, err := svc.UpdateCommitteeLink(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeLinkPayload) {
			p.FolderUID = strPtr("folder-aaa")
		}))
		require.NoError(t, err)

		res, err := svc.UpdateCommitteeLink(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeLinkPayload) {
			p.IfMatch = strPtr("2")
		}))

		require.NoError(t, err)
		assert.Nil(t, res.FolderUID)
		assert.Nil(t, res.Description)
	})

	t.Run("refuses a request it cannot act on, and changes nothing", func(t *testing.T) {
		tests := []struct {
			name     string
			ctx      context.Context
			mutate   func(*committeeservice.UpdateCommitteeLinkPayload)
			wantType string
		}{
			{name: "no If-Match", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeLinkPayload) { p.IfMatch = nil }, wantType: "bad request"},
			{name: "an If-Match that is not a revision", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeLinkPayload) { p.IfMatch = strPtr("not-a-revision") }, wantType: "bad request"},
			{name: "no identity in the token", ctx: context.Background(), mutate: nil, wantType: "bad request"},
			{name: "a target folder that does not exist", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeLinkPayload) { p.FolderUID = strPtr("no-such-folder") }, wantType: "bad request"},
			{name: "a link that does not exist", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeLinkPayload) { p.LinkUID = strPtr("no-such-link") }, wantType: "not found"},
			{name: "a link of another committee", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeLinkPayload) { p.UID = strPtr("committee-2") }, wantType: "not found"},
			{name: "a stale If-Match", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeLinkPayload) { p.IfMatch = strPtr("7") }, wantType: "conflict"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				svc, linkRepo := setupUpdateLinkService(t)

				res, err := svc.UpdateCommitteeLink(tt.ctx, newPayload(tt.mutate))

				require.Error(t, err)
				assert.Nil(t, res)
				switch tt.wantType {
				case "bad request":
					var badRequest *committeeservice.BadRequestError
					assert.ErrorAs(t, err, &badRequest)
				case "not found":
					var notFound *committeeservice.NotFoundError
					assert.ErrorAs(t, err, &notFound)
				case "conflict":
					var conflict *committeeservice.ConflictError
					assert.ErrorAs(t, err, &conflict)
				}

				stored, revision, getErr := linkRepo.GetLink(context.Background(), "committee-1", "link-1")
				require.NoError(t, getErr)
				assert.Equal(t, "Old name", stored.Name, "a refused update must not change the link")
				assert.Equal(t, uint64(1), revision)
			})
		}
	})
}

// failingFolderReader is a link repository whose folder lookup fails the way a storage outage does, not as "not found".
type failingFolderReader struct{ *mock.MockLinkRepository }

func (failingFolderReader) GetLinkFolder(context.Context, string, string) (*model.CommitteeLinkFolder, uint64, error) {
	return nil, 0, errs.NewUnexpected("folder store unavailable")
}

// setupUpdateDocumentService builds the service over in-memory repositories holding one folder of each of two
// committees and, in committee-1, doc-1 ("Minutes", at the root, created by a user other than the one the tests act as)
// and doc-2 ("Agenda"), plus doc-3 in committee-2 that shares doc-1's name.
func setupUpdateDocumentService(t *testing.T) (*committeeServicesrvc, *mock.MockDocumentRepository) {
	t.Helper()
	ctx := context.Background()
	linkRepo := mock.NewMockLinkRepository()
	docRepo := mock.NewMockDocumentRepository()
	require.NoError(t, linkRepo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "folder-aaa", CommitteeUID: "committee-1", Name: "Governance"}))
	require.NoError(t, linkRepo.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: "folder-other", CommitteeUID: "committee-2", Name: "Elsewhere"}))
	for _, d := range []*model.CommitteeDocument{
		{
			UID: "doc-1", CommitteeUID: "committee-1", Name: "Minutes", Description: "Old description", FileName: "minutes.pdf", FileSize: 2048,
			ContentType: "application/pdf", CreatedBy: &model.CommitteeUser{Username: "seed-creator"}, UpdatedBy: &model.CommitteeUser{Username: "seed-creator"},
		},
		{UID: "doc-2", CommitteeUID: "committee-1", Name: "Agenda", FileName: "agenda.pdf", ContentType: "application/pdf"},
		{UID: "doc-3", CommitteeUID: "committee-2", Name: "Minutes", FileName: "minutes.pdf", ContentType: "application/pdf"},
	} {
		require.NoError(t, docRepo.CreateDocumentMetadata(ctx, d))
	}

	svc := &committeeServicesrvc{
		linkReader: internalservice.NewLinkReaderOrchestrator(internalservice.WithLinkReader(linkRepo)),
		docWriter: internalservice.NewDocumentWriterOrchestrator(
			internalservice.WithDocumentWriter(docRepo),
			internalservice.WithDocumentReaderForWriter(docRepo),
		),
	}
	return svc, docRepo
}

func TestUpdateCommitteeDocument(t *testing.T) {
	newPayload := func(mutate func(*committeeservice.UpdateCommitteeDocumentPayload)) *committeeservice.UpdateCommitteeDocumentPayload {
		p := &committeeservice.UpdateCommitteeDocumentPayload{
			IfMatch:     "1",
			UID:         "committee-1",
			DocumentUID: "doc-1",
			Name:        "Minutes 2026",
		}
		if mutate != nil {
			mutate(p)
		}
		return p
	}

	t.Run("renames the document, moves it into the folder and stamps who changed it", func(t *testing.T) {
		svc, docRepo := setupUpdateDocumentService(t)

		res, err := svc.UpdateCommitteeDocument(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeDocumentPayload) {
			p.Description = strPtr("New description")
			p.FolderUID = strPtr("folder-aaa")
		}))

		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "doc-1", *res.UID)
		assert.Equal(t, "Minutes 2026", *res.Name)
		assert.Equal(t, "New description", *res.Description)
		require.NotNil(t, res.FolderUID)
		assert.Equal(t, "folder-aaa", *res.FolderUID)
		assert.Equal(t, "minutes.pdf", *res.FileName, "the uploaded file is not editable")
		assert.Equal(t, int64(2048), *res.FileSize)
		require.NotNil(t, res.UpdatedBy)
		assert.Equal(t, "first-last", *res.UpdatedBy.Username)
		require.NotNil(t, res.CreatedBy)
		assert.Equal(t, "seed-creator", *res.CreatedBy.Username, "who uploaded the document must not change")

		stored, revision, err := docRepo.GetDocumentMetadata(context.Background(), "committee-1", "doc-1")
		require.NoError(t, err)
		assert.Equal(t, "Minutes 2026", stored.Name)
		assert.Equal(t, uint64(2), revision)
	})

	t.Run("an omitted description is cleared and an omitted folder moves the document to the root", func(t *testing.T) {
		svc, _ := setupUpdateDocumentService(t)
		_, err := svc.UpdateCommitteeDocument(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeDocumentPayload) {
			p.FolderUID = strPtr("folder-aaa")
		}))
		require.NoError(t, err)

		res, err := svc.UpdateCommitteeDocument(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeDocumentPayload) {
			p.IfMatch = "2"
		}))

		require.NoError(t, err)
		assert.Nil(t, res.FolderUID)
		assert.Nil(t, res.Description)
	})

	t.Run("refuses a request it cannot act on, and changes nothing", func(t *testing.T) {
		tests := []struct {
			name     string
			ctx      context.Context
			mutate   func(*committeeservice.UpdateCommitteeDocumentPayload)
			wantType string
		}{
			{name: "no If-Match", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.IfMatch = "" }, wantType: "bad request"},
			{name: "an If-Match that is not a revision", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.IfMatch = "not-a-revision" }, wantType: "bad request"},
			{name: "no identity in the token", ctx: context.Background(), mutate: nil, wantType: "bad request"},
			{name: "a target folder that does not exist", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.FolderUID = strPtr("no-such-folder") }, wantType: "bad request"},
			{name: "a target folder of another committee", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.FolderUID = strPtr("folder-other") }, wantType: "bad request"},
			{name: "a document that does not exist", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.DocumentUID = "no-such-document" }, wantType: "not found"},
			{name: "a document of another committee", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.UID = "committee-2" }, wantType: "not found"},
			{name: "a stale If-Match", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.IfMatch = "7" }, wantType: "conflict"},
			{name: "a name another document in the committee holds", ctx: testCtx("first-last"), mutate: func(p *committeeservice.UpdateCommitteeDocumentPayload) { p.Name = "Agenda" }, wantType: "conflict"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				svc, docRepo := setupUpdateDocumentService(t)

				res, err := svc.UpdateCommitteeDocument(tt.ctx, newPayload(tt.mutate))

				require.Error(t, err)
				assert.Nil(t, res)
				switch tt.wantType {
				case "bad request":
					var badRequest *committeeservice.BadRequestError
					assert.ErrorAs(t, err, &badRequest)
				case "not found":
					var notFound *committeeservice.NotFoundError
					assert.ErrorAs(t, err, &notFound)
				case "conflict":
					var conflict *committeeservice.ConflictError
					assert.ErrorAs(t, err, &conflict)
				}

				stored, revision, getErr := docRepo.GetDocumentMetadata(context.Background(), "committee-1", "doc-1")
				require.NoError(t, getErr)
				assert.Equal(t, "Minutes", stored.Name, "a refused update must not change the document")
				assert.Equal(t, uint64(1), revision)
			})
		}
	})

	t.Run("a failing folder lookup is a server error, not the caller's mistake", func(t *testing.T) {
		svc, docRepo := setupUpdateDocumentService(t)
		svc.linkReader = internalservice.NewLinkReaderOrchestrator(internalservice.WithLinkReader(failingFolderReader{mock.NewMockLinkRepository()}))

		res, err := svc.UpdateCommitteeDocument(testCtx("first-last"), newPayload(func(p *committeeservice.UpdateCommitteeDocumentPayload) {
			p.FolderUID = strPtr("folder-aaa")
		}))

		require.Error(t, err)
		assert.Nil(t, res)
		var badRequest *committeeservice.BadRequestError
		assert.False(t, stderrors.As(err, &badRequest), "a storage failure must not be reported as a bad request")
		stored, _, getErr := docRepo.GetDocumentMetadata(context.Background(), "committee-1", "doc-1")
		require.NoError(t, getErr)
		assert.Equal(t, "Minutes", stored.Name)
	})
}
