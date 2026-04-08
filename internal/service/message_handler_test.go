// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
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

// Helper function to create string pointer
func messageHandlerStringPtr(s string) *string {
	return &s
}
