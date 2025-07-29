// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommitteeSSOGroupNameBuild(t *testing.T) {
	tests := []struct {
		name              string
		committee         Committee
		projectSlug       string
		expectedGroupName string
		expectError       bool
		errorMessage      string
	}{
		{
			name: "first time creation with empty SSO group name",
			committee: Committee{
				Name:         "Technical Steering Committee",
				SSOGroupName: "",
			},
			projectSlug:       "kubernetes",
			expectedGroupName: "kubernetes-technical-steering-committee-1",
			expectError:       false,
		},
		{
			name: "first time creation with simple name",
			committee: Committee{
				Name:         "Security",
				SSOGroupName: "",
			},
			projectSlug:       "linux",
			expectedGroupName: "linux-security-1",
			expectError:       false,
		},
		{
			name: "increment existing SSO group name",
			committee: Committee{
				Name:         "Technical Committee",
				SSOGroupName: "kubernetes-technical-committee-1",
			},
			projectSlug:       "kubernetes",
			expectedGroupName: "kubernetes-technical-committee-2",
			expectError:       false,
		},
		{
			name: "increment existing SSO group name with higher index",
			committee: Committee{
				Name:         "Governance",
				SSOGroupName: "project-governance-5",
			},
			projectSlug:       "project",
			expectedGroupName: "project-governance-6",
			expectError:       false,
		},
		{
			name: "handle special characters in committee name",
			committee: Committee{
				Name:         "API & Documentation Committee",
				SSOGroupName: "",
			},
			projectSlug:       "openapi",
			expectedGroupName: "openapi-api-and-documentation-committee-1",
			expectError:       false,
		},
		{
			name: "handle special characters in project slug",
			committee: Committee{
				Name:         "Core Team",
				SSOGroupName: "",
			},
			projectSlug:       "my-awesome-project",
			expectedGroupName: "my-awesome-project-core-team-1",
			expectError:       false,
		},
		{
			name: "handle unicode characters",
			committee: Committee{
				Name:         "Développement Committee",
				SSOGroupName: "",
			},
			projectSlug:       "français",
			expectedGroupName: "francais-developpement-committee-1",
			expectError:       false,
		},
		{
			name: "invalid index in existing SSO group name",
			committee: Committee{
				Name:         "Testing Committee",
				SSOGroupName: "project-testing-committee-invalid",
			},
			projectSlug:  "project",
			expectError:  true,
			errorMessage: "invalid SSO group name index",
		},
		{
			name: "invalid index with non-numeric suffix",
			committee: Committee{
				Name:         "Review Committee",
				SSOGroupName: "project-review-committee-abc",
			},
			projectSlug:  "project",
			expectError:  true,
			errorMessage: "invalid SSO group name index",
		},
		{
			name: "existing SSO group name with multiple dashes",
			committee: Committee{
				Name:         "Multi-Word-Committee",
				SSOGroupName: "multi-word-project-multi-word-committee-3",
			},
			projectSlug:       "multi-word-project",
			expectedGroupName: "multi-word-project-multi-word-committee-4",
			expectError:       false,
		},
		{
			name: "empty project slug",
			committee: Committee{
				Name:         "Committee",
				SSOGroupName: "",
			},
			projectSlug:       "",
			expectedGroupName: "committee-1",
			expectError:       false,
		},
		{
			name: "empty committee name",
			committee: Committee{
				Name:         "",
				SSOGroupName: "",
			},
			projectSlug:       "project",
			expectedGroupName: "project-1",
			expectError:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			committee := tc.committee

			err := committee.SSOGroupNameBuild(ctx, tc.projectSlug)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMessage)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedGroupName, committee.SSOGroupName)
			}
		})
	}
}

func TestCommitteeSSOGroupNameBuild_Idempotent(t *testing.T) {
	committee := Committee{
		Name:         "Idempotent Committee",
		SSOGroupName: "",
	}
	ctx := context.Background()
	projectSlug := "test-project"

	// First call
	err1 := committee.SSOGroupNameBuild(ctx, projectSlug)
	assert.NoError(t, err1)
	firstResult := committee.SSOGroupName

	// Second call should increment the index
	err2 := committee.SSOGroupNameBuild(ctx, projectSlug)
	assert.NoError(t, err2)
	secondResult := committee.SSOGroupName

	assert.Equal(t, "test-project-idempotent-committee-1", firstResult)
	assert.Equal(t, "test-project-idempotent-committee-2", secondResult)
	assert.NotEqual(t, firstResult, secondResult)
}
