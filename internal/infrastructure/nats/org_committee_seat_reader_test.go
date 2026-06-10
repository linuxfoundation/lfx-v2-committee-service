// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// stubMemberReader implements port.CommitteeMemberReader for the org-seat reader tests by overriding
// only ListMembersByOrganization (the sole method natsOrgCommitteeSeatReader uses). The embedded
// interface is nil, so any other method call panics — surfacing unexpected reads in the test.
type stubMemberReader struct {
	port.CommitteeMemberReader
	members []*model.CommitteeMember
	err     error
	gotOrg  string
}

func (s *stubMemberReader) ListMembersByOrganization(_ context.Context, orgSFID string) ([]*model.CommitteeMember, error) {
	s.gotOrg = orgSFID
	return s.members, s.err
}

func seatMember(uid, projectUID string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: uid, ProjectUID: projectUID}}
}

// TestNATSOrgCommitteeSeatReader_StorageErrorPropagates verifies that a storage-layer error from
// ListMembersByOrganization is propagated unchanged (not swallowed) by the reader.
func TestNATSOrgCommitteeSeatReader_StorageErrorPropagates(t *testing.T) {
	sentinel := errors.New("kv store unavailable")
	stub := &stubMemberReader{err: sentinel}
	reader := NewNATSOrgCommitteeSeatReader(stub)

	got, err := reader.ListOrgCommitteeSeats(context.Background(), "001B000000IqhSLIAZ", []string{"p-1"})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Nil(t, got)
	assert.Equal(t, "001B000000IqhSLIAZ", stub.gotOrg, "org SFID must be forwarded to the storage layer")
}

// TestNATSOrgCommitteeSeatReader_ProjectFamilyFilter verifies the project-family filter, including the
// documented "empty entries are unfiltered" behavior (?project_uids= and nil both mean org-only scope).
func TestNATSOrgCommitteeSeatReader_ProjectFamilyFilter(t *testing.T) {
	members := []*model.CommitteeMember{
		seatMember("m-1", "p-1"),
		seatMember("m-2", "p-2"),
		nil, // nil entries must be skipped without panicking
	}
	reader := NewNATSOrgCommitteeSeatReader(&stubMemberReader{members: members})
	const org = "001B000000IqhSLIAZ"

	t.Run("nil project_uids is unfiltered (org-only scope)", func(t *testing.T) {
		got, err := reader.ListOrgCommitteeSeats(context.Background(), org, nil)
		require.NoError(t, err)
		assert.Len(t, got, 3) // returned as-is (no filtering applied)
	})

	t.Run("all-empty project_uids is unfiltered (org-only scope)", func(t *testing.T) {
		got, err := reader.ListOrgCommitteeSeats(context.Background(), org, []string{"", ""})
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("non-empty project_uids narrows to the family (empty entries ignored)", func(t *testing.T) {
		got, err := reader.ListOrgCommitteeSeats(context.Background(), org, []string{"", "p-1"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "m-1", got[0].UID)
	})
}
