// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func TestCommitteeService_resolveRequestingUser(t *testing.T) {
	t.Run("nil principal returns nil", func(t *testing.T) {
		svc := &committeeServicesrvc{}
		got := svc.resolveRequestingUser(context.Background())
		assert.Nil(t, got)
	})

	t.Run("no user reader stamps username and email from context", func(t *testing.T) {
		svc := &committeeServicesrvc{}
		ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
		ctx = context.WithValue(ctx, constants.EmailContextID, "alice@example.com")

		got := svc.resolveRequestingUser(ctx)

		assert.NotNil(t, got)
		assert.Equal(t, "alice", got.Username)
		assert.Equal(t, "alice@example.com", got.Email)
	})

	t.Run("happy path resolves full profile", func(t *testing.T) {
		svc := &committeeServicesrvc{
			userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
				withMetadata("alice", &model.UserMetadata{
					Name:    "Alice Example",
					Picture: "https://cdn.example/avatar.png",
				}),
		}

		ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
		ctx = context.WithValue(ctx, constants.EmailContextID, "jwt@example.com")

		got := svc.resolveRequestingUser(ctx)

		assert.NotNil(t, got)
		assert.Equal(t, "alice", got.Username)
		assert.Equal(t, "Alice Example", got.Name)
		assert.Equal(t, "https://cdn.example/avatar.png", got.Avatar)
		assert.Equal(t, "alice@lf.org", got.Email)
	})

	t.Run("metadata failure falls back to username and JWT email", func(t *testing.T) {
		svc := &committeeServicesrvc{
			userReader: newMockUserReader().withMetadataErr(errors.New("nats unavailable")),
		}

		ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
		ctx = context.WithValue(ctx, constants.EmailContextID, "jwt@example.com")

		got := svc.resolveRequestingUser(ctx)

		assert.NotNil(t, got)
		assert.Equal(t, "alice", got.Username)
		assert.Equal(t, "jwt@example.com", got.Email)
		assert.Empty(t, got.Name)
	})
}

func TestCommitteeService_enrichAuditUserIfMissing(t *testing.T) {
	t.Run("skips when name already set", func(t *testing.T) {
		svc := &committeeServicesrvc{userReader: newMockUserReader()}
		user := &model.CommitteeUser{Username: "alice", Name: "Alice Example"}

		got := svc.enrichAuditUserIfMissing(context.Background(), user)

		assert.Equal(t, user, got)
	})

	t.Run("enriches legacy username-only record", func(t *testing.T) {
		svc := &committeeServicesrvc{
			userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
				withMetadata("alice", &model.UserMetadata{
					Name:    "Alice Example",
					Picture: "https://cdn.example/avatar.png",
				}),
		}

		user := &model.CommitteeUser{Username: "alice"}
		got := svc.enrichAuditUserIfMissing(context.Background(), user)

		assert.Equal(t, "Alice Example", got.Name)
		assert.Equal(t, "https://cdn.example/avatar.png", got.Avatar)
		assert.Equal(t, "alice@lf.org", got.Email)
		assert.Equal(t, "alice", user.Username, "original struct must not be mutated")
	})
}

func TestCommitteeService_stampDocumentAuditUsers(t *testing.T) {
	svc := &committeeServicesrvc{
		userReader: mockReaderForPrincipalEmail("alice", "alice@lf.org").
			withMetadata("alice", &model.UserMetadata{Name: "Alice Example"}),
	}

	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
	created, updated := svc.stampDocumentAuditUsers(ctx)

	assert.NotNil(t, created)
	assert.NotNil(t, updated)
	assert.Equal(t, created.Username, updated.Username)
	assert.Equal(t, "Alice Example", created.Name)
}

func TestCommitteeService_normalizeDocumentAuditUsers(t *testing.T) {
	svc := &committeeServicesrvc{
		userReader: newMockUserReader().withMetadata("alice", &model.UserMetadata{Name: "Alice Example"}),
	}

	var createdBy, updatedBy *model.CommitteeUser
	svc.normalizeDocumentAuditUsers(context.Background(), &createdBy, &updatedBy, "alice", "")

	assert.NotNil(t, createdBy)
	assert.Equal(t, "alice", createdBy.Username)
	assert.Equal(t, "Alice Example", createdBy.Name)
	assert.NotNil(t, updatedBy)
	assert.Equal(t, createdBy.Name, updatedBy.Name)
}
