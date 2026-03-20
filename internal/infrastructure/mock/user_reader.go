// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"os"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// MockUserReader provides a mock implementation of the UserReader port.
// It reads the caller's email from the JWT_AUTH_DISABLED_MOCK_LOCAL_EMAIL
// environment variable, mirroring the mock auth service pattern.
type MockUserReader struct{}

// SubByEmail is not used in mock mode; always returns an empty string.
func (m *MockUserReader) SubByEmail(_ context.Context, _ string) (string, error) {
	return "", nil
}

// EmailsByPrincipal returns a UserEmails populated from the
// JWT_AUTH_DISABLED_MOCK_LOCAL_EMAIL environment variable.
func (m *MockUserReader) EmailsByPrincipal(_ context.Context, _ string) (*model.UserEmails, error) {
	email := os.Getenv("JWT_AUTH_DISABLED_MOCK_LOCAL_EMAIL")
	if email == "" {
		return nil, errors.NewValidation("mock email not configured in JWT_AUTH_DISABLED_MOCK_LOCAL_EMAIL")
	}
	return &model.UserEmails{PrimaryEmail: email}, nil
}

// NewMockUserReader creates a new mock UserReader.
func NewMockUserReader() port.UserReader {
	return &MockUserReader{}
}
