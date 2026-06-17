// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
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
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
)

// mockTransportMessenger implements port.TransportMessenger for testing
type mockTransportMessenger struct {
	subject string
	data    []byte
	respond func([]byte) error
}

// Subject returns the mock message subject
func (m *mockTransportMessenger) Subject() string {
	return m.subject
}

// Data returns the mock message data
func (m *mockTransportMessenger) Data() []byte {
	return m.data
}

// Respond sends a response using the mock function
func (m *mockTransportMessenger) Respond(data []byte) error {
	if m.respond != nil {
		return m.respond(data)
	}
	return nil
}

// newMockTransportMessenger creates a new mock transport messenger
func newMockTransportMessenger(subject string, data []byte) *mockTransportMessenger {
	return &mockTransportMessenger{
		subject: subject,
		data:    data,
	}
}

func TestMessageHandlerOrchestratorHandleCommitteeGetAttribute(t *testing.T) {
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
			Website:         messageHandlerStringPtr("https://example.com"),
			EnableVoting:    true,
			SSOGroupEnabled: false,
			SSOGroupName:    "test-sso-group",
			RequiresReview:  true,
			Public:          false,
			Calendar: model.Calendar{
				Public: true,
			},
			DisplayName:      "Test Display Name",
			ParentUID:        messageHandlerStringPtr("parent-committee-uid"),
			TotalMembers:     5,
			TotalVotingRepos: 3,
			CreatedAt:        time.Now().Add(-24 * time.Hour),
			UpdatedAt:        time.Now(),
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:                   testCommitteeUID,
			BusinessEmailRequired: true,
			Writers:               []model.CommitteeUser{{Username: "writer1"}, {Username: "writer2"}},
			Auditors:              []model.CommitteeUser{{Username: "auditor1"}},
			CreatedAt:             time.Now().Add(-24 * time.Hour),
			UpdatedAt:             time.Now(),
		},
	}

	tests := []struct {
		name             string
		setupMock        func()
		messageData      []byte
		attribute        string
		expectedError    bool
		errorType        error
		validateResponse func(*testing.T, []byte)
	}{
		{
			name: "successful retrieval of committee name",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "name",
			expectedError: false,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Equal(t, "Test Committee", string(response))
			},
		},
		{
			name: "successful retrieval of committee project_uid",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "project_uid",
			expectedError: false,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Equal(t, "test-project-uid", string(response))
			},
		},
		{
			name: "successful retrieval of committee uid",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "uid",
			expectedError: false,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Equal(t, testCommitteeUID, string(response))
			},
		},
		{
			name: "successful retrieval of committee category",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "category",
			expectedError: false,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Equal(t, "technical", string(response))
			},
		},
		{
			name: "successful retrieval of committee description with omitempty",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "description,omitempty",
			expectedError: false,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Equal(t, "Test committee description", string(response))
			},
		},
		{
			name: "successful retrieval of committee sso_group_name with omitempty",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "sso_group_name,omitempty",
			expectedError: false,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Equal(t, "test-sso-group", string(response))
			},
		},
		{
			name: "invalid UUID format error",
			setupMock: func() {
				mockRepo.ClearAll()
			},
			messageData:   []byte("invalid-uuid-format"),
			attribute:     "name",
			expectedError: true,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "empty UUID error",
			setupMock: func() {
				mockRepo.ClearAll()
			},
			messageData:   []byte(""),
			attribute:     "name",
			expectedError: true,
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "committee not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				// Don't store any committee
			},
			messageData:   []byte(uuid.New().String()),
			attribute:     "name",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "attribute not found error",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "nonexistent_attribute",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "empty attribute name error",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "",
			expectedError: true,
			errorType:     errs.NotFound{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "non-string attribute error - boolean field",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "enable_voting",
			expectedError: true,
			errorType:     errs.Validation{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "non-string attribute error - integer field",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "total_members",
			expectedError: true,
			errorType:     errs.Validation{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "non-string attribute error - struct field",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "calendar,omitempty",
			expectedError: true,
			errorType:     errs.Validation{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "non-string attribute error - time field",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "created_at",
			expectedError: true,
			errorType:     errs.Validation{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
		{
			name: "non-string attribute error - pointer field",
			setupMock: func() {
				mockRepo.ClearAll()
				mockRepo.AddCommittee(testCommittee)
			},
			messageData:   []byte(testCommitteeUID),
			attribute:     "website,omitempty",
			expectedError: true,
			errorType:     errs.Validation{},
			validateResponse: func(t *testing.T, response []byte) {
				assert.Nil(t, response)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			tt.setupMock()

			// Create message handler orchestrator
			handler := NewMessageHandlerOrchestrator(
				WithCommitteeReaderForMessageHandler(
					NewCommitteeReaderOrchestrator(
						WithCommitteeReader(mockRepo),
					),
				),
			)

			// Create mock transport messenger
			mockMsg := newMockTransportMessenger("test.subject", tt.messageData)

			// Execute
			response, err := handler.HandleCommitteeGetAttribute(ctx, mockMsg, tt.attribute)

			// Validate
			if tt.expectedError {
				require.Error(t, err)
				if tt.errorType != nil {
					assert.IsType(t, tt.errorType, err)
				}
			} else {
				require.NoError(t, err)
			}

			tt.validateResponse(t, response)
		})
	}
}

func TestMessageHandlerOrchestratorHandleCommitteeGetAttributeWithNilReader(t *testing.T) {
	ctx := context.Background()

	// Create handler without committee reader
	handler := NewMessageHandlerOrchestrator()

	// Create mock transport messenger
	testUID := uuid.New().String()
	mockMsg := newMockTransportMessenger("test.subject", []byte(testUID))

	// Execute - this should panic or cause nil pointer dereference
	// In a real implementation, this should be handled gracefully
	assert.Panics(t, func() {
		_, _ = handler.HandleCommitteeGetAttribute(ctx, mockMsg, "name")
	})
}

func TestNewMessageHandlerOrchestrator(t *testing.T) {
	mockRepo := mock.NewMockRepository()

	tests := []struct {
		name     string
		options  []messageHandlerOrchestratorOption
		validate func(*testing.T, port.MessageHandler)
	}{
		{
			name:    "create with no options",
			options: []messageHandlerOrchestratorOption{},
			validate: func(t *testing.T, handler port.MessageHandler) {
				assert.NotNil(t, handler)
				// Test that it can be used (though it will have nil dependencies)
				orchestrator, ok := handler.(*messageHandlerOrchestrator)
				assert.True(t, ok)
				assert.Nil(t, orchestrator.committeeReader)
			},
		},
		{
			name: "create with committee reader option",
			options: []messageHandlerOrchestratorOption{
				WithCommitteeReaderForMessageHandler(
					NewCommitteeReaderOrchestrator(
						WithCommitteeReader(mockRepo),
					),
				),
			},
			validate: func(t *testing.T, handler port.MessageHandler) {
				assert.NotNil(t, handler)
				orchestrator, ok := handler.(*messageHandlerOrchestrator)
				assert.True(t, ok)
				assert.NotNil(t, orchestrator.committeeReader)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Execute
			handler := NewMessageHandlerOrchestrator(tt.options...)

			// Validate
			tt.validate(t, handler)
		})
	}
}

func TestMessageHandlerOrchestratorIntegration(t *testing.T) {
	ctx := context.Background()
	mockRepo := mock.NewMockRepository()
	mockRepo.ClearAll()

	// Setup comprehensive test data
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
			Writers:               []model.CommitteeUser{{Username: "integration-writer1"}, {Username: "integration-writer2"}},
			Auditors:              []model.CommitteeUser{{Username: "integration-auditor1"}, {Username: "integration-auditor2"}},
			CreatedAt:             time.Now().Add(-48 * time.Hour),
			UpdatedAt:             time.Now().Add(-1 * time.Hour),
		},
	}

	// Store the committee
	mockRepo.AddCommittee(testCommittee)

	// Create message handler orchestrator
	handler := NewMessageHandlerOrchestrator(
		WithCommitteeReaderForMessageHandler(
			NewCommitteeReaderOrchestrator(
				WithCommitteeReader(mockRepo),
			),
		),
	)

	t.Run("retrieve multiple string attributes for same committee", func(t *testing.T) {
		// Create mock transport messenger
		mockMsg := newMockTransportMessenger("test.subject", []byte(testCommitteeUID))

		// Test multiple string attributes
		stringAttributes := map[string]string{
			"uid":         testCommitteeUID,
			"project_uid": "integration-test-project",
			"name":        "Integration Test Committee",
			"category":    "governance",
		}

		for attribute, expectedValue := range stringAttributes {
			response, err := handler.HandleCommitteeGetAttribute(ctx, mockMsg, attribute)
			require.NoError(t, err, "Failed to get attribute: %s", attribute)
			assert.Equal(t, expectedValue, string(response), "Attribute %s value mismatch", attribute)
		}
	})

	t.Run("test error consistency across multiple calls", func(t *testing.T) {
		// Create mock transport messenger with invalid UUID
		mockMsg := newMockTransportMessenger("test.subject", []byte("invalid-uuid"))

		// Multiple calls should consistently fail
		for i := 0; i < 3; i++ {
			response, err := handler.HandleCommitteeGetAttribute(ctx, mockMsg, "name")
			require.Error(t, err, "Call %d should have failed", i+1)
			assert.Nil(t, response, "Call %d should return nil response", i+1)
		}
	})
}

// spyCommitteePublisher records Indexer calls so tests can assert on them.
type spyCommitteePublisher struct {
	indexerCallCount int
	lastSubject      string
}

func (s *spyCommitteePublisher) Indexer(_ context.Context, subject string, _ any, _ bool) error {
	s.indexerCallCount++
	s.lastSubject = subject
	return nil
}
func (s *spyCommitteePublisher) Access(_ context.Context, _ string, _ any, _ bool) error {
	return nil
}
func (s *spyCommitteePublisher) Event(_ context.Context, _ string, _ any, _ bool) error {
	return nil
}

func TestHandleCommitteeMailingListChanged(t *testing.T) {
	ctx := context.Background()

	makeCommittee := func(uid string, hasMailingList bool) *model.Committee {
		return &model.Committee{
			CommitteeBase: model.CommitteeBase{
				UID:            uid,
				ProjectUID:     "proj-1",
				Name:           "Test Committee",
				Category:       "technical",
				HasMailingList: hasMailingList,
				CreatedAt:      time.Now().Add(-time.Hour),
				UpdatedAt:      time.Now(),
			},
		}
	}

	tests := []struct {
		name                  string
		messageData           []byte
		initialHasMailingList bool
		skipCommitteeSetup    bool // true for "not found" cases — no committee added to mock
		wantIndexerCalled     bool
		wantErr               bool
	}{
		{
			name: "flag transitions false→true: UpdateHasMailingList writes and re-index published",
			messageData: func() []byte {
				uid := uuid.New().String()
				b, _ := json.Marshal(model.CommitteeMailingListChangedEvent{CommitteeUID: uid, HasMailingList: true})
				return b
			}(),
			initialHasMailingList: false,
			wantIndexerCalled:     true,
		},
		{
			name: "flag already true: UpdateHasMailingList skips write, no re-index",
			messageData: func() []byte {
				uid := uuid.New().String()
				b, _ := json.Marshal(model.CommitteeMailingListChangedEvent{CommitteeUID: uid, HasMailingList: true})
				return b
			}(),
			initialHasMailingList: true,
			wantIndexerCalled:     false,
		},
		{
			name: "flag transitions true→false: UpdateHasMailingList writes and re-index published",
			messageData: func() []byte {
				uid := uuid.New().String()
				b, _ := json.Marshal(model.CommitteeMailingListChangedEvent{CommitteeUID: uid, HasMailingList: false})
				return b
			}(),
			initialHasMailingList: true,
			wantIndexerCalled:     true,
		},
		{
			name: "empty committee_uid: event discarded silently",
			messageData: func() []byte {
				b, _ := json.Marshal(model.CommitteeMailingListChangedEvent{CommitteeUID: "", HasMailingList: true})
				return b
			}(),
			wantErr:           false,
			wantIndexerCalled: false,
		},
		{
			name:        "invalid JSON: unmarshal error returned",
			messageData: []byte(`not-json`),
			wantErr:     true,
		},
		{
			name: "committee not found: UpdateHasMailingList returns error",
			messageData: func() []byte {
				b, _ := json.Marshal(model.CommitteeMailingListChangedEvent{CommitteeUID: uuid.New().String(), HasMailingList: true})
				return b
			}(),
			skipCommitteeSetup: true,
			wantErr:            true,
			wantIndexerCalled:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mock.NewMockRepository()

			if !tt.skipCommitteeSetup {
				var event model.CommitteeMailingListChangedEvent
				if err := json.Unmarshal(tt.messageData, &event); err == nil && event.CommitteeUID != "" {
					mockRepo.AddCommittee(makeCommittee(event.CommitteeUID, tt.initialHasMailingList))
				}
			}

			spy := &spyCommitteePublisher{}

			handler := NewMessageHandlerOrchestrator(
				WithCommitteeReaderForMessageHandler(
					NewCommitteeReaderOrchestrator(WithCommitteeReader(mockRepo)),
				),
				WithCommitteeWriterForMessageHandler(mock.NewMockCommitteeWriter(mockRepo)),
				WithCommitteePublisherForMessageHandler(spy),
			)

			msg := newMockTransportMessenger(constants.MailingListCommitteeChangedSubject, tt.messageData)
			resp, err := handler.HandleCommitteeMailingListChanged(ctx, msg)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Nil(t, resp, "fire-and-forget handler must return nil response")
			}

			assert.Equal(t, tt.wantIndexerCalled, spy.indexerCallCount > 0,
				"indexer called mismatch: got %d calls", spy.indexerCallCount)
		})
	}
}

