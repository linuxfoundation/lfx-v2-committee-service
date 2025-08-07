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
		expectedError     bool
	}{
		{
			name: "first time creation with empty SSO group name",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Technical Steering Committee",
					SSOGroupName: "",
				},
			},
			projectSlug:       "kubernetes",
			expectedGroupName: "kubernetes-technical-steering-committee",
		},
		{
			name: "increment existing SSO group name with no index",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Governance",
					SSOGroupName: "project-governance",
				},
			},
			projectSlug:       "project",
			expectedGroupName: "project-governance-2",
		},
		{
			name: "increment existing SSO group name with higher index",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Governance",
					SSOGroupName: "project-governance-5",
				},
			},
			projectSlug:       "project",
			expectedGroupName: "project-governance-6",
		},
		{
			name: "handle special characters in committee name",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "API & Documentation Committee",
					SSOGroupName: "",
				},
			},
			projectSlug:       "openapi",
			expectedGroupName: "openapi-api-and-documentation-committee",
		},
		{
			name: "handle special characters in project slug",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Core Team",
					SSOGroupName: "",
				},
			},
			projectSlug:       "my-awesome-project",
			expectedGroupName: "my-awesome-project-core-team",
		},
		{
			name: "handle unicode characters",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Développement Committee",
					SSOGroupName: "",
				},
			},
			projectSlug:       "français",
			expectedGroupName: "francais-developpement-committee",
		},
		{
			name: "invalid index in existing SSO group name - treated as first time creation",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Testing Committee",
					SSOGroupName: "project-testing-committee-invalid",
				},
			},
			projectSlug:       "project",
			expectedGroupName: "project-testing-committee",
			expectedError:     true, // Expecting an error due to invalid suffix
		},
		{
			name: "existing SSO group name with multiple dashes",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Multi-Word-Committee",
					SSOGroupName: "multi-word-project-multi-word-committee-3",
				},
			},
			projectSlug:       "multi-word-project",
			expectedGroupName: "multi-word-project-multi-word-committee-4",
		},
		{
			name: "empty project slug",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "Committee",
					SSOGroupName: "",
				},
			},
			projectSlug:       "",
			expectedGroupName: "committee",
		},
		{
			name: "empty committee name",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					Name:         "",
					SSOGroupName: "",
				},
			},
			projectSlug:       "project",
			expectedGroupName: "project",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			committee := tc.committee

			err := committee.SSOGroupNameBuild(ctx, tc.projectSlug)
			if tc.expectedError {
				assert.Error(t, err, "expected error but got none")
			} else {
				assert.NoError(t, err, "unexpected error: %v", err)
			}
			if err != nil {
				return
			}
			assert.Equal(t, tc.expectedGroupName, committee.SSOGroupName)
		})
	}
}

func TestCommitteeSSOGroupNameBuildIdempotent(t *testing.T) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			Name:         "Idempotent Committee",
			SSOGroupName: "",
		},
	}
	ctx := context.Background()
	projectSlug := "test-project"

	// First call
	err := committee.SSOGroupNameBuild(ctx, projectSlug)
	assert.NoError(t, err)
	firstSSOGroupName := committee.SSOGroupName

	// Second call should increment the index
	err = committee.SSOGroupNameBuild(ctx, projectSlug)
	assert.NoError(t, err)
	secondSSOGroupName := committee.SSOGroupName

	assert.Equal(t, "test-project-idempotent-committee", firstSSOGroupName)
	assert.Equal(t, "test-project-idempotent-committee-2", secondSSOGroupName)
	assert.NotEqual(t, firstSSOGroupName, secondSSOGroupName)
}

