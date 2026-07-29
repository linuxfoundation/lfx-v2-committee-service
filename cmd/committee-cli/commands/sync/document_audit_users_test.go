// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

type auditUserMockReader struct {
	metadata map[string]*model.UserMetadata
}

func (m *auditUserMockReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *auditUserMockReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (m *auditUserMockReader) UserMetadataByPrincipal(_ context.Context, principal string) (*model.UserMetadata, error) {
	if meta, ok := m.metadata[principal]; ok {
		return meta, nil
	}
	return nil, nil
}

func TestParseDocumentResourceType(t *testing.T) {
	t.Run("empty means all", func(t *testing.T) {
		folders, links, docs, err := parseDocumentResourceType("")
		require.NoError(t, err)
		assert.True(t, folders)
		assert.True(t, links)
		assert.True(t, docs)
	})

	t.Run("folder only", func(t *testing.T) {
		folders, links, docs, err := parseDocumentResourceType("folder")
		require.NoError(t, err)
		assert.True(t, folders)
		assert.False(t, links)
		assert.False(t, docs)
	})

	t.Run("invalid", func(t *testing.T) {
		_, _, _, err := parseDocumentResourceType("bad")
		assert.Error(t, err)
	})
}

func TestDocumentAuditUsersRunner_applyAuditUsers_skipsEnriched(t *testing.T) {
	runner := &documentAuditUsersRunner{
		stats: commands.NewStats(),
	}
	err := runner.applyAuditUsers(context.Background(), freshAuditResource{
		resourceType: "folder",
		uid:          "f1",
		committeeUID: "c1",
		createdBy:    &model.CommitteeUser{Username: "alice", Name: "Alice Example"},
	}, func(*model.CommitteeUser) error {
		t.Fatal("apply should not be called")
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, runner.stats.Skipped)
}

func TestDocumentAuditUsersRunner_applyAuditUsers_dryRun(t *testing.T) {
	runner := &documentAuditUsersRunner{
		userReader: &auditUserMockReader{
			metadata: map[string]*model.UserMetadata{
				"alice": {Name: "Alice Example"},
			},
		},
		dryRun: true,
		stats:  commands.NewStats(),
	}
	err := runner.applyAuditUsers(context.Background(), freshAuditResource{
		resourceType: "link",
		uid:          "l1",
		committeeUID: "c1",
		createdBy:    &model.CommitteeUser{Username: "alice"},
	}, func(*model.CommitteeUser) error {
		t.Fatal("apply should not be called in dry-run")
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, runner.stats.Updated)
}

func TestDocumentAuditUsersRunner_applyAuditUsers_writes(t *testing.T) {
	var applied bool
	runner := &documentAuditUsersRunner{
		userReader: &auditUserMockReader{
			metadata: map[string]*model.UserMetadata{
				"alice": {Name: "Alice Example"},
			},
		},
		dryRun: false,
		stats:  commands.NewStats(),
	}
	err := runner.applyAuditUsers(context.Background(), freshAuditResource{
		resourceType: "document",
		uid:          "d1",
		committeeUID: "c1",
		createdBy:    &model.CommitteeUser{Username: "alice"},
	}, func(profile *model.CommitteeUser) error {
		applied = true
		assert.Equal(t, "Alice Example", profile.Name)
		return nil
	})
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, 1, runner.stats.Updated)
}