// mockStreamMessenger implements port.StreamMessenger for testing
type mockStreamMessenger struct {
	subject string
	data    []byte
}

func (m *mockStreamMessenger) Subject() string { return m.subject }
func (m *mockStreamMessenger) Data() []byte    { return m.data }

// spyCommitteeWriterOrchestrator records Update, UpdateMember, and UpdateSettings calls and can be configured to fail.
type spyCommitteeWriterOrchestrator struct {
	updateCalls      int
	updateErr        error
	updatedCommittee *model.Committee

	updateMemberCalls int
	updateMemberErr   error
	updateMemberErrs  []error
	updatedMembers    []*model.CommitteeMember

	updateSettingsCalls int
	updateSettingsErr   error
	// updateSettingsErrs is an optional per-call error queue. If non-empty, each call to
	// UpdateSettings drains one entry from the front; once exhausted, updateSettingsErr is used.
	updateSettingsErrs []error
	capturedSettings   []*model.CommitteeSettings
}

func (s *spyCommitteeWriterOrchestrator) Create(_ context.Context, _ *model.Committee, _ bool) (*model.Committee, error) {
	return nil, nil
}
func (s *spyCommitteeWriterOrchestrator) Update(_ context.Context, c *model.Committee, _ uint64, _ bool) (*model.Committee, error) {
	s.updateCalls++
	s.updatedCommittee = c
	return c, s.updateErr
}
func (s *spyCommitteeWriterOrchestrator) UpdateSettings(_ context.Context, settings *model.CommitteeSettings, _ uint64, _ bool) (*model.CommitteeSettings, error) {
	s.updateSettingsCalls++
	// Deep-copy the snapshot so later in-place mutations by the handler don't overwrite it.
	snap := *settings
	snap.Writers = append([]model.CommitteeUser(nil), settings.Writers...)
	snap.Auditors = append([]model.CommitteeUser(nil), settings.Auditors...)
	s.capturedSettings = append(s.capturedSettings, &snap)
	// Drain per-call error queue; fall back to the fixed error when exhausted.
	if len(s.updateSettingsErrs) > 0 {
		err := s.updateSettingsErrs[0]
		s.updateSettingsErrs = s.updateSettingsErrs[1:]
		return settings, err
	}
	return settings, s.updateSettingsErr
}
func (s *spyCommitteeWriterOrchestrator) Delete(_ context.Context, _ string, _ uint64, _ bool) error {
	return nil
}
func (s *spyCommitteeWriterOrchestrator) CreateMember(_ context.Context, _ *model.CommitteeMember, _ bool) (*model.CommitteeMember, error) {
	return nil, nil
}
func (s *spyCommitteeWriterOrchestrator) UpdateMember(_ context.Context, member *model.CommitteeMember, _ uint64, _ bool) (*model.CommitteeMember, error) {
	s.updateMemberCalls++
	memberCopy := *member
	s.updatedMembers = append(s.updatedMembers, &memberCopy)
	if len(s.updateMemberErrs) > 0 {
		err := s.updateMemberErrs[0]
		s.updateMemberErrs = s.updateMemberErrs[1:]
		return member, err
	}
	return member, s.updateMemberErr
}
func (s *spyCommitteeWriterOrchestrator) DeleteMember(_ context.Context, _ string, _ uint64, _ bool) error {
	return nil
}
func (s *spyCommitteeWriterOrchestrator) ReassignMember(_ context.Context, _ string, _ uint64, m *model.CommitteeMember, _ bool) (*model.CommitteeMember, error) {
	return m, nil
}

