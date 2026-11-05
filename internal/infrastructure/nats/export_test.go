// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func TestEachCommitteeExport(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	s := NewStorageBackend(client)

	require.NoError(t, s.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	withoutSettings := storetest.NewCommittee("uid-2", "project-1", "Committee Two")
	withoutSettings.CommitteeSettings = nil
	require.NoError(t, s.Create(ctx, withoutSettings))

	exports := map[string]port.CommitteeExport{}
	require.NoError(t, s.EachCommitteeExport(ctx, func(e port.CommitteeExport) error {
		exports[e.Base.UID] = e
		return nil
	}))

	require.Len(t, exports, 2)
	_, baseRevision, err := s.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	_, settingsRevision, err := s.GetSettings(ctx, "uid-1")
	require.NoError(t, err)
	assert.Equal(t, baseRevision, exports["uid-1"].BaseRevision)
	assert.Equal(t, settingsRevision, exports["uid-1"].SettingsRevision)
	require.NotNil(t, exports["uid-1"].Settings)
	assert.Equal(t, "Committee One", exports["uid-1"].Base.Name)

	assert.NotZero(t, exports["uid-2"].BaseRevision)
	assert.Nil(t, exports["uid-2"].Settings, "a committee with no settings record has none")
	assert.Zero(t, exports["uid-2"].SettingsRevision)

	t.Run("the settings are keyed by the committee uid whatever uid the record holds", func(t *testing.T) {
		_, err := client.kvStore[constants.KVBucketNameCommittees].Put(ctx, "uid-3", []byte(`{"uid":"uid-3","name":"Committee Three","project_uid":"project-1"}`))
		require.NoError(t, err)
		_, err = client.kvStore[constants.KVBucketNameCommitteeSettings].Put(ctx, "uid-3", []byte(`{"uid":"","business_email_required":true}`))
		require.NoError(t, err)
		var got port.CommitteeExport
		require.NoError(t, s.EachCommitteeExport(ctx, func(e port.CommitteeExport) error {
			if e.Base.UID == "uid-3" {
				got = e
			}
			return nil
		}))
		require.NotNil(t, got.Settings)
		assert.Equal(t, "uid-3", got.Settings.UID)
	})

	t.Run("the first error fn returns stops the scan", func(t *testing.T) {
		stop := errors.New("stop")
		calls := 0
		err := s.EachCommitteeExport(ctx, func(port.CommitteeExport) error { calls++; return stop })
		require.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})

	t.Run("a committee that cannot be read is skipped and reported at the end", func(t *testing.T) {
		_, err := client.kvStore[constants.KVBucketNameCommittees].Put(ctx, "uid-bad", []byte("{not json"))
		require.NoError(t, err)
		seen := 0
		err = s.EachCommitteeExport(ctx, func(port.CommitteeExport) error { seen++; return nil })
		require.Error(t, err)
		assert.Equal(t, 3, seen, "the readable committees were still streamed")
	})
}

func TestEachMemberExport(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	s := NewStorageBackend(client)
	require.NoError(t, s.CreateMember(ctx, newConflictTestMember("m-1", "first.last@example.com")))
	require.NoError(t, s.CreateMember(ctx, newConflictTestMember("m-2", "first.last2@example.com")))

	got := map[string]uint64{}
	require.NoError(t, s.EachMemberExport(ctx, func(m *model.CommitteeMember, revision uint64) error {
		got[m.UID] = revision
		return nil
	}))

	require.Len(t, got, 2, "the lookup keys the members hold are not members")
	for uid, revision := range got {
		_, want, err := s.GetMember(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, want, revision, uid)
	}

	t.Run("a member deleted between listing and reading is not a failed read", func(t *testing.T) {
		members := client.kvStore[constants.KVBucketNameCommitteeMembers]
		client.kvStore[constants.KVBucketNameCommitteeMembers] = &goneOnGetKV{KeyValue: members, key: "m-2"}
		t.Cleanup(func() { client.kvStore[constants.KVBucketNameCommitteeMembers] = members })
		seen := map[string]bool{}
		require.NoError(t, s.EachMemberExport(ctx, func(m *model.CommitteeMember, _ uint64) error { seen[m.UID] = true; return nil }))
		assert.Equal(t, map[string]bool{"m-1": true}, seen)
	})

	t.Run("a member that cannot be read is skipped and reported at the end", func(t *testing.T) {
		_, err := client.kvStore[constants.KVBucketNameCommitteeMembers].Put(ctx, "m-bad", []byte("{not json"))
		require.NoError(t, err)
		seen := 0
		err = s.EachMemberExport(ctx, func(*model.CommitteeMember, uint64) error { seen++; return nil })
		require.Error(t, err)
		assert.Equal(t, 2, seen)
	})
}

// goneOnGetKV wraps a bucket so a Get of one key says the key is not found, as if it were deleted after the listing.
type goneOnGetKV struct {
	jetstream.KeyValue
	key string
}

func (k *goneOnGetKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if key == k.key {
		return nil, jetstream.ErrKeyNotFound
	}
	return k.KeyValue.Get(ctx, key)
}
