// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func projMember(uid, committeeUID, projectUID, projectSlug string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:          uid,
		CommitteeUID: committeeUID,
		ProjectUID:   projectUID,
		ProjectSlug:  projectSlug,
	}}
}

func committeeBase(uid, projectUID, projectSlug string) *model.CommitteeBase {
	return &model.CommitteeBase{UID: uid, ProjectUID: projectUID, ProjectSlug: projectSlug}
}

// updatedByUID indexes the writer's recorded UpdateMember calls by member UID for assertions.
func updatedByUID(w *mockMemberWriter) map[string]*model.CommitteeMember {
	out := make(map[string]*model.CommitteeMember, len(w.updated))
	for _, m := range w.updated {
		out[m.UID] = m
	}
	return out
}

func TestMemberProjectAttribute_RepairsEmptyProjectUID(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": committeeBase("c1", "p1", "slug1"),
			"c2": committeeBase("c2", "p2", "slug2"),
		},
		members: map[string][]*model.CommitteeMember{
			"c1": {
				projMember("m1", "c1", "", ""),
				projMember("m2", "c1", "", ""), // same committee → served from cache
			},
			"c2": {projMember("m3", "c2", "", "")},
		},
	}
	w := &mockMemberWriter{}

	err := (&memberProjectAttributeSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.NoError(t, err)
	require.Len(t, w.updated, 3)

	got := updatedByUID(w)
	assert.Equal(t, "p1", got["m1"].ProjectUID)
	assert.Equal(t, "slug1", got["m1"].ProjectSlug)
	assert.Equal(t, "p1", got["m2"].ProjectUID)
	assert.Equal(t, "p2", got["m3"].ProjectUID)
	assert.Equal(t, "slug2", got["m3"].ProjectSlug)
}

func TestMemberProjectAttribute_Idempotent_SkipsCorrect(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{"c1": committeeBase("c1", "p1", "slug1")},
		members: map[string][]*model.CommitteeMember{
			"c1": {projMember("m1", "c1", "p1", "slug1")}, // already matches committee
		},
	}
	w := &mockMemberWriter{}

	err := (&memberProjectAttributeSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.NoError(t, err)
	assert.Empty(t, w.updated, "members already matching their committee must not be rewritten")
}

func TestMemberProjectAttribute_FilterByProject(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": committeeBase("c1", "p1", "slug1"),
			"c2": committeeBase("c2", "p2", "slug2"),
		},
		members: map[string][]*model.CommitteeMember{
			"c1": {projMember("m1", "c1", "", "")},
			"c2": {projMember("m2", "c2", "", "")},
		},
	}
	w := &mockMemberWriter{}

	err := (&memberProjectAttributeSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false", "--project-uid=p1"))
	require.NoError(t, err)
	require.Len(t, w.updated, 1)
	assert.Equal(t, "m1", w.updated[0].UID)
}

func TestMemberProjectAttribute_DryRun_NoWrites(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{"c1": committeeBase("c1", "p1", "slug1")},
		members: map[string][]*model.CommitteeMember{
			"c1": {projMember("m1", "c1", "", "")},
		},
	}
	w := &mockMemberWriter{}

	// default dry-run is true
	err := (&memberProjectAttributeSubcommand{}).Run(context.Background(), newBackfillRC(r, w))
	require.NoError(t, err)
	assert.Empty(t, w.updated, "dry-run must not write")
}

func TestMemberProjectAttribute_CommitteeBaseError_ReturnsError(t *testing.T) {
	r := &mockReader{
		baseErr: map[string]error{"c-broken": errors.New("committee kv unavailable")},
		members: map[string][]*model.CommitteeMember{
			"c-broken": {projMember("m1", "c-broken", "", "")},
		},
	}
	w := &mockMemberWriter{}

	err := (&memberProjectAttributeSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to repair")
	assert.Empty(t, w.updated, "a member whose committee can't be read must not be written")
}

func TestMemberProjectAttribute_MutuallyExclusiveFlags(t *testing.T) {
	err := (&memberProjectAttributeSubcommand{}).Run(context.Background(), newBackfillRC(&mockReader{}, &mockMemberWriter{}, "--committee-uid=c1", "--project-uid=p1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
