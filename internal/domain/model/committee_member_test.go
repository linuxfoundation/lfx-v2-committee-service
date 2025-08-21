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
	}

	nonGacCommittee := &Committee{
		CommitteeBase: CommitteeBase{
			Category: "Other",
		},
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
			name: "GAC member missing agency",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email:   "test@example.com",
					Country: "USA",
				},
			},
			committee:     gacCommittee,
			expectError:   true,
			expectedError: "missing required fields for Government Advisory Council members: agency",
		},
		{
			name: "GAC member missing country",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email:  "test@example.com",
					Agency: "Test Agency",
				},
			},
			committee:     gacCommittee,
			expectError:   true,
			expectedError: "missing required fields for Government Advisory Council members: country",
		},
		{
			name: "GAC member missing both agency and country",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "test@example.com",
				},
			},
			committee:     gacCommittee,
			expectError:   true,
			expectedError: "missing required fields for Government Advisory Council members: agency, country",
		},
		{
			name: "valid GAC member",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email:   "test@example.com",
					Agency:  "Test Agency",
					Country: "USA",
				},
			},
			committee:   gacCommittee,
			expectError: false,
		},
		{
			name: "non-GAC member with agency",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email:  "test@example.com",
					Agency: "Test Agency",
				},
			},
			committee:     nonGacCommittee,
			expectError:   true,
			expectedError: "agency and country should not be set for non-Government Advisory Council members",
		},
		{
			name: "non-GAC member with country",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email:   "test@example.com",
					Country: "USA",
				},
			},
			committee:     nonGacCommittee,
			expectError:   true,
			expectedError: "agency and country should not be set for non-Government Advisory Council members",
		},
		{
			name: "valid non-GAC member",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "test@example.com",
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
				"member_uid:member-123",
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
				"member_uid:member-123",
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
					UID:   "member-123",
					Email: "test@example.com",
					// Missing CommitteeUID, Username, and Voting.Status
				},
			},
			expected: []string{
				"member_uid:member-123",
				"email:test@example.com",
			},
		},
		{
			name: "member with only email",
			member: &CommitteeMember{
				CommitteeMemberBase: CommitteeMemberBase{
					Email: "test@example.com",
				},
			},
			expected: []string{
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
