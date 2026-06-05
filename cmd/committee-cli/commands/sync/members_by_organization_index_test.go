// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func orgMember(uid, orgID string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          uid,
		CommitteeUID: "c1",
		Organization: model.CommitteeMemberOrganization{ID: orgID},
	}}
}

func TestMembersByOrganizationIndex_BackfillAll_SkipsMembersWithoutOrg(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				orgMember("m1", "001A00000000001AAA"),
				orgMember("m2", "001A00000000002AAA"),
				orgMember("m3", ""), // no org → skipped
			},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByOrganizationIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w))
	require.NoError(t, err)
	assert.Len(t, w.orgIndexed, 2, "only members with an organization.id are indexed")
}

func TestMembersByOrganizationIndex_FilterByOrg(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				orgMember("m1", "001A00000000001AAA"),
				orgMember("m2", "001A00000000002AAA"),
			},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByOrganizationIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--org-sfid=001A00000000001AAA"))
	require.NoError(t, err)
	require.Len(t, w.orgIndexed, 1)
	assert.Contains(t, w.orgIndexed[0], "001A00000000001AAA.m1")
}

func TestMembersByOrganizationIndex_DryRun(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {orgMember("m1", "001A00000000001AAA")},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByOrganizationIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run"))
	require.NoError(t, err)
	assert.Empty(t, w.orgIndexed, "dry-run must not write")
}

func TestMembersByOrganizationIndex_IndexError_ReturnsError(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {orgMember("m1", "001A00000000001AAA")},
		},
	}
	w := &mockMemberWriter{indexError: fmt.Errorf("nats unavailable")}

	err := (&membersByOrganizationIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to index")
}