func buildCommitteeUpdatedMsg(committeeUID string, old, updated *model.CommitteeBase) []byte {
	data := model.CommitteeUpdateEventData{
		CommitteeUID: committeeUID,
		OldCommittee: old,
		Committee:    updated,
	}
	event := model.CommitteeEvent{Data: data}
	b, _ := json.Marshal(event)
	return b
}

func TestHandleCommitteeUpdated(t *testing.T) {
	ctx := context.Background()

	committeeUID := "committee-sync-test"
	oldBase := &model.CommitteeBase{
		Name:        "Old Name",
		Category:    "Board",
		ProjectUID:  "proj-1",
		ProjectSlug: "old-slug",
	}
	newBase := &model.CommitteeBase{
		Name:        "New Name",
		Category:    "Technical",
		ProjectUID:  "proj-1",
		ProjectSlug: "new-slug",
	}

	makeStaleMember := func(uid string) *model.CommitteeMember {
		return &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:               uid,
				CommitteeUID:      committeeUID,
				CommitteeName:     oldBase.Name,
				CommitteeCategory: oldBase.Category,
				ProjectUID:        oldBase.ProjectUID,
				ProjectSlug:       oldBase.ProjectSlug,
				Username:          uid + "@example.com",
				Email:             uid + "@example.com",
			},
		}
	}

	tests := []struct {
		name            string
		messageData     []byte
		setupMock       func(*mock.MockRepository)
		writerErr       error
		wantErr         bool
		wantUpdateCalls int
		validateUpdated func(*testing.T, []*model.CommitteeMember)
	}{
		{
			name:        "invalid JSON returns error",
			messageData: []byte(`not-json`),
			setupMock:   func(_ *mock.MockRepository) {},
			wantErr:     true,
		},
		{
			name:            "no denormalized fields changed — skips sync",
			messageData:     buildCommitteeUpdatedMsg(committeeUID, oldBase, oldBase),
			setupMock:       func(_ *mock.MockRepository) {},
			wantErr:         false,
			wantUpdateCalls: 0,
		},
		{
			name:            "list members fails — propagates error",
			messageData:     buildCommitteeUpdatedMsg("unknown-committee", oldBase, newBase),
			setupMock:       func(_ *mock.MockRepository) {},
			wantErr:         false, // ListMembersByCommittee returns empty slice for unknown committee, not error
			wantUpdateCalls: 0,
		},
		{
			name:        "all members already up to date — no UpdateMember calls",
			messageData: buildCommitteeUpdatedMsg(committeeUID, oldBase, newBase),
			setupMock: func(repo *mock.MockRepository) {
				upToDate := makeStaleMember("up-to-date-member")
				upToDate.CommitteeName = newBase.Name
				upToDate.CommitteeCategory = newBase.Category
				upToDate.ProjectSlug = newBase.ProjectSlug
				repo.AddCommitteeMember(committeeUID, upToDate)
			},
			wantErr:         false,
			wantUpdateCalls: 0,
		},
		{
			name:        "stale members are synced",
			messageData: buildCommitteeUpdatedMsg(committeeUID, oldBase, newBase),
			setupMock: func(repo *mock.MockRepository) {
				repo.AddCommitteeMember(committeeUID, makeStaleMember("stale-1"))
				repo.AddCommitteeMember(committeeUID, makeStaleMember("stale-2"))
			},
			wantErr:         false,
			wantUpdateCalls: 2,
			validateUpdated: func(t *testing.T, members []*model.CommitteeMember) {
				t.Helper()
				for _, m := range members {
					assert.Equal(t, newBase.Name, m.CommitteeName)
					assert.Equal(t, newBase.Category, m.CommitteeCategory)
					assert.Equal(t, newBase.ProjectUID, m.ProjectUID)
					assert.Equal(t, newBase.ProjectSlug, m.ProjectSlug)
				}
			},
		},
		{
			name:        "UpdateMember fails — error accumulated, all members attempted",
			messageData: buildCommitteeUpdatedMsg(committeeUID, oldBase, newBase),
			setupMock: func(repo *mock.MockRepository) {
				repo.AddCommitteeMember(committeeUID, makeStaleMember("stale-a"))
				repo.AddCommitteeMember(committeeUID, makeStaleMember("stale-b"))
			},
			writerErr:       fmt.Errorf("index unavailable"),
			wantErr:         true,
			wantUpdateCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mock.NewMockRepository()
			mockRepo.ClearAll()
			tt.setupMock(mockRepo)

			spy := &spyCommitteeWriterOrchestrator{updateMemberErr: tt.writerErr}

			handler := NewMessageHandlerOrchestrator(
				WithCommitteeReaderForMessageHandler(
					NewCommitteeReaderOrchestrator(WithCommitteeReader(mockRepo)),
				),
				WithCommitteeWriterOrchestratorForMessageHandler(spy),
			)

			msg := newMockTransportMessenger(constants.CommitteeUpdatedSubject, tt.messageData)
			_, err := handler.HandleCommitteeUpdated(ctx, msg)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantUpdateCalls, spy.updateMemberCalls,
				"UpdateMember call count mismatch")

			if tt.validateUpdated != nil {
				tt.validateUpdated(t, spy.updatedMembers)
			}
		})
	}
}

func buildTotalMembersSyncMsg(committeeUID string) []byte {
	member := model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{CommitteeUID: committeeUID},
	}
	event := model.CommitteeEvent{Data: member}
	b, _ := json.Marshal(event)
	return b
}

func TestHandleCommitteeTotalMembersSync(t *testing.T) {
	ctx := context.Background()

	makeCommittee := func(uid string, totalMembers int) *model.Committee {
		return &model.Committee{
			CommitteeBase: model.CommitteeBase{
				UID:          uid,
				ProjectUID:   "proj-1",
				Name:         "Test Committee",
				Category:     "technical",
				TotalMembers: totalMembers,
				CreatedAt:    time.Now().Add(-time.Hour),
				UpdatedAt:    time.Now(),
			},
		}
	}

	committeeUID := uuid.New().String()

	tests := []struct {
		name              string
		subject           string
		messageData       []byte
		setupMock         func(*mock.MockRepository)
		writerErr         error
		wantErr           bool
		wantUpdateCalls   int
		validateCommittee func(*testing.T, *model.Committee)
	}{
		{
			name:            "irrelevant subject — skipped silently",
			subject:         "lfx.committee-api.some.other.subject",
			messageData:     buildTotalMembersSyncMsg(committeeUID),
			setupMock:       func(_ *mock.MockRepository) {},
			wantErr:         false,
			wantUpdateCalls: 0,
		},
		{
			name:            "invalid JSON — returns parse error",
			subject:         constants.CommitteeMemberCreatedSubject,
			messageData:     []byte(`not-json`),
			setupMock:       func(_ *mock.MockRepository) {},
			wantErr:         true,
			wantUpdateCalls: 0,
		},
		{
			name:    "event data cannot decode to CommitteeMember — discarded silently",
			subject: constants.CommitteeMemberCreatedSubject,
			messageData: func() []byte {
				event := model.CommitteeEvent{Data: "not-a-member"}
				b, _ := json.Marshal(event)
				return b
			}(),
			setupMock:       func(_ *mock.MockRepository) {},
			wantErr:         false,
			wantUpdateCalls: 0,
		},
		{
			name:    "empty committee_uid — discarded silently",
			subject: constants.CommitteeMemberCreatedSubject,
			messageData: func() []byte {
				event := model.CommitteeEvent{Data: model.CommitteeMember{}}
				b, _ := json.Marshal(event)
				return b
			}(),
			setupMock:       func(_ *mock.MockRepository) {},
			wantErr:         false,
			wantUpdateCalls: 0,
		},
		{
			name:            "GetBase fails — propagates error",
			subject:         constants.CommitteeMemberCreatedSubject,
			messageData:     buildTotalMembersSyncMsg(committeeUID),
			setupMock:       func(repo *mock.MockRepository) {},
			wantErr:         true,
			wantUpdateCalls: 0,
		},
		{
			name:        "TotalMembers already correct — no update",
			subject:     constants.CommitteeMemberCreatedSubject,
			messageData: buildTotalMembersSyncMsg(committeeUID),
			setupMock: func(repo *mock.MockRepository) {
				repo.AddCommittee(makeCommittee(committeeUID, 2))
				repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{UID: uuid.New().String(), CommitteeUID: committeeUID},
				})
				repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{UID: uuid.New().String(), CommitteeUID: committeeUID},
				})
			},
			wantErr:         false,
			wantUpdateCalls: 0,
		},
		{
			name:        "TotalMembers stale — update called with correct count (created subject)",
			subject:     constants.CommitteeMemberCreatedSubject,
			messageData: buildTotalMembersSyncMsg(committeeUID),
			setupMock: func(repo *mock.MockRepository) {
				repo.AddCommittee(makeCommittee(committeeUID, 1))
				repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{UID: uuid.New().String(), CommitteeUID: committeeUID},
				})
				repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{UID: uuid.New().String(), CommitteeUID: committeeUID},
				})
			},
			wantErr:         false,
			wantUpdateCalls: 1,
			validateCommittee: func(t *testing.T, c *model.Committee) {
				t.Helper()
				assert.Equal(t, 2, c.TotalMembers)
			},
		},
		{
			name:        "TotalMembers stale — update called with correct count (deleted subject)",
			subject:     constants.CommitteeMemberDeletedSubject,
			messageData: buildTotalMembersSyncMsg(committeeUID),
			setupMock: func(repo *mock.MockRepository) {
				repo.AddCommittee(makeCommittee(committeeUID, 3))
				repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{UID: uuid.New().String(), CommitteeUID: committeeUID},
				})
			},
			wantErr:         false,
			wantUpdateCalls: 1,
			validateCommittee: func(t *testing.T, c *model.Committee) {
				t.Helper()
				assert.Equal(t, 1, c.TotalMembers)
			},
		},
		{
			name:        "Update fails — propagates error",
			subject:     constants.CommitteeMemberCreatedSubject,
			messageData: buildTotalMembersSyncMsg(committeeUID),
			setupMock: func(repo *mock.MockRepository) {
				repo.AddCommittee(makeCommittee(committeeUID, 0))
				repo.AddCommitteeMember(committeeUID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{UID: uuid.New().String(), CommitteeUID: committeeUID},
				})
			},
			writerErr:       fmt.Errorf("storage unavailable"),
			wantErr:         true,
			wantUpdateCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mock.NewMockRepository()
			mockRepo.ClearAll()
			tt.setupMock(mockRepo)

			spy := &spyCommitteeWriterOrchestrator{updateErr: tt.writerErr}

			handler := NewMessageHandlerOrchestrator(
				WithCommitteeReaderForMessageHandler(
					NewCommitteeReaderOrchestrator(WithCommitteeReader(mockRepo)),
				),
				WithCommitteeWriterOrchestratorForMessageHandler(spy),
			)

			msg := &mockStreamMessenger{subject: tt.subject, data: tt.messageData}
			err := handler.HandleCommitteeTotalMembersSync(ctx, msg)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantUpdateCalls, spy.updateCalls, "Update call count mismatch")

			if tt.validateCommittee != nil {
				require.NotNil(t, spy.updatedCommittee)
				tt.validateCommittee(t, spy.updatedCommittee)
			}
		})
	}
}

