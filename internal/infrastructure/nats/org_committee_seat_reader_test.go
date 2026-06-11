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

// stubBaseReader implements port.CommitteeBaseReader for the project_uid fallback path. It maps a
// committee UID to a project UID; GetBase counts calls so tests can assert per-committee caching.
type stubBaseReader struct {
	port.CommitteeBaseReader
	byUID map[string]string
	err   error
	calls int
}

func (s *stubBaseReader) GetBase(_ context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	s.calls++
	if s.err != nil {
		return nil, 0, s.err
	}
	projectUID, ok := s.byUID[uid]
	if !ok {
		return nil, 0, errors.New("committee not found")
	}
	return &model.CommitteeBase{UID: uid, ProjectUID: projectUID}, 1, nil
}

func seatMember(uid, projectUID string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: uid, ProjectUID: projectUID}}
}

func seatMemberOnCommittee(uid, projectUID, committeeUID string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: uid, ProjectUID: projectUID, CommitteeUID: committeeUID}}
}

// TestNATSOrgCommitteeSeatReader_StorageErrorPropagates verifies that a storage-layer error from
// ListMembersByOrganization is propagated unchanged (not swallowed) by the reader.
func TestNATSOrgCommitteeSeatReader_StorageErrorPropagates(t *testing.T) {
	sentinel := errors.New("kv store unavailable")
	stub := &stubMemberReader{err: sentinel}
	reader := NewNATSOrgCommitteeSeatReader(stub, &stubBaseReader{})

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
	reader := NewNATSOrgCommitteeSeatReader(&stubMemberReader{members: members}, &stubBaseReader{})
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

// TestNATSOrgCommitteeSeatReader_ProjectUIDFallback verifies that a member whose own project_uid is
// empty (legacy record, pre-LFXV2-1442) is recovered from its committee's project_uid, included in the
// family filter, and enriched on a shallow copy — while the committee read is cached per committee and a lookup
// failure degrades to exclusion (the pre-fallback behavior) rather than failing the whole list.
func TestNATSOrgCommitteeSeatReader_ProjectUIDFallback(t *testing.T) {
	const org = "001B000000IqhSLIAZ"

	t.Run("empty member project_uid is recovered from the committee and enriched", func(t *testing.T) {
		members := []*model.CommitteeMember{
			seatMemberOnCommittee("m-empty-1", "", "c-1"), // belongs to family via committee c-1 → p-1
			seatMemberOnCommittee("m-empty-2", "", "c-1"), // same committee → served from cache
			seatMemberOnCommittee("m-other", "", "c-2"),   // committee c-2 → p-2, outside family
			seatMember("m-populated", "p-1"),              // already correct, no lookup needed
		}
		base := &stubBaseReader{byUID: map[string]string{"c-1": "p-1", "c-2": "p-2"}}
		reader := NewNATSOrgCommitteeSeatReader(&stubMemberReader{members: members}, base)

		got, err := reader.ListOrgCommitteeSeats(context.Background(), org, []string{"p-1"})
		require.NoError(t, err)
		require.Len(t, got, 3)

		uids := map[string]string{}
		for _, m := range got {
			uids[m.UID] = m.ProjectUID
		}
		assert.Equal(t, "p-1", uids["m-empty-1"], "recovered project_uid must be set on the enriched copy")
		assert.Equal(t, "p-1", uids["m-empty-2"])
		assert.Equal(t, "p-1", uids["m-populated"])
		assert.NotContains(t, uids, "m-other", "committee outside the family stays excluded")
		assert.Equal(t, 2, base.calls, "committee base read must be cached per committee (c-1 once, c-2 once)")
	})

	t.Run("committee lookup failure degrades to exclusion, not a list error", func(t *testing.T) {
		members := []*model.CommitteeMember{
			seatMemberOnCommittee("m-empty", "", "c-broken"),
			seatMember("m-populated", "p-1"),
		}
		base := &stubBaseReader{err: errors.New("committee kv unavailable")}
		reader := NewNATSOrgCommitteeSeatReader(&stubMemberReader{members: members}, base)

		got, err := reader.ListOrgCommitteeSeats(context.Background(), org, []string{"p-1"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "m-populated", got[0].UID)
		assert.Equal(t, 1, base.calls, "the failed committee is negative-cached, not re-fetched")
	})
}
