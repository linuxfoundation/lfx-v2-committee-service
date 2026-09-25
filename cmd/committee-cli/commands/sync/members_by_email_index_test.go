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

func emailBackfillMember(uid, email string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          uid,
		CommitteeUID: "c1",
		Email:        email,
	}}
}

func TestMembersByEmailIndex_BackfillAll_SkipsMembersWithoutEmail(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				emailBackfillMember("m1", "user@example.com"),
				emailBackfillMember("m2", "other@example.com"),
				emailBackfillMember("m3", ""), // no email → skipped
			},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByEmailIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.NoError(t, err)
	assert.Len(t, w.emailIndexed, 2, "only members with an email are indexed")
}

func TestMembersByEmailIndex_BackfillAll_SkipsWhitespaceOnlyEmail(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {
				emailBackfillMember("m1", "user@example.com"),
				emailBackfillMember("m2", "   "), // whitespace-only → skipped
			},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByEmailIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.NoError(t, err)
	assert.Len(t, w.emailIndexed, 1, "whitespace-only email must be skipped like empty email")
}

func TestMembersByEmailIndex_DryRun(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {emailBackfillMember("m1", "user@example.com")},
		},
	}
	w := &mockMemberWriter{}

	err := (&membersByEmailIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run"))
	require.NoError(t, err)
	assert.Empty(t, w.emailIndexed, "dry-run must not write")
}

func TestMembersByEmailIndex_IndexError_ReturnsError(t *testing.T) {
	r := &mockReader{
		members: map[string][]*model.CommitteeMember{
			"c1": {emailBackfillMember("m1", "user@example.com")},
		},
	}
	w := &mockMemberWriter{indexError: fmt.Errorf("nats unavailable")}

	err := (&membersByEmailIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to index")
}
