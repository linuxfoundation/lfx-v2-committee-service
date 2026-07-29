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

type testAuditUserReader struct {
	metadata map[string]*model.UserMetadata
}

func (m *testAuditUserReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *testAuditUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (m *testAuditUserReader) UserMetadataByPrincipal(_ context.Context, principal string) (*model.UserMetadata, error) {
	if meta, ok := m.metadata[principal]; ok {
		return meta, nil
	}
	return nil, nil
}

func TestParseDocumentResourceType(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantFolders bool
		wantLinks   bool
		wantDocs    bool
		wantErr     bool
	}{
		{name: "empty means all", wantFolders: true, wantLinks: true, wantDocs: true},
		{name: "folder only", in: "folder", wantFolders: true},
		{name: "invalid", in: "bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folders, links, docs, err := parseDocumentResourceType(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantFolders, folders)
			assert.Equal(t, tt.wantLinks, links)
			assert.Equal(t, tt.wantDocs, docs)
		})
	}
}

func TestDocumentAuditUsersRunner_applyAuditUsers(t *testing.T) {
	reader := &testAuditUserReader{
		metadata: map[string]*model.UserMetadata{
			"alice": {Name: "Alice Example"},
			"bob":   {Name: "Bob Example"},
		},
	}

	tests := []struct {
		name        string
		runner      *documentAuditUsersRunner
		resource    freshAuditResource
		wantSkipped bool
		wantUpdated bool
		wantFailed  bool
		wantApplied bool
	}{
		{
			name:   "skips enriched records",
			runner: &documentAuditUsersRunner{stats: commands.NewStats()},
			resource: freshAuditResource{
				resourceType: "folder",
				uid:          "f1",
				committeeUID: "c1",
				createdBy:    &model.CommitteeUser{Username: "alice", Name: "Alice Example"},
			},
			wantSkipped: true,
		},
		{
			name: "dry run counts updated",
			runner: &documentAuditUsersRunner{
				userReader: reader,
				dryRun:     true,
				stats:      commands.NewStats(),
			},
			resource: freshAuditResource{
				resourceType: "link",
				uid:          "l1",
				committeeUID: "c1",
				createdBy:    &model.CommitteeUser{Username: "alice"},
			},
			wantUpdated: true,
		},
		{
			name: "writes enriched profile",
			runner: &documentAuditUsersRunner{
				userReader: reader,
				dryRun:     false,
				stats:      commands.NewStats(),
			},
			resource: freshAuditResource{
				resourceType: "document",
				uid:          "d1",
				committeeUID: "c1",
				createdBy:    &model.CommitteeUser{Username: "alice"},
			},
			wantUpdated: true,
			wantApplied: true,
		},
		{
			name: "migrates distinct updated_by independently",
			runner: &documentAuditUsersRunner{
				userReader: reader,
				dryRun:     false,
				stats:      commands.NewStats(),
			},
			resource: freshAuditResource{
				resourceType: "link",
				uid:          "l2",
				committeeUID: "c1",
				createdBy:    &model.CommitteeUser{Username: "alice", Name: "Alice Example"},
				updatedBy:    &model.CommitteeUser{Username: "bob"},
			},
			wantUpdated: true,
			wantApplied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applied := false
			err := tt.runner.applyAuditUsers(context.Background(), tt.resource, func(createdBy, updatedBy *model.CommitteeUser) error {
				applied = true
				if tt.resource.updatedBy != nil && model.AuditUserNeedsMigration(tt.resource.updatedBy) {
					require.NotNil(t, updatedBy)
					assert.Equal(t, "bob", updatedBy.Username)
					assert.Equal(t, "Bob Example", updatedBy.Name)
					return nil
				}
				require.NotNil(t, createdBy)
				assert.Equal(t, "Alice Example", createdBy.Name)
				return nil
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantApplied, applied)
			if tt.wantSkipped {
				assert.Equal(t, 1, tt.runner.stats.Skipped)
			}
			if tt.wantUpdated {
				assert.Equal(t, 1, tt.runner.stats.Updated)
			}
			if tt.wantFailed {
				assert.Equal(t, 1, tt.runner.stats.Failed)
			}
		})
	}
}

func TestDocumentAuditUsersRunner_throttle(t *testing.T) {
	runner := &documentAuditUsersRunner{sleep: 0}
	require.NoError(t, runner.throttle(context.Background()))
}
