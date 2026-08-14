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
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

type stubB2BOrgResolver struct {
	sfid string
	ok   bool
	err  error
}

func (s stubB2BOrgResolver) ResolveSFID(_ context.Context, _, _ string) (string, bool, error) {
	return s.sfid, s.ok, s.err
}

func testMemberCDPOrgIDCmd(resolver b2bOrgSFIDResolver, memberUIDs []string) *memberCDPOrgIDSubcommand {
	return &memberCDPOrgIDSubcommand{resolver: resolver, memberUIDs: memberUIDs}
}

type stubCommitteeWriter struct {
	updated []*model.CommitteeMember
}

func (s *stubCommitteeWriter) Create(_ context.Context, _ *model.Committee, _ bool) (*model.Committee, error) {
	return nil, nil
}
func (s *stubCommitteeWriter) Update(_ context.Context, _ *model.Committee, _ uint64, _ bool) (*model.Committee, error) {
	return nil, nil
}
func (s *stubCommitteeWriter) UpdateSettings(_ context.Context, _ *model.CommitteeSettings, _ uint64, _ bool) (*model.CommitteeSettings, error) {
	return nil, nil
}
func (s *stubCommitteeWriter) Delete(_ context.Context, _ string, _ uint64, _ bool) error { return nil }
func (s *stubCommitteeWriter) CreateMember(_ context.Context, _ *model.CommitteeMember, _ bool, _ bool) (*model.CommitteeMember, error) {
	return nil, nil
}
func (s *stubCommitteeWriter) UpdateMember(_ context.Context, member *model.CommitteeMember, _ uint64, _ bool, _ bool) (*model.CommitteeMember, error) {
	s.updated = append(s.updated, member)
	return member, nil
}
func (s *stubCommitteeWriter) DeleteMember(_ context.Context, _ string, _ uint64, _ bool, _ bool) error {
	return nil
}
func (s *stubCommitteeWriter) ReassignMember(_ context.Context, _ string, _ uint64, _ *model.CommitteeMember, _ bool) (*model.CommitteeMember, error) {
	return nil, nil
}

var _ service.CommitteeWriter = (*stubCommitteeWriter)(nil)

type freshMemberReader struct {
	*mockReader
	fresh *model.CommitteeMember
}

func (r *freshMemberReader) GetMember(_ context.Context, _ string) (*model.CommitteeMember, uint64, error) {
	return r.fresh, 9, nil
}

func TestIsCDPUUID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"001B000000IqhSLIAZ", false},
		{"0014100000Te2ovAAB", false},
		{"51fde723-67df-4e0e-91c6-936d01d59559", true},
		{"4340abc06f4e11f1944c4bb16c3aa46c", true},
		{"111", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, isCDPUUID(tt.in), "input=%q", tt.in)
	}
}

func TestExtractPrimaryDomain(t *testing.T) {
	assert.Equal(t, "linuxfoundation.org", extractPrimaryDomain("https://www.linuxfoundation.org/about"))
	assert.Equal(t, "example.com", extractPrimaryDomain("example.com"))
	assert.Equal(t, "", extractPrimaryDomain(""))
}

func TestMemberCDPOrgID_DryRunCountsResolved(t *testing.T) {
	member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	r := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {member}}}
	w := &stubCommitteeWriter{}

	err := testMemberCDPOrgIDCmd(stubB2BOrgResolver{sfid: "0014100000Te2ovAAB", ok: true}, []string{"m1"}).Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
		Args:                        []string{"--dry-run"},
	})
	require.NoError(t, err)
	assert.Empty(t, w.updated)
}

func TestMemberCDPOrgID_WritesResolvedSFID(t *testing.T) {
	snapshot := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	fresh := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	r := &freshMemberReader{
		mockReader: &mockReader{members: map[string][]*model.CommitteeMember{"c1": {snapshot}}},
		fresh:      fresh,
	}
	w := &stubCommitteeWriter{}

	err := testMemberCDPOrgIDCmd(stubB2BOrgResolver{sfid: "0014100000Te2ovAAB", ok: true}, []string{"m1"}).Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
		Args:                        []string{"--dry-run=false"},
	})
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Equal(t, "0014100000Te2ovAAB", w.updated[0].Organization.ID)
}

func TestMemberCDPOrgID_ResolverErrorIncrementsFailed(t *testing.T) {
	member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	r := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {member}}}
	w := &stubCommitteeWriter{}

	err := testMemberCDPOrgIDCmd(stubB2BOrgResolver{err: assert.AnError}, []string{"m1"}).Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
		Args:                        []string{"--dry-run=false"},
	})
	require.Error(t, err)
	assert.Empty(t, w.updated)
}

// sequentialMemberReader returns snapshot on the first GetMember call and fresh on all
// subsequent calls. This lets tests exercise the stale-resolution guard by simulating
// an org change between the discovery load and the pre-write re-read.
type sequentialMemberReader struct {
	*mockReader
	calls    int
	snapshot *model.CommitteeMember
	fresh    *model.CommitteeMember
}

func (r *sequentialMemberReader) GetMember(_ context.Context, _ string) (*model.CommitteeMember, uint64, error) {
	r.calls++
	if r.calls == 1 {
		return r.snapshot, 1, nil
	}
	return r.fresh, 1, nil
}

func TestMemberCDPOrgID_SkipsWhenFreshOrgChanged(t *testing.T) {
	snapshot := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	// Re-read has a different org name — stale resolution guard should skip the write.
	fresh := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "Different Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	r := &sequentialMemberReader{
		mockReader: &mockReader{},
		snapshot:   snapshot,
		fresh:      fresh,
	}
	w := &stubCommitteeWriter{}

	err := testMemberCDPOrgIDCmd(stubB2BOrgResolver{sfid: "0014100000Te2ovAAB", ok: true}, []string{"m1"}).Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
		Args:                        []string{"--dry-run=false"},
	})
	require.NoError(t, err)
	assert.Empty(t, w.updated, "write should be skipped when fresh org name differs from snapshot")
}

func TestMemberCDPOrgID_ClearUnresolved(t *testing.T) {
	snapshot := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	fresh := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          "m1",
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{
			ID:      "51fde723-67df-4e0e-91c6-936d01d59559",
			Name:    "The Linux Foundation",
			Website: "https://linuxfoundation.org",
		},
	}}
	r := &freshMemberReader{
		mockReader: &mockReader{members: map[string][]*model.CommitteeMember{"c1": {snapshot}}},
		fresh:      fresh,
	}
	w := &stubCommitteeWriter{}

	err := testMemberCDPOrgIDCmd(stubB2BOrgResolver{}, []string{"m1"}).Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
		Args:                        []string{"--dry-run=false", "--clear-unresolved"},
	})
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Empty(t, w.updated[0].Organization.ID)
}
