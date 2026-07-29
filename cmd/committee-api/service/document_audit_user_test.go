// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func TestCommitteeService_resolveRequestingUser(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		userReader port.UserReader
		wantNil    bool
		wantUser   *model.CommitteeUser
	}{
		{
			name:    "nil principal returns nil",
			ctx:     context.Background(),
			wantNil: true,
		},
		{
			name: "no user reader stamps username and email from context",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
				return context.WithValue(ctx, constants.EmailContextID, "alice@example.com")
			}(),
			wantUser: &model.CommitteeUser{Username: "alice", Email: "alice@example.com"},
		},
		{
			name: "happy path resolves full profile",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
				return context.WithValue(ctx, constants.EmailContextID, "jwt@example.com")
			}(),
			userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
				withMetadata("alice", &model.UserMetadata{
					Name:    "Alice Example",
					Picture: "https://cdn.example/avatar.png",
				}),
			wantUser: &model.CommitteeUser{
				Username: "alice",
				Name:     "Alice Example",
				Avatar:   "https://cdn.example/avatar.png",
				Email:    "alice@lf.org",
			},
		},
		{
			name: "metadata failure falls back to username and JWT email",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
				return context.WithValue(ctx, constants.EmailContextID, "jwt@example.com")
			}(),
			userReader: newMockUserReader().withMetadataErr(errors.New("nats unavailable")),
			wantUser:   &model.CommitteeUser{Username: "alice", Email: "jwt@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{userReader: tt.userReader}
			got := svc.resolveRequestingUser(tt.ctx)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantUser.Username, got.Username)
			assert.Equal(t, tt.wantUser.Email, got.Email)
			assert.Equal(t, tt.wantUser.Name, got.Name)
			assert.Equal(t, tt.wantUser.Avatar, got.Avatar)
		})
	}
}

func TestCommitteeService_enrichAuditUserIfMissing(t *testing.T) {
	tests := []struct {
		name       string
		userReader port.UserReader
		user       *model.CommitteeUser
		wantName   string
		wantAvatar string
		wantEmail  string
		skipAuth   bool
	}{
		{
			name:       "skips when profile is complete",
			user:       &model.CommitteeUser{Username: "alice", Name: "Alice Example", Avatar: "a.png", Email: "a@lf.org"},
			skipAuth:   true,
			wantName:   "Alice Example",
			wantAvatar: "a.png",
			wantEmail:  "a@lf.org",
		},
		{
			name: "enriches avatar and email when name already set",
			user: &model.CommitteeUser{Username: "alice", Name: "Alice Example"},
			userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
				withMetadata("alice", &model.UserMetadata{
					Name:    "Alice Example",
					Picture: "https://cdn.example/avatar.png",
				}),
			wantName:   "Alice Example",
			wantAvatar: "https://cdn.example/avatar.png",
			wantEmail:  "alice@lf.org",
		},
		{
			name: "enriches legacy username-only record",
			user: &model.CommitteeUser{Username: "alice"},
			userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
				withMetadata("alice", &model.UserMetadata{
					Name:    "Alice Example",
					Picture: "https://cdn.example/avatar.png",
				}),
			wantName:   "Alice Example",
			wantAvatar: "https://cdn.example/avatar.png",
			wantEmail:  "alice@lf.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{userReader: tt.userReader}
			originalUsername := tt.user.Username
			got := svc.enrichAuditUserIfMissing(context.Background(), tt.user)

			assert.Equal(t, tt.wantName, got.Name)
			assert.Equal(t, tt.wantAvatar, got.Avatar)
			assert.Equal(t, tt.wantEmail, got.Email)
			assert.Equal(t, originalUsername, tt.user.Username, "original struct must not be mutated")
			if tt.skipAuth {
				assert.Equal(t, tt.user, got)
			}
		})
	}
}

func TestCommitteeService_stampDocumentAuditUsers(t *testing.T) {
	svc := &committeeServicesrvc{
		userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
			withMetadata("alice", &model.UserMetadata{Name: "Alice Example"}),
	}

	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
	created, updated := svc.stampDocumentAuditUsers(ctx)

	require.NotNil(t, created)
	require.NotNil(t, updated)
	assert.Equal(t, created.Username, updated.Username)
	assert.Equal(t, "Alice Example", created.Name)
}

func TestCommitteeService_normalizeDocumentAuditUsers(t *testing.T) {
	tests := []struct {
		name         string
		userReader   port.UserReader
		legacyCreate string
		legacyUpload string
		seedUpdated  *model.CommitteeUser
		wantUpdated  string
	}{
		{
			name:         "legacy username enriches created and updated",
			userReader:   newMockUserReader().withMetadata("alice", &model.UserMetadata{Name: "Alice Example"}),
			legacyCreate: "alice",
			wantUpdated:  "Alice Example",
		},
		{
			name: "preserves richer updated_by when usernames match",
			userReader: newMockUserReader().
				withMetadata("alice", &model.UserMetadata{Name: "Alice Example"}),
			legacyCreate: "alice",
			seedUpdated:  &model.CommitteeUser{Username: "alice", Name: "Alice Existing", Avatar: "keep.png", Email: "keep@lf.org"},
			wantUpdated:  "Alice Existing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{userReader: tt.userReader}
			var createdBy, updatedBy *model.CommitteeUser
			if tt.seedUpdated != nil {
				updatedBy = model.CloneCommitteeUser(tt.seedUpdated)
				createdBy = &model.CommitteeUser{Username: tt.seedUpdated.Username}
			}
			svc.normalizeDocumentAuditUsers(context.Background(), &createdBy, &updatedBy, tt.legacyCreate, tt.legacyUpload)

			require.NotNil(t, createdBy)
			require.NotNil(t, updatedBy)
			assert.Equal(t, tt.wantUpdated, updatedBy.Name)
		})
	}
}