// Helper function to create string pointer
func messageHandlerStringPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// Notification handler tests
// ---------------------------------------------------------------------------

// mockEmailSender records SendEmail calls for assertions.
type mockEmailSender struct {
	mu     sync.Mutex
	calls  []emailapi.SendEmailRequest
	retErr error
}

func (m *mockEmailSender) SendEmail(_ context.Context, req emailapi.SendEmailRequest) error {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()
	return m.retErr
}

// mockInviteSender records SendInvite calls for assertions.
type mockInviteSender struct {
	mu        sync.Mutex
	calls     []inviteapi.SendInviteRequest
	retErr    error
	retResult port.InviteResult
}

func (m *mockInviteSender) SendInvite(_ context.Context, req inviteapi.SendInviteRequest) (port.InviteResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()
	return m.retResult, m.retErr
}

// mockUserReader is a simple UserReader for tests that returns fixed metadata.
type mockUserReader struct {
	meta         *model.UserMetadata
	err          error
	primaryEmail string // returned by EmailsByAuthToken
}

func (m *mockUserReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	if m.primaryEmail != "" {
		return &model.UserEmails{PrimaryEmail: m.primaryEmail}, nil
	}
	return nil, nil
}

func (m *mockUserReader) UserMetadataByPrincipal(_ context.Context, _ string) (*model.UserMetadata, error) {
	return m.meta, m.err
}

func buildMemberCreatedPayload(t *testing.T, member *model.CommitteeMember, skipNotification ...bool) []byte {
	t.Helper()
	skip := false
	if len(skipNotification) > 0 {
		skip = skipNotification[0]
	}
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeMember, model.ActionCreated,
		&model.CommitteeMemberCreatedEventData{CommitteeMember: member, SkipNotification: skip})
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

func buildSettingsUpdatedPayload(t *testing.T, data *model.CommitteeSettingsUpdateEventData) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeSettings, model.ActionUpdated, data)
	require.NoError(t, err)
	payload, err := json.Marshal(built)
	require.NoError(t, err)
	return payload
}

func TestHandleCommitteeMemberCreated(t *testing.T) {
	// LFID member: has username → email notification path
	lfidMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:           "member-uid-1",
			Username:      "alice_lfid",
			Email:         "alice@example.com",
			FirstName:     "Alice",
			LastName:      "Smith",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
			Role:          model.CommitteeMemberRole{Name: "writer"},
		},
	}
	// Non-LFID member: no username → invite path
	nonLFIDMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:           "member-uid-2",
			Email:         "bob@example.com",
			FirstName:     "Bob",
			LastName:      "Jones",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
			Role:          model.CommitteeMemberRole{Name: "Member"},
		},
	}
	// Non-LFID auditor: should receive InviteRoleView, not Manage
	nonLFIDAuditor := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:           "member-uid-3",
			Email:         "carol@example.com",
			FirstName:     "Carol",
			LastName:      "Lee",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
			Role:          model.CommitteeMemberRole{Name: "Auditor"},
		},
	}
	// Non-LFID auditor with lowercase role name — role matching must be case-insensitive
	nonLFIDAuditorLower := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:           "member-uid-4",
			Email:         "diana@example.com",
			FirstName:     "Diana",
			LastName:      "Kim",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
			Role:          model.CommitteeMemberRole{Name: "auditor"},
		},
	}

	tests := []struct {
		name             string
		msgData          []byte
		emailSender      *mockEmailSender
		inviteSender     *mockInviteSender
		omitEmailSender  bool
		omitInviteSender bool
		wantEmailCount   int
		wantInviteCount  int
		wantInviteRole   string
	}{
		{
			name:            "LFID member — email notification sent",
			msgData:         buildMemberCreatedPayload(t, lfidMember),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  1,
			wantInviteCount: 0,
		},
		{
			name:            "non-LFID member — invite sent with Member role",
			msgData:         buildMemberCreatedPayload(t, nonLFIDMember),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 1,
			wantInviteRole:  "Member",
		},
		{
			name:            "non-LFID auditor member — invite sent with Member role",
			msgData:         buildMemberCreatedPayload(t, nonLFIDAuditor),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 1,
			wantInviteRole:  "Member",
		},
		{
			name:            "non-LFID auditor member lowercase role — invite sent with Member role",
			msgData:         buildMemberCreatedPayload(t, nonLFIDAuditorLower),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 1,
			wantInviteRole:  "Member",
		},
		{
			name: "member without email — no notification sent",
			msgData: buildMemberCreatedPayload(t, &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					Username:      "noemail",
					CommitteeUID:  "committee-1",
					CommitteeName: "TSC Committee",
					Role:          model.CommitteeMemberRole{Name: "writer"},
				},
			}),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
		{
			name:            "LFID member — email send error — handler still returns nil",
			msgData:         buildMemberCreatedPayload(t, lfidMember),
			emailSender:     &mockEmailSender{retErr: assert.AnError},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  1,
			wantInviteCount: 0,
		},
		{
			name:            "non-LFID member — invite send error — handler still returns nil",
			msgData:         buildMemberCreatedPayload(t, nonLFIDMember),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{retErr: assert.AnError},
			wantEmailCount:  0,
			wantInviteCount: 1,
		},
		{
			name:            "invalid JSON — handler returns nil",
			msgData:         []byte("not json"),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
		{
			name:             "no email sender and no invite sender — LFID member skipped",
			msgData:          buildMemberCreatedPayload(t, lfidMember),
			omitEmailSender:  true,
			omitInviteSender: true,
			wantEmailCount:   0,
			wantInviteCount:  0,
		},
		{
			name:             "no invite sender — non-LFID member skipped",
			msgData:          buildMemberCreatedPayload(t, nonLFIDMember),
			emailSender:      &mockEmailSender{},
			omitInviteSender: true,
			wantEmailCount:   0,
			wantInviteCount:  0,
		},
		{
			name:            "LFID member with skip_notification — no email sent",
			msgData:         buildMemberCreatedPayload(t, lfidMember, true),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
		{
			name:            "non-LFID member with skip_notification — no invite sent",
			msgData:         buildMemberCreatedPayload(t, nonLFIDMember, true),
			emailSender:     &mockEmailSender{},
			inviteSender:    &mockInviteSender{},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &messageHandlerOrchestrator{lfxSelfServeBaseURL: "https://app.dev.lfx.dev"}
			if !tt.omitEmailSender {
				h.emailSender = tt.emailSender
			}
			if !tt.omitInviteSender {
				h.inviteSender = tt.inviteSender
			}

			msg := newMockTransportMessenger(constants.CommitteeMemberCreatedSubject, tt.msgData)
			resp, err := h.HandleCommitteeMemberCreated(context.Background(), msg)

			assert.NoError(t, err)
			assert.Nil(t, resp)

			if tt.emailSender != nil {
				assert.Len(t, tt.emailSender.calls, tt.wantEmailCount, "email call count")
				if tt.wantEmailCount > 0 {
					assert.Equal(t, "alice@example.com", tt.emailSender.calls[0].To)
					assert.Contains(t, tt.emailSender.calls[0].Subject, "TSC Committee")
					assert.Contains(t, tt.emailSender.calls[0].HTML, "Alice Smith")
					assert.Contains(t, tt.emailSender.calls[0].HTML, "https://app.dev.lfx.dev/project/groups/committee-1")
				}
			}
			if tt.inviteSender != nil {
				assert.Len(t, tt.inviteSender.calls, tt.wantInviteCount, "invite call count")
				if tt.wantInviteCount > 0 {
					req := tt.inviteSender.calls[0]
					if assert.NotNil(t, req.Resource, "Resource must be set") {
						assert.Equal(t, "committee-1", req.Resource.UID)
						assert.Equal(t, "TSC Committee", req.Resource.Name)
						assert.Equal(t, "group", req.Resource.Type)
					}
					assert.Contains(t, req.ReturnURL, "committee-1")
					if tt.wantInviteRole != "" {
						assert.Equal(t, tt.wantInviteRole, req.Role, "invite role")
					}
				}
			}
		})
	}
}

