// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func TestErrorMessageNATSResponse_CheckError(t *testing.T) {
	tests := []struct {
		name        string
		message     string
		wantErr     bool
		wantErrType string
		wantErrMsg  string
		description string
	}{
		{
			name:        "valid error response with generic error",
			message:     `{"success":false,"error":"something went wrong"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "something went wrong",
			description: "should return Unexpected error when success is false",
		},
		{
			name:        "valid error response with not found",
			message:     `{"success":false,"error":"user not found"}`,
			wantErr:     true,
			wantErrType: "NotFound",
			wantErrMsg:  "user not found",
			description: "should return NotFound error when error contains 'not found'",
		},
		{
			name:        "valid error response with not found uppercase",
			message:     `{"success":false,"error":"Resource Not Found"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "Resource Not Found",
			description: "should return Unexpected error when 'not found' is not lowercase (case-sensitive)",
		},
		{
			name:        "valid success response",
			message:     `{"success":true,"error":""}`,
			wantErr:     false,
			wantErrType: "",
			wantErrMsg:  "",
			description: "should return nil when success is true",
		},
		{
			name:        "success true with error message",
			message:     `{"success":true,"error":"this should be ignored"}`,
			wantErr:     false,
			wantErrType: "",
			wantErrMsg:  "",
			description: "should return nil even if error field has value when success is true",
		},
		{
			name:        "invalid json",
			message:     `{invalid json}`,
			wantErr:     false,
			wantErrType: "",
			wantErrMsg:  "",
			description: "should return nil for invalid JSON",
		},
		{
			name:        "empty string",
			message:     ``,
			wantErr:     false,
			wantErrType: "",
			wantErrMsg:  "",
			description: "should return nil for empty message",
		},
		{
			name:        "missing success field",
			message:     `{"error":"some error"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "some error",
			description: "should return Unexpected error when success field is missing (defaults to false)",
		},
		{
			name:        "missing error field",
			message:     `{"success":false}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "",
			description: "should return Unexpected error with empty message when error field is missing",
		},
		{
			name:        "empty json object",
			message:     `{}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "",
			description: "should return Unexpected error with empty message for empty JSON object",
		},
		{
			name:        "error with special characters",
			message:     `{"success":false,"error":"database error: connection timeout\n\tat line 42"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "database error: connection timeout\n\tat line 42",
			description: "should handle error messages with special characters",
		},
		{
			name:        "error with unicode characters",
			message:     `{"success":false,"error":"erreur: échec de connexion"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "erreur: échec de connexion",
			description: "should handle error messages with unicode characters",
		},
		{
			name:        "malformed json with extra fields",
			message:     `{"success":false,"error":"test error","extra":"field"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "test error",
			description: "should ignore extra fields and extract error correctly",
		},
		{
			name:        "not found in middle of message",
			message:     `{"success":false,"error":"the item was not found in database"}`,
			wantErr:     true,
			wantErrType: "NotFound",
			wantErrMsg:  "the item was not found in database",
			description: "should detect 'not found' anywhere in the error message",
		},
		{
			name:        "notfound without space",
			message:     `{"success":false,"error":"notfound error"}`,
			wantErr:     true,
			wantErrType: "Unexpected",
			wantErrMsg:  "notfound error",
			description: "should not match 'notfound' without space - requires 'not found'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ErrorMessageNATSResponse{}
			err := e.CheckError(tt.message)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CheckError() error = nil, want error\nDescription: %s", tt.description)
					return
				}

				// Check error type
				switch tt.wantErrType {
				case "NotFound":
					if _, ok := err.(errors.NotFound); !ok {
						t.Errorf("CheckError() error type = %T, want errors.NotFound\nDescription: %s",
							err, tt.description)
					}
				case "Unexpected":
					if _, ok := err.(errors.Unexpected); !ok {
						t.Errorf("CheckError() error type = %T, want errors.Unexpected\nDescription: %s",
							err, tt.description)
					}
				default:
					t.Errorf("Unknown error type in test: %s", tt.wantErrType)
				}

				// Check error message
				if err.Error() != tt.wantErrMsg {
					t.Errorf("CheckError() error message = %q, want %q\nDescription: %s",
						err.Error(), tt.wantErrMsg, tt.description)
				}
			} else {
				if err != nil {
					t.Errorf("CheckError() error = %v, want nil\nDescription: %s",
						err, tt.description)
				}
			}
		})
	}
}

func TestErrorMessageNATSResponse_CheckError_NonMutating(t *testing.T) {
	// Test that CheckError doesn't mutate the receiver
	e := ErrorMessageNATSResponse{
		Success: true,
		Error:   "initial error",
	}

	message := `{"success":false,"error":"new error"}`
	err := e.CheckError(message)

	if err == nil {
		t.Error("CheckError() should return error, got nil")
		return
	}

	// Check it's an Unexpected error
	if _, ok := err.(errors.Unexpected); !ok {
		t.Errorf("CheckError() error type = %T, want errors.Unexpected", err)
	}

	// Check error message
	if err.Error() != "new error" {
		t.Errorf("CheckError() error message = %q, want %q", err.Error(), "new error")
	}

	// Verify the receiver wasn't mutated
	if !e.Success {
		t.Errorf("CheckError() mutated receiver Success field: got %v, want true", e.Success)
	}

	if e.Error != "initial error" {
		t.Errorf("CheckError() mutated receiver Error field: got %q, want %q",
			e.Error, "initial error")
	}
}

func TestErrorMessageNATSResponse_CheckError_ErrorTypes(t *testing.T) {
	tests := []struct {
		name          string
		message       string
		expectedType  interface{}
		expectedError string
	}{
		{
			name:          "NotFound error type",
			message:       `{"success":false,"error":"record not found"}`,
			expectedType:  errors.NotFound{},
			expectedError: "record not found",
		},
		{
			name:          "Unexpected error type",
			message:       `{"success":false,"error":"internal server error"}`,
			expectedType:  errors.Unexpected{},
			expectedError: "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ErrorMessageNATSResponse{}
			err := e.CheckError(tt.message)

			if err == nil {
				t.Fatal("CheckError() should return error, got nil")
			}

			// Verify we can type assert to the expected error type
			switch tt.expectedType.(type) {
			case errors.NotFound:
				if _, ok := err.(errors.NotFound); !ok {
					t.Errorf("CheckError() error type = %T, want errors.NotFound", err)
				}
			case errors.Unexpected:
				if _, ok := err.(errors.Unexpected); !ok {
					t.Errorf("CheckError() error type = %T, want errors.Unexpected", err)
				}
			}

			if err.Error() != tt.expectedError {
				t.Errorf("CheckError() error = %q, want %q", err.Error(), tt.expectedError)
			}
		})
	}
}
