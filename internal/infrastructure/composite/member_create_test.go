// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/stretchr/testify/require"
)

func TestStore_RecoverableMembers(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModeDual} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			var secondary port.CommitteeReaderWriter
			if mode.HasSecondary() {
				// Exercise the destination port with real KV, without requiring
				// a live PostgreSQL server or claiming SQL/revision parity.
				secondary = natstest.NewStore(t)
			}
			store, err := New(mode, natstest.NewStore(t), secondary)
			require.NoError(t, err)
			member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "member-1", CommitteeUID: "committee-1", Email: "first.last@example.com"}}
			selected, err := store.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
			require.NoError(t, err)
			require.Nil(t, selected)
			outcome, err := store.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{SkipNotification: true})
			require.NoError(t, err)
			require.True(t, outcome.Added)
			require.True(t, outcome.Intent.SkipNotification)
			if secondary != nil {
				copied, _, err := secondary.GetMember(ctx, member.UID)
				require.NoError(t, err, "a missing secondary must be CREATED, not updated")
				require.Equal(t, member.Email, copied.Email)
			}
			selected, err = store.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.False(t, selected.Pending)
			require.Equal(t, member.UID, selected.Member.UID)
			outcome, err = store.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{})
			require.NoError(t, err)
			require.False(t, outcome.Added)
			require.Error(t, store.AbortMemberCreate(ctx, member), "a committed membership cannot be aborted")
			stored, _, err := store.GetMember(ctx, member.UID)
			require.NoError(t, err)
			require.Equal(t, member.Email, stored.Email)
		})
	}
}
