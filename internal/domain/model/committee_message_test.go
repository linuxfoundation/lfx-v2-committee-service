// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitteeIndexerMessage_Build(t *testing.T) {
	tests := []struct {
		name            string
		action          MessageAction
		input           any
		ctx             context.Context
		expectedData    any
		expectedError   bool
		expectedHeaders map[string]string
	}{
		{
			name:   "ActionCreated with struct input",
			action: ActionCreated,
			input: struct {
				UID  string `json:"uid"`
				Name string `json:"name"`
			}{
				UID:  "test-uid-123",
				Name: "Test Committee",
			},
			ctx: context.Background(),
			expectedData: map[string]any{
				"uid":  "test-uid-123",
				"name": "Test Committee",
			},
			expectedError:   false,
			expectedHeaders: map[string]string{},
		},
		{
			name:   "ActionUpdated with map input",
			action: ActionUpdated,
			input: map[string]any{
				"uid":         "test-uid-456",
				"name":        "Updated Committee",
				"description": "Updated description",
			},
			ctx: context.Background(),
			expectedData: map[string]any{
				"uid":         "test-uid-456",
				"name":        "Updated Committee",
				"description": "Updated description",
			},
			expectedError:   false,
			expectedHeaders: map[string]string{},
		},
		{
			name:            "ActionDeleted with UID string",
			action:          ActionDeleted,
			input:           "committee-uid-789",
			ctx:             context.Background(),
			expectedData:    "committee-uid-789",
			expectedError:   false,
			expectedHeaders: map[string]string{},
		},
		{
			name:   "ActionCreated with context headers",
			action: ActionCreated,
			input: map[string]string{
				"uid": "test-uid-with-headers",
			},
			ctx: func() context.Context {
				ctx := context.Background()
				ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer token123")
				ctx = context.WithValue(ctx, constants.PrincipalContextID, "user@example.com")
				return ctx
			}(),
			expectedData: map[string]any{
				"uid": "test-uid-with-headers",
			},
			expectedError: false,
			expectedHeaders: map[string]string{
				constants.AuthorizationHeader: "Bearer token123",
				constants.XOnBehalfOfHeader:   "user@example.com",
			},
		},
		{
			name:   "ActionDeleted with context headers",
			action: ActionDeleted,
			input:  "committee-uid-with-headers",
			ctx: func() context.Context {
				ctx := context.Background()
				ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer token456")
				ctx = context.WithValue(ctx, constants.PrincipalContextID, "admin@example.com")
				return ctx
			}(),
			expectedData:  "committee-uid-with-headers",
			expectedError: false,
			expectedHeaders: map[string]string{
				constants.AuthorizationHeader: "Bearer token456",
				constants.XOnBehalfOfHeader:   "admin@example.com",
			},
		},
		{
			name:            "ActionCreated with unmarshalable input",
			action:          ActionCreated,
			input:           func() {}, // functions cannot be marshaled to JSON
			ctx:             context.Background(),
			expectedData:    nil,
			expectedError:   true,
			expectedHeaders: map[string]string{},
		},
		{
			name:   "ActionCreated with complex nested struct",
			action: ActionCreated,
			input: struct {
				UID      string            `json:"uid"`
				Name     string            `json:"name"`
				Settings map[string]any    `json:"settings"`
				Members  []string          `json:"members"`
				Metadata map[string]string `json:"metadata"`
			}{
				UID:  "complex-uid-123",
				Name: "Complex Committee",
				Settings: map[string]any{
					"public":      true,
					"max_members": 10,
				},
				Members: []string{"user1", "user2", "user3"},
				Metadata: map[string]string{
					"created_by": "admin",
					"version":    "1.0",
				},
			},
			ctx: context.Background(),
			expectedData: map[string]any{
				"uid":  "complex-uid-123",
				"name": "Complex Committee",
				"settings": map[string]any{
					"public":      true,
					"max_members": float64(10), // JSON unmarshaling converts numbers to float64
				},
				"members": []any{"user1", "user2", "user3"},
				"metadata": map[string]any{
					"created_by": "admin",
					"version":    "1.0",
				},
			},
			expectedError:   false,
			expectedHeaders: map[string]string{},
		},
		{
			name:            "ActionDeleted with non-string input (should still work)",
			action:          ActionDeleted,
			input:           123456, // numeric UID
			ctx:             context.Background(),
			expectedData:    123456,
			expectedError:   false,
			expectedHeaders: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			message := &CommitteeIndexerMessage{
				Action: tt.action,
			}

			// Act
			result, err := message.Build(tt.ctx, tt.input)

			// Assert
			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			// Verify action is preserved
			assert.Equal(t, tt.action, result.Action)

			// Verify data content
			assert.Equal(t, tt.expectedData, result.Data)

			// Verify headers
			assert.Equal(t, tt.expectedHeaders, result.Headers)

			// Verify the result is the same instance (method should modify and return self)
			assert.Equal(t, message, result)
		})
	}
}

