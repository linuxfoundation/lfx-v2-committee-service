// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// mockMemberWriter is a minimal implementation of port.CommitteeMemberWriter used by
// the backfill subcommand.  Only IndexMemberByCommittee is exercised here.
type mockMemberWriter struct {
	indexed    []string // committee_uid+"."+member_uid keys that were written
	orgIndexed []string // org_sfid+"."+member_uid keys that were written
	indexError error
}

func (w *mockMemberWriter) CreateMember(_ context.Context, _ *model.CommitteeMember) error {
	return nil
}
func (w *mockMemberWriter) UpdateMember(_ context.Context, m *model.CommitteeMember, _ uint64) (*model.CommitteeMember, error) {
	return m, nil
}
func (w *mockMemberWriter) DeleteMember(_ context.Context, _ string, _ uint64) error { return nil }
func (w *mockMemberWriter) UniqueMember(_ context.Context, _ *model.CommitteeMember) (string, error) {
	return "", nil
}
func (w *mockMemberWriter) IndexMemberByCommittee(_ context.Context, m *model.CommitteeMember) (string, error) {
	if w.indexError != nil {
		return "", w.indexError
	}
	key := fmt.Sprintf(constants.KVLookupMembersByCommitteePrefix, m.CommitteeUID, m.UID)
	w.indexed = append(w.indexed, key)
	return key, nil
}
func (w *mockMemberWriter) IndexMemberByOrganization(_ context.Context, m *model.CommitteeMember) (string, error) {
	if w.indexError != nil {
		return "", w.indexError
	}
	orgSFID := utils.NormalizeAccountSFID(m.Organization.ID)
	if orgSFID == "" {
		return "", nil
	}
	key := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, orgSFID, m.UID)
	w.orgIndexed = append(w.orgIndexed, key)
	return key, nil
}

// newBackfillRC builds a RunContext wired with the provided reader and writer mocks.
func newBackfillRC(r *mockReader, w *mockMemberWriter, args ...string) commands.RunContext {
	return commands.RunContext{
		CommitteeReader:       r,
		CommitteeMemberWriter: w,
		Args:                  args,
	}
}

func TestMembersByCommitteeIndex_BackfillAll(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", CommitteeUID: "c1"}},
				{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m2", CommitteeUID: "c1"}},
			},
			"c2": {
				{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m3", CommitteeUID: "c2"}},
			},
		},
	}
	w := &mockMemberWriter{}

	rc := newBackfillRC(r, w)
	err := (&membersByCommitteeIndexSubcommand{}).Run(context.Background(), rc)
	require.NoError(t, err)
	assert.Len(t, w.indexed, 3)
}

func TestMembersByCommitteeIndex_FilterByCommittee(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", CommitteeUID: "c1"}}},
			"c2": {{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m2", CommitteeUID: "c2"}}},
		},
	}
	w := &mockMemberWriter{}

	rc := newBackfillRC(r, w, "--committee-uid=c1")
	err := (&membersByCommitteeIndexSubcommand{}).Run(context.Background(), rc)
	require.NoError(t, err)
	assert.Len(t, w.indexed, 1)
	assert.Contains(t, w.indexed[0], "c1.m1")
}

func TestMembersByCommitteeIndex_DryRun(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", CommitteeUID: "c1"}}},
		},
	}
	w := &mockMemberWriter{}

	rc := newBackfillRC(r, w, "--dry-run")
	err := (&membersByCommitteeIndexSubcommand{}).Run(context.Background(), rc)
	require.NoError(t, err)
	// Dry-run must not write any index entries.
	assert.Empty(t, w.indexed)
}

func TestMembersByCommitteeIndex_IndexError_ReturnsError(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m1", CommitteeUID: "c1"}}},
		},
	}
	w := &mockMemberWriter{indexError: fmt.Errorf("nats unavailable")}

	rc := newBackfillRC(r, w)
	err := (&membersByCommitteeIndexSubcommand{}).Run(context.Background(), rc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to index")
}
