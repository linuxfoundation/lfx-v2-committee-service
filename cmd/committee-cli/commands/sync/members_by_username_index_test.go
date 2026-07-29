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

func usernameBackfillMember(uid, username string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          uid,
		CommitteeUID: "c1",
		Username:     username,
	}}
}

func TestMembersByUsernameIndex_BackfillAll_SkipsMembersWithoutUsername(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				usernameBackfillMember("m1", "jdoe"),
				usernameBackfillMember("m2", "asmith"),
				usernameBackfillMember("m3", ""), // no username → skipped
			},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByUsernameIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.NoError(t, err)
	assert.Len(t, w.usernameIndexed, 2, "only members with a username are indexed")
}

func TestMembersByUsernameIndex_BackfillAll_SkipsWhitespaceOnlyUsername(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				usernameBackfillMember("m1", "jdoe"),
				usernameBackfillMember("m2", "   "), // whitespace-only → skipped
			},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByUsernameIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.NoError(t, err)
	assert.Len(t, w.usernameIndexed, 1, "whitespace-only username must be skipped like empty username")
}

func TestMembersByUsernameIndex_DryRun(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {usernameBackfillMember("m1", "jdoe")},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByUsernameIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run"))
	require.NoError(t, err)
	assert.Empty(t, w.usernameIndexed, "dry-run must not write")
}

func TestMembersByUsernameIndex_IndexError_ReturnsError(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {usernameBackfillMember("m1", "jdoe")},
		},
	}
	w := &mockMemberWriter{indexError: fmt.Errorf("nats unavailable")}

	err := (&membersByUsernameIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to index")
}