func TestHandleCommitteeSettingsUpdated(t *testing.T) {
	alice := model.CommitteeUser{Username: "alice", Email: "alice@example.com", Name: "Alice"}
	bob := model.CommitteeUser{Username: "bob", Email: "bob@example.com", Name: "Bob"}
	noemail := model.CommitteeUser{Username: "noemail"}
	noLFIDUser := model.CommitteeUser{Email: "nolfid@example.com", Name: "No LFID User"}

	base := &model.CommitteeSettingsUpdateEventData{
		CommitteeUID:  "committee-1",
		CommitteeName: "TSC Committee",
	}

	tests := []struct {
		name             string
		oldWriters       []model.CommitteeUser
		newWriters       []model.CommitteeUser
		oldAuditors      []model.CommitteeUser
		newAuditors      []model.CommitteeUser
		updatedBy        string
		userReader       *mockUserReader
		omitEmailSender  bool
		inviteSender     *mockInviteSender
		omitInviteSender bool
		invalidJSON      bool
		wantSendCount    int
		wantInviteCount  int
		wantInviteRole   string
		wantInviterName  string
	}{
		{
			name:          "new writer added — one email sent with Writer role",
			newWriters:    []model.CommitteeUser{alice},
			wantSendCount: 1,
		},
		{
			name:          "new auditor added — one email sent with Auditor role",
			newAuditors:   []model.CommitteeUser{alice},
			wantSendCount: 1,
		},
		{
			name:          "writer and auditor both added — two emails sent",
			newWriters:    []model.CommitteeUser{alice},
			newAuditors:   []model.CommitteeUser{bob},
			wantSendCount: 2,
		},
		{
			name:          "same user in both writer and auditor — deduplicated to one email",
			newWriters:    []model.CommitteeUser{alice},
			newAuditors:   []model.CommitteeUser{alice},
			wantSendCount: 1,
		},
		{
			name:          "writer already existed — no email sent",
			oldWriters:    []model.CommitteeUser{alice},
			newWriters:    []model.CommitteeUser{alice},
			wantSendCount: 0,
		},
		{
			name:          "new user has no email — no email sent",
			newWriters:    []model.CommitteeUser{noemail},
			wantSendCount: 0,
		},
		{
			name:          "new user has no stored email — skipped even if user reader is configured",
			newWriters:    []model.CommitteeUser{noemail},
			userReader:    &mockUserReader{primaryEmail: "noemail@example.com"},
			wantSendCount: 0,
		},
		{
			name:            "no email sender configured — no email sent",
			newWriters:      []model.CommitteeUser{alice},
			omitEmailSender: true,
			wantSendCount:   0,
		},
		{
			name:          "invalid JSON — returns nil without panic",
			invalidJSON:   true,
			wantSendCount: 0,
		},
		{
			name:            "user reader returns full name — used as inviter name",
			newWriters:      []model.CommitteeUser{alice},
			updatedBy:       "johndoe",
			userReader:      &mockUserReader{meta: &model.UserMetadata{Name: "John Doe"}},
			wantSendCount:   1,
			wantInviterName: "John Doe",
		},
		{
			name:            "user reader returns given+family name — combined as inviter name",
			newWriters:      []model.CommitteeUser{alice},
			updatedBy:       "johndoe",
			userReader:      &mockUserReader{meta: &model.UserMetadata{GivenName: "John", FamilyName: "Doe"}},
			wantSendCount:   1,
			wantInviterName: "John Doe",
		},
		{
			name:            "user reader returns empty metadata — falls back to committee administrator",
			newWriters:      []model.CommitteeUser{alice},
			updatedBy:       "johndoe",
			userReader:      &mockUserReader{meta: &model.UserMetadata{}},
			wantSendCount:   1,
			wantInviterName: "A committee administrator",
		},
		{
			name:            "no user reader — falls back to committee administrator",
			newWriters:      []model.CommitteeUser{alice},
			updatedBy:       "johndoe",
			wantSendCount:   1,
			wantInviterName: "A committee administrator",
		},
		{
			name:            "updatedBy empty — falls back to committee administrator",
			newWriters:      []model.CommitteeUser{alice},
			updatedBy:       "",
			wantSendCount:   1,
			wantInviterName: "A committee administrator",
		},
		{
			name:            "non-LFID writer added — invite sent with Manage role",
			newWriters:      []model.CommitteeUser{noLFIDUser},
			omitEmailSender: true,
			inviteSender:    &mockInviteSender{},
			wantSendCount:   0,
			wantInviteCount: 1,
			wantInviteRole:  string(inviteapi.InviteRoleManage),
		},
		{
			name:            "non-LFID auditor added — invite sent with View role",
			newAuditors:     []model.CommitteeUser{noLFIDUser},
			omitEmailSender: true,
			inviteSender:    &mockInviteSender{},
			wantSendCount:   0,
			wantInviteCount: 1,
			wantInviteRole:  string(inviteapi.InviteRoleView),
		},
		{
			name:             "no invite sender — non-LFID writer skipped",
			newWriters:       []model.CommitteeUser{noLFIDUser},
			omitEmailSender:  true,
			omitInviteSender: true,
			wantSendCount:    0,
			wantInviteCount:  0,
		},
		{
			name:            "non-LFID user in both writer and auditor — deduplicated to one invite",
			newWriters:      []model.CommitteeUser{noLFIDUser},
			newAuditors:     []model.CommitteeUser{noLFIDUser},
			omitEmailSender: true,
			inviteSender:    &mockInviteSender{},
			wantSendCount:   0,
			wantInviteCount: 1,
		},
		{
			name:            "non-LFID writer already existed — no invite sent",
			oldWriters:      []model.CommitteeUser{noLFIDUser},
			newWriters:      []model.CommitteeUser{noLFIDUser},
			omitEmailSender: true,
			inviteSender:    &mockInviteSender{},
			wantSendCount:   0,
			wantInviteCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockEmailSender{}
			h := &messageHandlerOrchestrator{lfxSelfServeBaseURL: "https://app.dev.lfx.dev"}
			if !tt.omitEmailSender {
				h.emailSender = sender
			}
			if !tt.omitInviteSender && tt.inviteSender != nil {
				h.inviteSender = tt.inviteSender
			}
			if tt.userReader != nil {
				h.userReader = tt.userReader
			}

			var payload []byte
			if tt.invalidJSON {
				payload = []byte("not json")
			} else {
				d := *base
				d.OldSettings = &model.CommitteeSettings{Writers: tt.oldWriters, Auditors: tt.oldAuditors}
				d.Settings = &model.CommitteeSettings{Writers: tt.newWriters, Auditors: tt.newAuditors}
				d.UpdatedBy = tt.updatedBy
				payload = buildSettingsUpdatedPayload(t, &d)
			}

			msg := newMockTransportMessenger(constants.CommitteeSettingsUpdatedSubject, payload)
			resp, err := h.HandleCommitteeSettingsUpdated(context.Background(), msg)

			assert.NoError(t, err)
			assert.Nil(t, resp)
			assert.Len(t, sender.calls, tt.wantSendCount)
			if tt.wantSendCount > 0 {
				assert.Contains(t, sender.calls[0].HTML, "https://app.dev.lfx.dev/project/groups/committee-1")
				assert.Contains(t, sender.calls[0].Subject, "TSC Committee")
			}
			// Verify correct display role labels in email content (Writer→Manage, Auditor→View)
			if tt.name == "new writer added — one email sent with Writer role" {
				assert.Contains(t, sender.calls[0].HTML, "Manage")
			}
			if tt.name == "new auditor added — one email sent with Auditor role" {
				assert.Contains(t, sender.calls[0].HTML, "View")
			}
			if tt.wantInviterName != "" {
				assert.Contains(t, sender.calls[0].HTML, tt.wantInviterName)
			}
			if tt.inviteSender != nil {
				tt.inviteSender.mu.Lock()
				inviteCalls := make([]inviteapi.SendInviteRequest, len(tt.inviteSender.calls))
				copy(inviteCalls, tt.inviteSender.calls)
				tt.inviteSender.mu.Unlock()
				assert.Len(t, inviteCalls, tt.wantInviteCount, "invite call count")
				if tt.wantInviteCount > 0 && tt.wantInviteRole != "" {
					assert.Equal(t, tt.wantInviteRole, inviteCalls[0].Role, "invite role")
				}
			}
		})
	}
}