func TestCommitteeIndexerMessage_Build_ContextValues(t *testing.T) {
	tests := []struct {
		name                      string
		authorizationValue        any
		principalValue            any
		expectedAuthHeader        string
		expectedPrincipalHeader   string
		shouldHaveAuthHeader      bool
		shouldHavePrincipalHeader bool
	}{
		{
			name:                      "Both context values as strings",
			authorizationValue:        "Bearer valid-token",
			principalValue:            "user@example.com",
			expectedAuthHeader:        "Bearer valid-token",
			expectedPrincipalHeader:   "user@example.com",
			shouldHaveAuthHeader:      true,
			shouldHavePrincipalHeader: true,
		},
		{
			name:                      "Only authorization context value",
			authorizationValue:        "Bearer only-auth",
			principalValue:            nil,
			expectedAuthHeader:        "Bearer only-auth",
			expectedPrincipalHeader:   "",
			shouldHaveAuthHeader:      true,
			shouldHavePrincipalHeader: false,
		},
		{
			name:                      "Only principal context value",
			authorizationValue:        nil,
			principalValue:            "only-principal@example.com",
			expectedAuthHeader:        "",
			expectedPrincipalHeader:   "only-principal@example.com",
			shouldHaveAuthHeader:      false,
			shouldHavePrincipalHeader: true,
		},
		{
			name:                      "Non-string context values (should be ignored)",
			authorizationValue:        12345,
			principalValue:            []string{"not", "a", "string"},
			expectedAuthHeader:        "",
			expectedPrincipalHeader:   "",
			shouldHaveAuthHeader:      false,
			shouldHavePrincipalHeader: false,
		},
		{
			name:                      "Empty string context values",
			authorizationValue:        "",
			principalValue:            "",
			expectedAuthHeader:        "",
			expectedPrincipalHeader:   "",
			shouldHaveAuthHeader:      true,
			shouldHavePrincipalHeader: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			ctx := context.Background()
			if tt.authorizationValue != nil {
				ctx = context.WithValue(ctx, constants.AuthorizationContextID, tt.authorizationValue)
			}
			if tt.principalValue != nil {
				ctx = context.WithValue(ctx, constants.PrincipalContextID, tt.principalValue)
			}

			message := &CommitteeIndexerMessage{
				Action: ActionDeleted,
			}

			// Act
			result, err := message.Build(ctx, "test-uid")

			// Assert
			require.NoError(t, err)
			require.NotNil(t, result)

			if tt.shouldHaveAuthHeader {
				assert.Contains(t, result.Headers, constants.AuthorizationHeader)
				assert.Equal(t, tt.expectedAuthHeader, result.Headers[constants.AuthorizationHeader])
			} else {
				assert.NotContains(t, result.Headers, constants.AuthorizationHeader)
			}

			if tt.shouldHavePrincipalHeader {
				assert.Contains(t, result.Headers, constants.XOnBehalfOfHeader)
				assert.Equal(t, tt.expectedPrincipalHeader, result.Headers[constants.XOnBehalfOfHeader])
			} else {
				assert.NotContains(t, result.Headers, constants.XOnBehalfOfHeader)
			}
		})
	}
}

// TestCommitteeIndexerMessage_Build_DeleteAction_RawUID tests the specific issue
// mentioned in LFXV2-258 where delete actions should pass the UID directly
// without JSON marshaling to avoid quotes in the indexer.
func TestCommitteeIndexerMessage_Build_DeleteAction_RawUID(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{
			name:     "String UID should be passed directly",
			input:    "bc2f4225-4b77-4a36-8992-aa9430731600",
			expected: "bc2f4225-4b77-4a36-8992-aa9430731600",
		},
		{
			name:     "Numeric UID should be passed directly",
			input:    123456789,
			expected: 123456789,
		},
		{
			name:     "Complex object should be passed directly (not JSON processed)",
			input:    map[string]string{"uid": "test-123"},
			expected: map[string]string{"uid": "test-123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			message := &CommitteeIndexerMessage{
				Action: ActionDeleted,
			}

			// Act
			result, err := message.Build(context.Background(), tt.input)

			// Assert
			require.NoError(t, err)
			require.NotNil(t, result)

			// The key assertion: for delete actions, the data should be exactly
			// what was passed in, without any JSON marshaling/unmarshaling
			assert.Equal(t, tt.expected, result.Data)

			// Verify it's exactly the same type and value (no JSON conversion)
			assert.IsType(t, tt.input, result.Data)
		})
	}
}
