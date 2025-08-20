// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"errors"
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
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}