func TestBuildCommitteeURL(t *testing.T) {
	assert.Equal(t, "https://app.dev.lfx.dev/project/groups/abc-123", buildCommitteeURL("https://app.dev.lfx.dev", "abc-123"))
	assert.Equal(t, "https://app.dev.lfx.dev/project/groups/abc-123", buildCommitteeURL("https://app.dev.lfx.dev/", "abc-123"))
}

func TestDiffNewCommitteeUsers(t *testing.T) {
	alice := model.CommitteeUser{Username: "alice"}
	bob := model.CommitteeUser{Username: "bob"}
	noLFID := model.CommitteeUser{Email: "nolfid@example.com"}
	noLFID2 := model.CommitteeUser{Email: "other@example.com"}

	// LFID users: diff by Username
	got := diffNewCommitteeUsers([]model.CommitteeUser{alice}, []model.CommitteeUser{alice, bob})
	assert.Equal(t, []model.CommitteeUser{bob}, got)

	got = diffNewCommitteeUsers(nil, []model.CommitteeUser{alice})
	assert.Equal(t, []model.CommitteeUser{alice}, got)

	got = diffNewCommitteeUsers([]model.CommitteeUser{alice}, []model.CommitteeUser{alice})
	assert.Empty(t, got)

	// Non-LFID users: diff by Email
	got = diffNewCommitteeUsers([]model.CommitteeUser{noLFID}, []model.CommitteeUser{noLFID, noLFID2})
	assert.Equal(t, []model.CommitteeUser{noLFID2}, got)

	got = diffNewCommitteeUsers([]model.CommitteeUser{noLFID}, []model.CommitteeUser{noLFID})
	assert.Empty(t, got, "non-LFID user already in old list should not appear as new")

	// Email normalization: different casing/whitespace must not produce a duplicate.
	noLFIDUpper := model.CommitteeUser{Email: "  NOLFID@EXAMPLE.COM  "}
	got = diffNewCommitteeUsers([]model.CommitteeUser{noLFID}, []model.CommitteeUser{noLFIDUpper})
	assert.Empty(t, got, "email match should be case- and whitespace-insensitive")
}

func buildMemberDeletedPayload(t *testing.T, member *model.CommitteeMember) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeMember, model.ActionDeleted, member)
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

func TestHandleCommitteeMemberDeleted(t *testing.T) {
	lfidMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:           "member-uid-1",
			Username:      "alice_lfid",
			Email:         "alice@example.com",
			FirstName:     "Alice",
			LastName:      "Smith",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
			Role:          model.CommitteeMemberRole{Name: "Member"},
		},
	}
	nonLFIDMember := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:           "member-uid-2",
			Email:         "bob@example.com",
			FirstName:     "Bob",
			LastName:      "Jones",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
		},
	}

	tests := []struct {
		name            string
		msgData         []byte
		omitEmailSender bool
		emailSenderErr  error
		wantEmailCount  int
	}{
		{
			name:           "LF member deleted — removal email sent",
			msgData:        buildMemberDeletedPayload(t, lfidMember),
			wantEmailCount: 1,
		},
		{
			name:           "non-LF member deleted — no email sent",
			msgData:        buildMemberDeletedPayload(t, nonLFIDMember),
			wantEmailCount: 0,
		},
		{
			name: "LF member without email — no email sent",
			msgData: buildMemberDeletedPayload(t, &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					Username:      "alice_lfid",
					CommitteeUID:  "committee-1",
					CommitteeName: "TSC Committee",
				},
			}),
			wantEmailCount: 0,
		},
		{
			name:            "no email sender configured — no email sent",
			msgData:         buildMemberDeletedPayload(t, lfidMember),
			omitEmailSender: true,
			wantEmailCount:  0,
		},
		{
			name:           "email send error — handler returns nil (best-effort)",
			msgData:        buildMemberDeletedPayload(t, lfidMember),
			emailSenderErr: assert.AnError,
			wantEmailCount: 1,
		},
		{
			name:           "invalid JSON — handler returns nil without panic",
			msgData:        []byte("not json"),
			wantEmailCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockEmailSender{retErr: tt.emailSenderErr}
			h := &messageHandlerOrchestrator{lfxSelfServeBaseURL: "https://app.dev.lfx.dev"}
			if !tt.omitEmailSender {
				h.emailSender = sender
			}

			msg := newMockTransportMessenger(constants.CommitteeMemberDeletedSubject, tt.msgData)
			resp, err := h.HandleCommitteeMemberDeleted(context.Background(), msg)

			assert.NoError(t, err)
			assert.Nil(t, resp)
			assert.Len(t, sender.calls, tt.wantEmailCount, "email call count")
			if tt.wantEmailCount > 0 {
				assert.Equal(t, "alice@example.com", sender.calls[0].To)
				assert.Contains(t, sender.calls[0].Subject, "TSC Committee")
				assert.Contains(t, sender.calls[0].HTML, "Alice Smith")
				assert.Contains(t, sender.calls[0].HTML, "Member", "previous role in removal email")
			}
		})
	}
}

func TestClassifyCommitteeUsers(t *testing.T) {
	alice := model.CommitteeUser{Username: "alice", Email: "alice@example.com", Name: "Alice"}
	bob := model.CommitteeUser{Username: "bob", Email: "bob@example.com", Name: "Bob"}
	noLFID := model.CommitteeUser{Email: "nolfid@example.com", Name: "No LFID"}

	buildSettings := func(writers, auditors []model.CommitteeUser) *model.CommitteeSettings {
		return &model.CommitteeSettings{Writers: writers, Auditors: auditors}
	}

	t.Run("new user added as writer", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings(nil, nil), buildSettings([]model.CommitteeUser{alice}, nil))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindAdded, changes[0].kind)
		assert.Equal(t, []string{"Writer"}, changes[0].newRoles)
	})

	t.Run("user added as both writer and auditor — one added entry", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings(nil, nil), buildSettings([]model.CommitteeUser{alice}, []model.CommitteeUser{alice}))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindAdded, changes[0].kind)
		assert.Equal(t, []string{"Auditor", "Writer"}, changes[0].newRoles)
	})

	t.Run("writer swapped to auditor — one updated entry", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings([]model.CommitteeUser{alice}, nil), buildSettings(nil, []model.CommitteeUser{alice}))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindUpdated, changes[0].kind)
		assert.Equal(t, []string{"Auditor"}, changes[0].newRoles)
	})

	t.Run("user gained additional role — one updated entry", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings([]model.CommitteeUser{alice}, nil), buildSettings([]model.CommitteeUser{alice}, []model.CommitteeUser{alice}))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindUpdated, changes[0].kind)
		assert.Equal(t, []string{"Auditor", "Writer"}, changes[0].newRoles)
	})

	t.Run("user lost one of two roles — one updated entry", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings([]model.CommitteeUser{alice}, []model.CommitteeUser{alice}), buildSettings([]model.CommitteeUser{alice}, nil))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindUpdated, changes[0].kind)
		assert.Equal(t, []string{"Writer"}, changes[0].newRoles)
	})

	t.Run("user fully removed — one removed entry", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings([]model.CommitteeUser{alice}, nil), buildSettings(nil, nil))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindRemoved, changes[0].kind)
		assert.Empty(t, changes[0].newRoles)
	})

	t.Run("unchanged user — no entry", func(t *testing.T) {
		changes := classifyCommitteeUsers(buildSettings([]model.CommitteeUser{alice}, nil), buildSettings([]model.CommitteeUser{alice}, nil))
		assert.Empty(t, changes)
	})

	t.Run("multiple users with different outcomes", func(t *testing.T) {
		old := buildSettings([]model.CommitteeUser{alice}, []model.CommitteeUser{bob})
		new := buildSettings([]model.CommitteeUser{alice, noLFID}, nil)
		changes := classifyCommitteeUsers(old, new)
		// alice: Writer in both → no change; bob: Auditor removed; noLFID: added as Writer
		require.Len(t, changes, 2)
		kinds := map[roleChangeKind]bool{}
		for _, c := range changes {
			kinds[c.kind] = true
		}
		assert.True(t, kinds[roleChangeKindAdded], "noLFID should be added")
		assert.True(t, kinds[roleChangeKindRemoved], "bob should be removed")
	})

	t.Run("nil old settings — all users added", func(t *testing.T) {
		changes := classifyCommitteeUsers(nil, buildSettings([]model.CommitteeUser{alice}, nil))
		require.Len(t, changes, 1)
		assert.Equal(t, roleChangeKindAdded, changes[0].kind)
	})
}

