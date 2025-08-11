// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEtagValidator(t *testing.T) {
	tests := []struct {
		name           string
		etag           *string
		expectedResult uint64
		expectError    bool
		errorType      interface{}
		errorMessage   string
	}{
		{
			name:           "valid etag with number",
			etag:           stringPtr("123"),
			expectedResult: 123,
			expectError:    false,
		},
		{
			name:           "valid etag with zero",
			etag:           stringPtr("0"),
			expectedResult: 0,
			expectError:    false,
		},
		{
			name:           "valid etag with large number",
			etag:           stringPtr("18446744073709551615"), // max uint64
			expectedResult: 18446744073709551615,
			expectError:    false,
		},
		{
			name:         "nil etag",
			etag:         nil,
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "ETag is required for update operations",
		},
		{
			name:         "empty etag",
			etag:         stringPtr(""),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "ETag is required for update operations",
		},
		{
			name:         "invalid etag format - non-numeric",
			etag:         stringPtr("abc"),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
		{
			name:         "invalid etag format - negative number",
			etag:         stringPtr("-123"),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
		{
			name:         "invalid etag format - decimal number",
			etag:         stringPtr("123.45"),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
		{
			name:         "invalid etag format - mixed alphanumeric",
			etag:         stringPtr("123abc"),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
		{
			name:         "invalid etag format - number too large for uint64",
			etag:         stringPtr("18446744073709551616"), // max uint64 + 1
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
		{
			name:         "invalid etag format - whitespace",
			etag:         stringPtr(" 123 "),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
		{
			name:         "invalid etag format - special characters",
			etag:         stringPtr("123!@#"),
			expectError:  true,
			errorType:    &committeeservice.BadRequestError{},
			errorMessage: "invalid ETag format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := etagValidator(tt.etag)

			if tt.expectError {
				require.Error(t, err)
				assert.Equal(t, uint64(0), result)

				// Check error type and message based on the specific GOA error type
				switch expectedErr := tt.errorType.(type) {
				case *committeeservice.BadRequestError:
					badReqErr, ok := err.(*committeeservice.BadRequestError)
					require.True(t, ok, "Expected BadRequestError, got %T", err)
					assert.Contains(t, badReqErr.Message, tt.errorMessage)
				default:
					t.Errorf("Unexpected error type in test: %T", expectedErr)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestEtagValidatorWithDifferentInputs(t *testing.T) {
	// Test with different input scenarios
	tests := []struct {
		name    string
		etag    *string
		wantErr bool
	}{
		{
			name:    "valid etag with number 42",
			etag:    stringPtr("42"),
			wantErr: false,
		},
		{
			name:    "valid etag with number 100",
			etag:    stringPtr("100"),
			wantErr: false,
		},
		{
			name:    "invalid etag with non-numeric value",
			etag:    stringPtr("invalid"),
			wantErr: true,
		},
		{
			name:    "invalid etag with mixed alphanumeric",
			etag:    stringPtr("123invalid"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := etagValidator(tt.etag)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEtagValidatorEdgeCases(t *testing.T) {
	t.Run("etag with leading zeros", func(t *testing.T) {
		etag := stringPtr("00123")
		result, err := etagValidator(etag)

		require.NoError(t, err)
		assert.Equal(t, uint64(123), result)
	})

	t.Run("etag with just zero", func(t *testing.T) {
		etag := stringPtr("0")
		result, err := etagValidator(etag)

		require.NoError(t, err)
		assert.Equal(t, uint64(0), result)
	})

	t.Run("etag with multiple zeros", func(t *testing.T) {
		etag := stringPtr("000")
		result, err := etagValidator(etag)

		require.NoError(t, err)
		assert.Equal(t, uint64(0), result)
	})
}