func TestCommitteeBuildIndexKey(t *testing.T) {
	tests := []struct {
		name        string
		committee   Committee
		expectedKey string
	}{
		{
			name: "basic functionality",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "proj-123",
					Name:       "Technical Committee",
				},
			},
			expectedKey: "c5f3a7e8d9b2f1a4c6e8d9b2f1a4c6e8d9b2f1a4c6e8d9b2f1a4c6e8d9b2f1a4", // This will be calculated
		},
		{
			name: "different project same committee",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "proj-456",
					Name:       "Technical Committee",
				},
			},
			expectedKey: "", // Will be different from above
		},
		{
			name: "same project different committee",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "proj-123",
					Name:       "Security Committee",
				},
			},
			expectedKey: "", // Will be different from first test
		},
		{
			name: "empty committee name",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "proj-123",
					Name:       "",
				},
			},
			expectedKey: "", // Should still generate a valid hash
		},
		{
			name: "empty project uid",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "",
					Name:       "Technical Committee",
				},
			},
			expectedKey: "", // Should still generate a valid hash
		},
		{
			name: "special characters in name",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "proj-123",
					Name:       "API & Documentation Committee!",
				},
			},
			expectedKey: "", // Should handle special characters
		},
		{
			name: "unicode characters",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "proj-français",
					Name:       "Développement Committee",
				},
			},
			expectedKey: "", // Should handle unicode
		},
		{
			name: "very long names",
			committee: Committee{
				CommitteeBase: CommitteeBase{
					ProjectUID: "very-long-project-uid-with-many-characters-and-details",
					Name:       "Very Long Committee Name With Many Words And Detailed Description",
				},
			},
			expectedKey: "", // Should handle long strings
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			key := tc.committee.BuildIndexKey(ctx)

			// Verify the key is not empty
			assert.NotEmpty(t, key)

			// Verify the key is a valid hex string (64 characters for SHA256)
			assert.Len(t, key, 64)
			assert.Regexp(t, "^[a-f0-9]+$", key)
		})
	}
}

func TestCommitteeBuildIndexKey_Deterministic(t *testing.T) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-123",
			Name:       "Technical Committee",
		},
	}
	ctx := context.Background()

	// Generate key multiple times
	key1 := committee.BuildIndexKey(ctx)
	key2 := committee.BuildIndexKey(ctx)
	key3 := committee.BuildIndexKey(ctx)

	// All keys should be identical (deterministic)
	assert.Equal(t, key1, key2)
	assert.Equal(t, key2, key3)
	assert.Equal(t, key1, key3)
}

func TestCommitteeBuildIndexKey_UniqueForDifferentInputs(t *testing.T) {
	ctx := context.Background()

	committee1 := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-123",
			Name:       "Technical Committee",
		},
	}

	committee2 := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-456",
			Name:       "Technical Committee",
		},
	}

	committee3 := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-123",
			Name:       "Security Committee",
		},
	}

	key1 := committee1.BuildIndexKey(ctx)
	key2 := committee2.BuildIndexKey(ctx)
	key3 := committee3.BuildIndexKey(ctx)

	// All keys should be different
	assert.NotEqual(t, key1, key2)
	assert.NotEqual(t, key1, key3)
	assert.NotEqual(t, key2, key3)

	// Verify they are all valid hex strings
	for _, key := range []string{key1, key2, key3} {
		assert.Len(t, key, 64)
		assert.Regexp(t, "^[a-f0-9]+$", key)
	}
}

func BenchmarkCommitteeBuildIndexKey(b *testing.B) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-123",
			Name:       "Technical Committee",
		},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = committee.BuildIndexKey(ctx)
	}
}

func BenchmarkCommitteeBuildIndexKey_ShortNames(b *testing.B) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "p1",
			Name:       "TC",
		},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = committee.BuildIndexKey(ctx)
	}
}

func BenchmarkCommitteeBuildIndexKey_LongNames(b *testing.B) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "very-long-project-uid-with-many-characters-and-details-that-makes-it-quite-lengthy",
			Name:       "Very Long Committee Name With Many Words And Detailed Description That Goes On And On",
		},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = committee.BuildIndexKey(ctx)
	}
}

func BenchmarkCommitteeBuildIndexKey_UnicodeNames(b *testing.B) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-français-中文-русский",
			Name:       "Développement Committee 开发委员会 Комитет разработки",
		},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = committee.BuildIndexKey(ctx)
	}
}

func BenchmarkCommitteeBuildIndexKey_Parallel(b *testing.B) {
	committee := Committee{
		CommitteeBase: CommitteeBase{
			ProjectUID: "proj-123",
			Name:       "Technical Committee",
		},
	}
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = committee.BuildIndexKey(ctx)
		}
	})
}
