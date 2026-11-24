// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// listMembersReaderStub overrides stubCommitteeReader's GetBase and ListMembersByCommittee
// so TestListCommitteeMembers can drive both the existence check and the roster scan
// independently; every other method keeps stubCommitteeReader's "not used" panic.
// baseCalls/memberCalls and their own captured UIDs are tracked separately so a test
// can tell the two calls apart, e.g. asserting the member scan never ran.
type listMembersReaderStub struct {
	*stubCommitteeReader
	base        *model.CommitteeBase
	baseErr     error
	members     []*model.CommitteeMember
	membersErr  error
	baseCalls   int
	baseUID     string
	memberCalls int
	memberUID   string
}

func (r *listMembersReaderStub) GetBase(_ context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	r.baseCalls++
	r.baseUID = uid
	return r.base, 1, r.baseErr
}

func (r *listMembersReaderStub) ListMembersByCommittee(_ context.Context, uid string) ([]*model.CommitteeMember, error) {
	r.memberCalls++
	r.memberUID = uid
	return r.members, r.membersErr
}

func TestListCommitteeMembers(t *testing.T) {
	member := func(uid string) *model.CommitteeMember {
		return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          uid,
			CommitteeUID: "committee-123",
			Email:        "first.last@example.com",
			FirstName:    "First",
			LastName:     "Last",
		}}
	}

	tests := []struct {
		name            string
		reader          *listMembersReaderStub
		expectError     bool
		expectedError   string
		expectedLen     int
		expectMemberUID string
	}{
		{
			name: "returns every member of the committee",
			reader: &listMembersReaderStub{
				base:    &model.CommitteeBase{UID: "committee-123"},
				members: []*model.CommitteeMember{member("member-1"), member("member-2")},
			},
			expectedLen:     2,
			expectMemberUID: "member-1",
		},
		{
			name: "empty roster returns an empty slice, not an error",
			reader: &listMembersReaderStub{
				base:    &model.CommitteeBase{UID: "committee-123"},
				members: nil,
			},
			expectedLen: 0,
		},
		{
			name: "committee not found short-circuits before the member scan",
			reader: &listMembersReaderStub{
				baseErr: errs.NewNotFound("committee not found"),
			},
			expectError:   true,
			expectedError: "committee not found",
		},
		{
			name: "member scan failure is propagated",
			reader: &listMembersReaderStub{
				base:       &model.CommitteeBase{UID: "committee-123"},
				membersErr: errs.NewUnexpected("storage unavailable", nil),
			},
			expectError:   true,
			expectedError: "storage unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{committeeReaderOrchestrator: tt.reader}

			res, err := svc.ListCommitteeMembers(context.Background(), &committeeservice.ListCommitteeMembersPayload{
				UID: "committee-123",
			})

			require.Equal(t, 1, tt.reader.baseCalls)
			require.Equal(t, "committee-123", tt.reader.baseUID)

			if tt.expectError {
				assertGoaErrContains(t, err, tt.expectedError)
				if tt.reader.baseErr != nil {
					assert.Zero(t, tt.reader.memberCalls, "member scan must not run when the committee lookup fails")
				}
				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, tt.reader.memberCalls)
			require.Equal(t, "committee-123", tt.reader.memberUID)
			require.NotNil(t, res)
			require.Len(t, res, tt.expectedLen)

			if tt.expectMemberUID != "" {
				require.NotEmpty(t, res)
				assert.Equal(t, tt.expectMemberUID, *res[0].UID)
				assert.Equal(t, "committee-123", *res[0].CommitteeUID)
				assert.Equal(t, "first.last@example.com", *res[0].Email)
			}
		})
	}
}