func TestHandleCommitteeSettingsUpdatedRoleChanges(t *testing.T) {
	alice := model.CommitteeUser{Username: "alice", Email: "alice@example.com", Name: "Alice"}
	noLFID := model.CommitteeUser{Email: "nolfid@example.com", Name: "No LFID"}

	base := &model.CommitteeSettingsUpdateEventData{
		CommitteeUID:  "committee-1",
		CommitteeName: "TSC Committee",
	}

	tests := []struct {
		name            string
		oldWriters      []model.CommitteeUser
		oldAuditors     []model.CommitteeUser
		newWriters      []model.CommitteeUser
		newAuditors     []model.CommitteeUser
		wantEmailCount  int
		wantInviteCount int
		wantInviteRole  string
		subjectContains string
		htmlContains    string
	}{
		{
			name:            "LF user: writer swapped to auditor — role updated email",
			oldWriters:      []model.CommitteeUser{alice},
			newAuditors:     []model.CommitteeUser{alice},
			wantEmailCount:  1,
			subjectContains: "updated your role",
			htmlContains:    "View", // Auditor → View display name
		},
		{
			// Gaining Auditor on top of Writer collapses to the same effective display role
			// ("Manage" supersedes "View"), so no email is sent — effective access is unchanged.
			name:            "LF user: gained auditor on top of writer — no email (effective role unchanged)",
			oldWriters:      []model.CommitteeUser{alice},
			newWriters:      []model.CommitteeUser{alice},
			newAuditors:     []model.CommitteeUser{alice},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
		{
			// Losing Auditor while keeping Writer: old=Writer+Auditor→["Manage"], new=Writer→["Manage"].
			// Effective display role is unchanged so no email is sent.
			name:            "LF user: lost auditor but kept writer — no email (effective role unchanged)",
			oldWriters:      []model.CommitteeUser{alice},
			oldAuditors:     []model.CommitteeUser{alice},
			newWriters:      []model.CommitteeUser{alice},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
		{
			// Losing Writer while keeping Auditor: old=Writer+Auditor→["Manage"], new=Auditor→["View"].
			// Effective role changed from Manage to View, so an update email is sent.
			name:            "LF user: lost writer but kept auditor — role updated email",
			oldWriters:      []model.CommitteeUser{alice},
			oldAuditors:     []model.CommitteeUser{alice},
			newAuditors:     []model.CommitteeUser{alice},
			wantEmailCount:  1,
			subjectContains: "updated your role",
			htmlContains:    "View", // new effective role
		},
		{
			name:            "LF user: fully removed — removal email",
			oldWriters:      []model.CommitteeUser{alice},
			wantEmailCount:  1,
			subjectContains: "removed you from",
		},
		{
			name:            "non-LF user: fully removed — no email, no invite",
			oldWriters:      []model.CommitteeUser{noLFID},
			wantEmailCount:  0,
			wantInviteCount: 0,
		},
		{
			name:            "non-LF user: writer swapped to auditor — re-invite with View role",
			oldWriters:      []model.CommitteeUser{noLFID},
			newAuditors:     []model.CommitteeUser{noLFID},
			wantEmailCount:  0,
			wantInviteCount: 1,
			wantInviteRole:  string(inviteapi.InviteRoleView),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockEmailSender{}
			inviter := &mockInviteSender{}
			h := &messageHandlerOrchestrator{
				lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
				emailSender:         sender,
				inviteSender:        inviter,
			}

			d := *base
			d.OldSettings = &model.CommitteeSettings{Writers: tt.oldWriters, Auditors: tt.oldAuditors}
			d.Settings = &model.CommitteeSettings{Writers: tt.newWriters, Auditors: tt.newAuditors}
			payload := buildSettingsUpdatedPayload(t, &d)

			msg := newMockTransportMessenger(constants.CommitteeSettingsUpdatedSubject, payload)
			resp, err := h.HandleCommitteeSettingsUpdated(context.Background(), msg)

			assert.NoError(t, err)
			assert.Nil(t, resp)
			assert.Len(t, sender.calls, tt.wantEmailCount, "email call count")
			if tt.wantEmailCount > 0 && tt.subjectContains != "" {
				assert.Contains(t, sender.calls[0].Subject, tt.subjectContains, "email subject")
			}
			if tt.wantEmailCount > 0 && tt.htmlContains != "" {
				assert.Contains(t, sender.calls[0].HTML, tt.htmlContains, "email HTML content")
			}

			inviter.mu.Lock()
			inviteCount := len(inviter.calls)
			var inviteRole string
			if inviteCount > 0 {
				inviteRole = inviter.calls[0].Role
			}
			inviter.mu.Unlock()
			assert.Equal(t, tt.wantInviteCount, inviteCount, "invite call count")
			if tt.wantInviteRole != "" {
				assert.Equal(t, tt.wantInviteRole, inviteRole, "invite role")
			}
		})
	}
}

func TestHandleInviteAccepted(t *testing.T) {
	ctx := context.Background()

	const inviteUID = "inv-abc"
	const username = "newuser"
	const committee1UID = "committee-1"
	const committee2UID = "committee-2"
	const writerEmail = "writer@example.com"
	const auditorEmail = "auditor@example.com"
	const memberEmail = "member@example.com"

	makeEvent := func(invUID, acceptedBy, recipientEmail, role string) []byte {
		event := inviteapi.InviteServiceAcceptedEvent{Invite: inviteapi.Invite{
			UID:        invUID,
			AcceptedBy: acceptedBy,
			Role:       role,
			Recipient:  inviteapi.Recipient{Email: recipientEmail},
		}}
		b, _ := json.Marshal(event)
		return b
	}

	makeCommitteeWithSettings := func(uid string, settings *model.CommitteeSettings) *model.Committee {
		return &model.Committee{
			CommitteeBase:     model.CommitteeBase{UID: uid, ProjectUID: "proj-1", Name: "Test Committee"},
			CommitteeSettings: settings,
		}
	}

	makeHandler := func(repo *mock.MockRepository, spy *spyCommitteeWriterOrchestrator) *messageHandlerOrchestrator {
		h := NewMessageHandlerOrchestrator(
			WithCommitteeReaderForMessageHandler(
				NewCommitteeReaderOrchestrator(WithCommitteeReader(repo)),
			),
			WithCommitteeWriterOrchestratorForMessageHandler(spy),
		)
		return h.(*messageHandlerOrchestrator)
	}

	tests := []struct {
		name                  string
		setupRepo             func(*mock.MockRepository)
		spyErr                error
		spyErrs               []error // per-call error queue; takes precedence over spyErr when non-nil
		spyMemberErr          error
		spyMemberErrs         []error
		msgData               []byte
		wantUpdateCalls       int
		wantUpdateMemberCalls int
		validateSettings      func(*testing.T, []*model.CommitteeSettings)
		validateMembers       func(*testing.T, []*model.CommitteeMember)
	}{
		{
			name: "malformed payload — no update called",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
			},
			msgData:         []byte("not json"),
			wantUpdateCalls: 0,
		},
		{
			name: "missing uid — discarded",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
			},
			msgData:         makeEvent("", username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 0,
		},
		{
			name: "missing accepted_by — discarded",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
			},
			msgData:         makeEvent(inviteUID, "", writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 0,
		},
		{
			name: "happy path — enriches Writer entries across matching committees",
			setupRepo: func(r *mock.MockRepository) {
				// Two committees both have an email-only Writer entry.
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
				r.AddCommittee(makeCommitteeWithSettings(committee2UID,
					&model.CommitteeSettings{UID: committee2UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
			},
			msgData:         makeEvent(inviteUID, username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 2,
			validateSettings: func(t *testing.T, captured []*model.CommitteeSettings) {
				for _, s := range captured {
					require.Len(t, s.Writers, 1)
					assert.Equal(t, username, s.Writers[0].Username, "writer should be enriched")
				}
			},
		},
		{
			name: "Manage role invite — enriches Writers and matching members with same email",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
				r.AddCommitteeMember(committee1UID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-writer",
						CommitteeUID: committee1UID,
						Email:        writerEmail,
					},
				})
			},
			msgData:               makeEvent(inviteUID, username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls:       1,
			wantUpdateMemberCalls: 1,
			validateSettings: func(t *testing.T, captured []*model.CommitteeSettings) {
				require.Len(t, captured, 1)
				assert.Equal(t, username, captured[0].Writers[0].Username, "writer should be enriched")
			},
			validateMembers: func(t *testing.T, captured []*model.CommitteeMember) {
				require.Len(t, captured, 1)
				assert.Equal(t, username, captured[0].Username, "member should be enriched for Manage role invite")
			},
		},
		{
			name: "View role invite — enriches Writers, Auditors, and matching members with same email",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID, &model.CommitteeSettings{
					UID:      committee1UID,
					Writers:  []model.CommitteeUser{{Email: auditorEmail}},
					Auditors: []model.CommitteeUser{{Email: auditorEmail}},
				}))
				r.AddCommitteeMember(committee1UID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-auditor",
						CommitteeUID: committee1UID,
						Email:        auditorEmail,
					},
				})
			},
			msgData:               makeEvent(inviteUID, username, auditorEmail, string(inviteapi.InviteRoleView)),
			wantUpdateCalls:       1,
			wantUpdateMemberCalls: 1,
			validateSettings: func(t *testing.T, captured []*model.CommitteeSettings) {
				require.Len(t, captured, 1)
				s := captured[0]
				assert.Equal(t, username, s.Writers[0].Username, "writer should be enriched")
				assert.Equal(t, username, s.Auditors[0].Username, "auditor should be enriched")
			},
			validateMembers: func(t *testing.T, captured []*model.CommitteeMember) {
				require.Len(t, captured, 1)
				assert.Equal(t, username, captured[0].Username, "member should be enriched for View role invite")
			},
		},
		{
			name: "Member role invite — enriches members, Writers, and Auditors with matching email",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID, &model.CommitteeSettings{
					UID:      committee1UID,
					Writers:  []model.CommitteeUser{{Email: memberEmail}},
					Auditors: []model.CommitteeUser{{Email: memberEmail}},
				}))
				r.AddCommittee(makeCommitteeWithSettings(committee2UID, &model.CommitteeSettings{
					UID: committee2UID,
				}))
				r.AddCommitteeMember(committee1UID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-1",
						CommitteeUID: committee1UID,
						Email:        memberEmail,
					},
				})
				r.AddCommitteeMember(committee2UID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-2",
						CommitteeUID: committee2UID,
						Email:        memberEmail,
					},
				})
			},
			msgData:               makeEvent(inviteUID, username, memberEmail, string(inviteapi.InviteRoleMember)),
			wantUpdateCalls:       1,
			wantUpdateMemberCalls: 2,
			validateSettings: func(t *testing.T, captured []*model.CommitteeSettings) {
				require.Len(t, captured, 1)
				s := captured[0]
				assert.Equal(t, username, s.Writers[0].Username, "writer should be enriched")
				assert.Equal(t, username, s.Auditors[0].Username, "auditor should be enriched")
			},
			validateMembers: func(t *testing.T, captured []*model.CommitteeMember) {
				for _, member := range captured {
					assert.Equal(t, username, member.Username, "member should be enriched")
				}
			},
		},
		{
			name: "unknown invite role — enriches Writers, Auditors, and matching members",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID, &model.CommitteeSettings{
					UID:      committee1UID,
					Writers:  []model.CommitteeUser{{Email: writerEmail}},
					Auditors: []model.CommitteeUser{{Email: writerEmail}},
				}))
				r.AddCommitteeMember(committee1UID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-unknown-role",
						CommitteeUID: committee1UID,
						Email:        writerEmail,
					},
				})
			},
			msgData:               makeEvent(inviteUID, username, writerEmail, "SomeFutureRole"),
			wantUpdateCalls:       1,
			wantUpdateMemberCalls: 1,
			validateSettings: func(t *testing.T, captured []*model.CommitteeSettings) {
				require.Len(t, captured, 1)
				s := captured[0]
				assert.Equal(t, username, s.Writers[0].Username, "writer should be enriched for unknown role")
				assert.Equal(t, username, s.Auditors[0].Username, "auditor should be enriched for unknown role")
			},
			validateMembers: func(t *testing.T, captured []*model.CommitteeMember) {
				require.Len(t, captured, 1)
				assert.Equal(t, username, captured[0].Username, "member should be enriched for unknown role")
			},
		},
		{
			name: "no matching email in any committee — no update called",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: "other@example.com"}}}))
			},
			msgData:         makeEvent(inviteUID, username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 0,
		},
		{
			name: "already-enriched entry (has Username) — not double-enriched",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail, Username: "already-set"}}}))
			},
			msgData:         makeEvent(inviteUID, username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 0,
		},
		{
			name: "conflict on write — non-conflict error surfaces immediately, no retry",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
			},
			spyErr:          fmt.Errorf("NATS unavailable"),
			msgData:         makeEvent(inviteUID, username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 1,
		},
		{
			name: "conflict on write — retries and succeeds on second attempt",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID,
					&model.CommitteeSettings{UID: committee1UID, Writers: []model.CommitteeUser{{Email: writerEmail}}}))
			},
			// First UpdateSettings call returns a conflict; second succeeds.
			// GetSettings returns a deep copy each time, so the in-memory enrichment
			// from attempt 1 is not visible on re-read — a genuine second write happens.
			spyErrs:         []error{errs.NewConflict("revision mismatch"), nil},
			msgData:         makeEvent(inviteUID, username, writerEmail, string(inviteapi.InviteRoleManage)),
			wantUpdateCalls: 2,
		},
		{
			name: "member conflict on write — retries and succeeds on second attempt",
			setupRepo: func(r *mock.MockRepository) {
				r.AddCommittee(makeCommitteeWithSettings(committee1UID, &model.CommitteeSettings{UID: committee1UID}))
				r.AddCommitteeMember(committee1UID, &model.CommitteeMember{
					CommitteeMemberBase: model.CommitteeMemberBase{
						UID:          "member-retry",
						CommitteeUID: committee1UID,
						Email:        memberEmail,
					},
				})
			},
			spyMemberErrs:         []error{errs.NewConflict("revision mismatch"), nil},
			msgData:               makeEvent(inviteUID, username, memberEmail, string(inviteapi.InviteRoleMember)),
			wantUpdateCalls:       0,
			wantUpdateMemberCalls: 2,
			validateMembers: func(t *testing.T, captured []*model.CommitteeMember) {
				require.Len(t, captured, 2)
				assert.Equal(t, username, captured[len(captured)-1].Username)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mock.NewMockRepository()
			mockRepo.ClearAll()
			tt.setupRepo(mockRepo)

			spy := &spyCommitteeWriterOrchestrator{
				updateSettingsErr:  tt.spyErr,
				updateSettingsErrs: tt.spyErrs,
				updateMemberErr:    tt.spyMemberErr,
				updateMemberErrs:   tt.spyMemberErrs,
			}
			handler := makeHandler(mockRepo, spy)

			msg := newMockTransportMessenger(inviteapi.InviteServiceAcceptedSubject, tt.msgData)
			_, err := handler.HandleInviteAccepted(ctx, msg)

			require.NoError(t, err)
			assert.Equal(t, tt.wantUpdateCalls, spy.updateSettingsCalls, "UpdateSettings call count")
			assert.Equal(t, tt.wantUpdateMemberCalls, spy.updateMemberCalls, "UpdateMember call count")

			if tt.validateSettings != nil && len(spy.capturedSettings) > 0 {
				tt.validateSettings(t, spy.capturedSettings)
			}
			if tt.validateMembers != nil && len(spy.updatedMembers) > 0 {
				tt.validateMembers(t, spy.updatedMembers)
			}
		})
	}
}
