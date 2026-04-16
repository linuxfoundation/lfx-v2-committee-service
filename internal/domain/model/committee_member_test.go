// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"errors"
	"reflect"
	"testing"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func TestCommitteeMember_Validate(t *testing.T) {
	// Create test committees
	gacCommittee := &Committee{
		CommitteeBase: CommitteeBase{
			Category: categoryGovernmentAdvisoryCouncil,
		},
		CommitteeSettings: &CommitteeSettings{},
	}

	nonGacCommittee := &Committee{
		CommitteeBase:     CommitteeBase{Category: "Other"},
		CommitteeSettings: &CommitteeSettings{},
	}

	committeeWithBusinessEmail := &Committee{
		CommitteeBase:     CommitteeBase{Category: "Other"},
		CommitteeSettings: &CommitteeSettings{BusinessEmailRequired: true},
	}

	committeeWithVoting := &Committee{
		CommitteeBase:     CommitteeBase{Category: "Other", EnableVoting: true},
		CommitteeSettings: &CommitteeSettings{},
	}

	committeeWithBoth := &Committee{
		CommitteeBase:     CommitteeBase{Category: "Other", EnableVoting: true},
		CommitteeSettings: &CommitteeSettings{BusinessEmailRequired: true},
	}

	tests := []struct {
		name          string
		member        *CommitteeMember
		committee     *Committee
		expectError   bool
		expectedError string
	}{
		{
			name:          "nil member",
			member:        nil,
			committee:     gacCommittee,
			expectError:   true,
			expectedError: "committee member cannot be nil",
		},
		{
			name: "nil committee",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Username: "testuser",
					Organization: CommitteeMemberOrganization{
						Name: "Test Org",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			committee:     nil,
			expectError:   true,
			expectedError: "committee cannot be nil",
		},
		{
			name: "missing email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Username: "testuser",
					Organization: CommitteeMemberOrganization{
						Name: "Test Org",
					},
				},
			},
			committee:     nonGacCommittee,
			expectError:   true,
			expectedError: "email is required",
		},
		{
			name: "business email required - missing org info",
			member: &CommitteeMember{
				CommitteeMemberBase:      CommitteeMemberBase{},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:     committeeWithBusinessEmail,
			expectError:   true,
			expectedError: "organization id or organization name and domain are required when business email is required or voting is enabled",
		},
		{
			name: "business email required - org id satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Organization: CommitteeMemberOrganization{
						ID: "org-123",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:   committeeWithBusinessEmail,
			expectError: false,
		},
		{
			name: "business email required - org name and domain satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Organization: CommitteeMemberOrganization{
						Name:    "Acme Corp",
						Website: "https://acme.com",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:   committeeWithBusinessEmail,
			expectError: false,
		},
		{
			name: "business email required - org name only is insufficient",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Organization: CommitteeMemberOrganization{
						Name: "Acme Corp",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:     committeeWithBusinessEmail,
			expectError:   true,
			expectedError: "organization id or organization name and domain are required when business email is required or voting is enabled",
		},
		{
			name: "voting enabled - missing org info",
			member: &CommitteeMember{
				CommitteeMemberBase:      CommitteeMemberBase{},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:     committeeWithVoting,
			expectError:   true,
			expectedError: "organization id or organization name and domain are required when business email is required or voting is enabled",
		},
		{
			name: "voting enabled - org id satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Organization: CommitteeMemberOrganization{
						ID: "org-456",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name: "voting enabled - org name and domain satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Organization: CommitteeMemberOrganization{
						Name:    "Acme Corp",
						Website: "https://acme.com",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name: "both flags set - org id satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Organization: CommitteeMemberOrganization{
						ID: "org-789",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@corp.com"},
			},
			committee:   committeeWithBoth,
			expectError: false,
		},
		{
			name: "no restrictions - no org info is fine",
			member: &CommitteeMember{
				CommitteeMemberBase:      CommitteeMemberBase{},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "user@example.com"},
			},
			committee:   nonGacCommittee,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.member.Validate(tt.committee)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}

				var validationErr errs.Validation
				if !errors.As(err, &validationErr) {
					t.Errorf("expected validation error, got %T: %v", err, err)
					return
				}

				if err.Error() != tt.expectedError {
					t.Errorf("expected error %q, got %q", tt.expectedError, err.Error())
				}
			} else if err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

func TestCommitteeMember_Tags(t *testing.T) {
	tests := []struct {
		name     string
		member   *CommitteeMember
		expected []string
	}{
		{
			name:     "nil member",
			member:   nil,
			expected: nil,
		},
		{
			name: "empty member",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{},
			},
			expected: nil,
		},
		{
			name: "member with basic fields",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Username:     "testuser",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"username:testuser",
			},
		},
		{
			name: "member with voting status",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Username:     "testuser",
					Voting: CommitteeMemberVotingInfo{
						Status: "Voting Rep",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"username:testuser",
				"voting_status:Voting Rep",
			},
		},
		{
			name: "member with partial fields",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					// Missing Username, and Voting.Status
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
			},
		},
		{
			name: "member with only email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
			},
		},
		{
			name: "member with organization information",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Organization: CommitteeMemberOrganization{
						ID:      "org-789",
						Name:    "The Linux Foundation",
						Website: "https://linuxfoundation.org",
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"organization_id:org-789",
				"organization_name:The Linux Foundation",
				"organization_website:https://linuxfoundation.org",
			},
		},
		{
			name: "member with partial organization information",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Organization: CommitteeMemberOrganization{
						ID:   "org-789",
						Name: "The Linux Foundation",
						// Missing Website
					},
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"organization_id:org-789",
				"organization_name:The Linux Foundation",
			},
		},
		{
			name: "member with project uid and slug",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					ProjectUID:   "cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
					ProjectSlug:  "test-project",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"project_uid:cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
				"project_slug:test-project",
			},
		},
		{
			name: "member with project uid only",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					ProjectUID:   "cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
					// Missing ProjectSlug
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"project_uid:cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.member.Tags()

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Tags() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCommitteeMember_BuildIndexKey(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		member   *CommitteeMember
		expected string
	}{
		{
			name: "basic member",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "committee-123",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			// SHA-256 of "committee-123|test@example.com"
			expected: "93548eeb4f04488dfe77d98d56f0642fff5e1c9637314866d07e9f289cc4343a",
		},
		{
			name: "different committee same email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "committee-456",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
			},
			// SHA-256 of "committee-456|test@example.com"
			expected: "5281cc60c1a073d75d5acf4ced91d0d454fc9889d9e6fd2606b31b82b5e49c4c",
		},
		{
			name: "same committee different email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "committee-123",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "different@example.com"},
			},
			// SHA-256 of "committee-123|different@example.com"
			expected: "ea78a994c26a7504ee1329d68db5081cda3eae3cad7e55a86f3d9ce981c5912f",
		},
		{
			name: "empty fields",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "",
				},
				CommitteeMemberSensitive: CommitteeMemberSensitive{Email: ""},
			},
			// SHA-256 of "|"
			expected: "cbe5cfdf7c2118a9c3d78ef1d684f3afa089201352886449a06a6511cfef74a7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.member.BuildIndexKey(ctx)

			if result != tt.expected {
				t.Errorf("BuildIndexKey() = %s, want %s", result, tt.expected)
			}

			// Test consistency - same input should produce same hash
			result2 := tt.member.BuildIndexKey(ctx)
			if result != result2 {
				t.Errorf("BuildIndexKey() is not consistent: first call = %s, second call = %s", result, result2)
			}
		})
	}
}

func TestCommitteeMember_BuildIndexKey_Uniqueness(t *testing.T) {
	ctx := context.Background()

	member1 := &CommitteeMember{
		CommitteeMemberBase: CommitteeMemberBase{
			CommitteeUID: "committee-123",
		},
		CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
	}

	member2 := &CommitteeMember{
		CommitteeMemberBase: CommitteeMemberBase{
			CommitteeUID: "committee-456",
		},
		CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "test@example.com"},
	}

	member3 := &CommitteeMember{
		CommitteeMemberBase: CommitteeMemberBase{
			CommitteeUID: "committee-123",
		},
		CommitteeMemberSensitive: CommitteeMemberSensitive{Email: "different@example.com"},
	}

	key1 := member1.BuildIndexKey(ctx)
	key2 := member2.BuildIndexKey(ctx)
	key3 := member3.BuildIndexKey(ctx)

	// All keys should be different
	if key1 == key2 {
		t.Errorf("Expected different keys for different committees, but got same key: %s", key1)
	}

	if key1 == key3 {
		t.Errorf("Expected different keys for different emails, but got same key: %s", key1)
	}

	if key2 == key3 {
		t.Errorf("Expected different keys for different committee/email combinations, but got same key: %s", key2)
	}
}
