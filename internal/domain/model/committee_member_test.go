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
					Email:    "test@example.com",
					Username: "testuser",
					Organization: CommitteeMemberOrganization{
						Name: "Test Org",
					},
				},
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
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
				},
			},
			committee:     committeeWithBusinessEmail,
			expectError:   true,
			expectedError: "organization id or organization name and domain are required when business email is required or voting is enabled",
		},
		{
			name: "business email required - org id satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						ID: "org-123",
					},
				},
			},
			committee:   committeeWithBusinessEmail,
			expectError: false,
		},
		{
			name: "business email required - org name and domain satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						Name:    "Acme Corp",
						Website: "https://acme.com",
					},
				},
			},
			committee:   committeeWithBusinessEmail,
			expectError: false,
		},
		{
			name: "business email required - org name only is insufficient",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						Name: "Acme Corp",
					},
				},
			},
			committee:     committeeWithBusinessEmail,
			expectError:   true,
			expectedError: "organization id or organization name and domain are required when business email is required or voting is enabled",
		},
		{
			name: "voting enabled - missing org info",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
				},
			},
			committee:     committeeWithVoting,
			expectError:   true,
			expectedError: "organization id or organization name and domain are required when business email is required or voting is enabled",
		},
		{
			name: "voting enabled - org id satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						ID: "org-456",
					},
				},
			},
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name: "voting enabled - org name and domain satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						Name:    "Acme Corp",
						Website: "https://acme.com",
					},
				},
			},
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name: "both flags set - org id satisfies requirement",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						ID: "org-789",
					},
				},
			},
			committee:   committeeWithBoth,
			expectError: false,
		},
		{
			name: "no restrictions - no org info is fine",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@example.com",
				},
			},
			committee:   nonGacCommittee,
			expectError: false,
		},
		{
			name: "voting enabled - voting status None is rejected",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						ID: "org-456",
					},
					Voting: CommitteeMemberVotingInfo{
						Status: "None",
					},
				},
			},
			committee:     committeeWithVoting,
			expectError:   true,
			expectedError: "voting_status \"None\" is not allowed on voting-enabled committees",
		},
		{
			name: "voting enabled - valid voting status is accepted",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@corp.com",
					Organization: CommitteeMemberOrganization{
						ID: "org-456",
					},
					Voting: CommitteeMemberVotingInfo{
						Status: "Voting Rep",
					},
				},
			},
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name: "voting disabled - voting status None is allowed",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "user@example.com",
					Voting: CommitteeMemberVotingInfo{
						Status: "None",
					},
				},
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
					Email:        "test@example.com",
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"username:testuser",
				"email:test@example.com",
			},
		},
		{
			name: "member with voting status",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Username:     "testuser",
					Email:        "test@example.com",
					Voting: CommitteeMemberVotingInfo{
						Status: "Voting Rep",
					},
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"username:testuser",
				"email:test@example.com",
				"voting_status:Voting Rep",
			},
		},
		{
			name: "member with partial fields",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Email:        "test@example.com",
					// Missing Username, and Voting.Status
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"email:test@example.com",
			},
		},
		{
			name: "member with only email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Email:        "test@example.com",
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"email:test@example.com",
			},
		},
		{
			name: "member with organization information",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Email:        "test@example.com",
					Organization: CommitteeMemberOrganization{
						ID:      "org-789",
						Name:    "The Linux Foundation",
						Website: "https://linuxfoundation.org",
					},
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"email:test@example.com",
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
					Email:        "test@example.com",
					Organization: CommitteeMemberOrganization{
						ID:   "org-789",
						Name: "The Linux Foundation",
						// Missing Website
					},
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"email:test@example.com",
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
					Email:        "test@example.com",
					ProjectUID:   "cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
					ProjectSlug:  "test-project",
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"project_uid:cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
				"project_slug:test-project",
				"email:test@example.com",
			},
		},
		{
			name: "member with project uid only",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					UID:          "member-123",
					CommitteeUID: "committee-456",
					Email:        "test@example.com",
					ProjectUID:   "cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
					// Missing ProjectSlug
				},
			},
			expected: []string{
				"member-123",
				"committee_member_uid:member-123",
				"committee_uid:committee-456",
				"project_uid:cbef1ed5-17dc-4a50-84e2-6cddd70f6878",
				"email:test@example.com",
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
					Email:        "test@example.com",
				},
			},
			// SHA-256 of "committee-123|test@example.com"
			expected: "c7c8e1a1e1e8e6c8a6b8f5c7e1e8e6c8a6b8f5c7e1e8e6c8a6b8f5c7e1e8e6c8",
		},
		{
			name: "different committee same email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "committee-456",
					Email:        "test@example.com",
				},
			},
			// Should produce different hash than above
			expected: "different-hash-expected",
		},
		{
			name: "same committee different email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "committee-123",
					Email:        "different@example.com",
				},
			},
			// Should produce different hash than first test
			expected: "another-different-hash-expected",
		},
		{
			name: "empty fields",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeUID: "",
					Email:        "",
				},
			},
			// SHA-256 of "|"
			expected: "hash-of-empty-fields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.member.BuildIndexKey(ctx)

			// Check that result is a valid SHA-256 hash (64 hex characters)
			if len(result) != 64 {
				t.Errorf("BuildIndexKey() returned hash with length %d, expected 64", len(result))
			}

			// Check that it's a valid hex string
			for _, r := range result {
				if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
					t.Errorf("BuildIndexKey() returned non-hex character: %c", r)
				}
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
			Email:        "test@example.com",
		},
	}

	member2 := &CommitteeMember{
		CommitteeMemberBase: CommitteeMemberBase{
			CommitteeUID: "committee-456",
			Email:        "test@example.com",
		},
	}

	member3 := &CommitteeMember{
		CommitteeMemberBase: CommitteeMemberBase{
			CommitteeUID: "committee-123",
			Email:        "different@example.com",
		},
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

func TestCommitteeMember_ValidateUpdate(t *testing.T) {
	committeeWithVoting := &Committee{
		CommitteeBase:     CommitteeBase{Category: "Other", EnableVoting: true},
		CommitteeSettings: &CommitteeSettings{},
	}

	nonVotingCommittee := &Committee{
		CommitteeBase:     CommitteeBase{Category: "Other"},
		CommitteeSettings: &CommitteeSettings{},
	}

	validMember := func(status string) *CommitteeMember {
		return &CommitteeMember{
			CommitteeMemberBase: CommitteeMemberBase{
				Email: "user@corp.com",
				Organization: CommitteeMemberOrganization{
					ID: "org-123",
				},
				Voting: CommitteeMemberVotingInfo{Status: status},
			},
		}
	}

	tests := []struct {
		name          string
		incoming      *CommitteeMember
		existing      *CommitteeMember
		committee     *Committee
		expectError   bool
		expectedError string
	}{
		{
			name:          "voting enabled - valid existing, incoming None is rejected",
			incoming:      validMember("None"),
			existing:      validMember("Voting Rep"),
			committee:     committeeWithVoting,
			expectError:   true,
			expectedError: "voting_status \"None\" is not allowed on voting-enabled committees",
		},
		{
			name:        "voting enabled - legacy existing None, incoming valid is allowed",
			incoming:    validMember("Voting Rep"),
			existing:    validMember("None"),
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name:        "voting enabled - legacy existing None, keeping None is allowed",
			incoming:    validMember("None"),
			existing:    validMember("None"),
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name:        "voting enabled - valid to valid transition is allowed",
			incoming:    validMember("Observer"),
			existing:    validMember("Voting Rep"),
			committee:   committeeWithVoting,
			expectError: false,
		},
		{
			name:        "voting disabled - any status is allowed",
			incoming:    validMember("None"),
			existing:    validMember("Voting Rep"),
			committee:   nonVotingCommittee,
			expectError: false,
		},
		{
			name:          "nil incoming member",
			incoming:      nil,
			existing:      validMember("Voting Rep"),
			committee:     committeeWithVoting,
			expectError:   true,
			expectedError: "committee member cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.incoming.ValidateUpdate(tt.committee, tt.existing)

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

func TestCommitteeMember_NeedsSyncWith(t *testing.T) {
	base := &CommitteeBase{
		Name:        "TSC Committee",
		Category:    "Board",
		ProjectUID:  "proj-uid-123",
		ProjectSlug: "my-project",
	}

	tests := []struct {
		name      string
		member    *CommitteeMember
		committee *CommitteeBase
		want      bool
	}{
		{
			name: "nil committee — no sync needed",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "TSC Committee",
					CommitteeCategory: "Board",
					ProjectUID:        "proj-uid-123",
					ProjectSlug:       "my-project",
				},
			},
			committee: nil,
			want:      false,
		},
		{
			name: "all fields match — no sync needed",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "TSC Committee",
					CommitteeCategory: "Board",
					ProjectUID:        "proj-uid-123",
					ProjectSlug:       "my-project",
				},
			},
			committee: base,
			want:      false,
		},
		{
			name: "name differs",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "Old Name",
					CommitteeCategory: "Board",
					ProjectUID:        "proj-uid-123",
					ProjectSlug:       "my-project",
				},
			},
			committee: base,
			want:      true,
		},
		{
			name: "category differs",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "TSC Committee",
					CommitteeCategory: "Other",
					ProjectUID:        "proj-uid-123",
					ProjectSlug:       "my-project",
				},
			},
			committee: base,
			want:      true,
		},
		{
			name: "project_uid differs",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "TSC Committee",
					CommitteeCategory: "Board",
					ProjectUID:        "old-proj-uid",
					ProjectSlug:       "my-project",
				},
			},
			committee: base,
			want:      true,
		},
		{
			name: "project_slug differs",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "TSC Committee",
					CommitteeCategory: "Board",
					ProjectUID:        "proj-uid-123",
					ProjectSlug:       "old-slug",
				},
			},
			committee: base,
			want:      true,
		},
		{
			name: "multiple fields differ",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					CommitteeName:     "Old Name",
					CommitteeCategory: "Other",
					ProjectUID:        "old-proj-uid",
					ProjectSlug:       "old-slug",
				},
			},
			committee: base,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.member.NeedsSyncWith(tt.committee)
			if got != tt.want {
				t.Errorf("NeedsSyncWith() = %v, want %v", got, tt.want)
			}
		})
	}
}
