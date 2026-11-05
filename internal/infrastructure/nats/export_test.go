// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
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

	t.Run("a context cancelled as the scan ends is an error, not a complete scan", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		defer cancel()
		seen := 0
		err := s.EachCommitteeExport(cancelled, func(port.CommitteeExport) error {
			seen++
			if seen == 3 { // the last of the three committees
				cancel()
			}
			return nil
		})
		require.Error(t, err)
		assert.Equal(t, 3, seen)
	})

	t.Run("a cancelled context is an error", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		require.Error(t, s.EachCommitteeExport(cancelled, func(port.CommitteeExport) error { return nil }))
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

// putJSON writes a record straight to a bucket and returns the revision it was written at.
func putJSON(t *testing.T, kv jetstream.KeyValue, key string, record any) uint64 {
	t.Helper()
	data, err := json.Marshal(record)
	require.NoError(t, err)
	revision, err := kv.Put(context.Background(), key, data)
	require.NoError(t, err)
	return revision
}

func TestEachExportOfTheOtherEntities(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	s := NewStorageBackend(client)
	bucket := func(name string) jetstream.KeyValue { return client.kvStore[name] }

	// Each case writes two records and a lookup key, the shape the real writers leave, and streams them back.
	cases := []struct {
		name   string
		bucket string
		gone   string // a key to say is deleted between listing and reading; empty for a bucket the client binds lazily
		write  func(t *testing.T, kv jetstream.KeyValue) map[string]uint64
		stream func(ctx context.Context, fn func(uid string, revision uint64) error) error
	}{
		{"invites", constants.KVBucketNameCommitteeInvites, "i-1",
			func(t *testing.T, kv jetstream.KeyValue) map[string]uint64 {
				return map[string]uint64{
					"i-1": putJSON(t, kv, "i-1", storetest.NewInvite("i-1", "c-1", "first.last@example.com")),
					"i-2": putJSON(t, kv, "i-2", storetest.NewInvite("i-2", "c-1", "first.last2@example.com")),
				}
			},
			func(ctx context.Context, fn func(string, uint64) error) error {
				return s.EachInviteExport(ctx, func(i *model.CommitteeInvite, r uint64) error { return fn(i.UID, r) })
			}},
		{"applications", constants.KVBucketNameCommitteeApplications, "a-1",
			func(t *testing.T, kv jetstream.KeyValue) map[string]uint64 {
				return map[string]uint64{
					"a-1": putJSON(t, kv, "a-1", storetest.NewApplication("a-1", "c-1", "first.last@example.com")),
					"a-2": putJSON(t, kv, "a-2", storetest.NewApplication("a-2", "c-1", "first.last2@example.com")),
				}
			},
			func(ctx context.Context, fn func(string, uint64) error) error {
				return s.EachApplicationExport(ctx, func(a *model.CommitteeApplication, r uint64) error { return fn(a.UID, r) })
			}},
		{"links", constants.KVBucketNameCommitteeLinks, "l-1",
			func(t *testing.T, kv jetstream.KeyValue) map[string]uint64 {
				return map[string]uint64{
					"l-1": putJSON(t, kv, "l-1", storetest.NewLink("l-1", "c-1")),
					"l-2": putJSON(t, kv, "l-2", storetest.NewLink("l-2", "c-1")),
				}
			},
			func(ctx context.Context, fn func(string, uint64) error) error {
				return s.EachLinkExport(ctx, func(l *model.CommitteeLink, r uint64) error { return fn(l.UID, r) })
			}},
		{"folders", constants.KVBucketNameCommitteeFolders, "f-1",
			func(t *testing.T, kv jetstream.KeyValue) map[string]uint64 {
				return map[string]uint64{
					"f-1": putJSON(t, kv, "f-1", storetest.NewFolder("f-1", "c-1", "One")),
					"f-2": putJSON(t, kv, "f-2", storetest.NewFolder("f-2", "c-1", "Two")),
				}
			},
			func(ctx context.Context, fn func(string, uint64) error) error {
				return s.EachFolderExport(ctx, func(f *model.CommitteeLinkFolder, r uint64) error { return fn(f.UID, r) })
			}},
		{"documents", constants.KVBucketNameCommitteeDocuments, "d-1",
			func(t *testing.T, kv jetstream.KeyValue) map[string]uint64 {
				return map[string]uint64{
					"d-1": putJSON(t, kv, "d-1", storetest.NewDocument("d-1", "c-1", "One")),
					"d-2": putJSON(t, kv, "d-2", storetest.NewDocument("d-2", "c-1", "Two")),
				}
			},
			func(ctx context.Context, fn func(string, uint64) error) error {
				return s.EachDocumentExport(ctx, func(d *model.CommitteeDocument, r uint64) error { return fn(d.UID, r) })
			}},
		{"throttles", constants.KVBucketNameGroupWeeklyBriefThrottle, "",
			func(t *testing.T, kv jetstream.KeyValue) map[string]uint64 {
				return map[string]uint64{
					"c-1": putJSON(t, kv, "c-1.20260104", storetest.NewThrottle("c-1", storetest.FixtureWindow)),
					"c-2": putJSON(t, kv, "c-2.20260104", storetest.NewThrottle("c-2", storetest.FixtureWindow)),
				}
			},
			func(ctx context.Context, fn func(string, uint64) error) error {
				return s.EachThrottleExport(ctx, func(th *model.GroupWeeklyBriefThrottle, r uint64) error {
					assert.Equal(t, r, th.Revision, "the throttle carries the revision it was read at")
					return fn(th.CommitteeUID, r)
				})
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kv := bucket(c.bucket)
			if kv == nil {
				var err error
				kv, err = client.GetOrBindKVStore(ctx, c.bucket)
				require.NoError(t, err)
			}
			want := c.write(t, kv)
			_, err := kv.Put(ctx, "lookup/some-index-key", []byte("a-uid"))
			require.NoError(t, err)

			got := map[string]uint64{}
			require.NoError(t, c.stream(ctx, func(uid string, revision uint64) error { got[uid] = revision; return nil }))
			assert.Equal(t, want, got, "every record with the revision it was read at, and not the lookup key")

			t.Run("a record that cannot be read is skipped and reported at the end", func(t *testing.T) {
				_, err := kv.Put(ctx, "bad", []byte("{not json"))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, kv.Delete(ctx, "bad")) })
				seen := 0
				err = c.stream(ctx, func(string, uint64) error { seen++; return nil })
				require.Error(t, err)
				assert.Equal(t, 2, seen, "the readable records were still streamed")
			})

			t.Run("a record deleted between listing and reading is not a failed read", func(t *testing.T) {
				if c.gone == "" {
					t.Skip("the bucket is bound lazily, so it cannot be wrapped here")
				}
				client.kvStore[c.bucket] = &goneOnGetKV{KeyValue: kv, key: c.gone}
				t.Cleanup(func() { client.kvStore[c.bucket] = kv })
				seen := 0
				require.NoError(t, c.stream(ctx, func(string, uint64) error { seen++; return nil }))
				assert.Equal(t, 1, seen)
			})

			t.Run("a scan that is cancelled after its last record is not a complete one", func(t *testing.T) {
				cancelCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				seen := 0
				err := c.stream(cancelCtx, func(string, uint64) error {
					if seen++; seen == 2 {
						cancel()
					}
					return nil
				})
				require.Error(t, err)
			})

			t.Run("the error fn returns stops the scan", func(t *testing.T) {
				stop := errors.New("stop")
				assert.ErrorIs(t, c.stream(ctx, func(string, uint64) error { return stop }), stop)
			})
		})
	}
}

func TestEachBriefExport(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	s := NewStorageBackend(client)
	briefs, err := client.GetOrBindKVStore(ctx, constants.KVBucketNameGroupWeeklyBriefs)
	require.NoError(t, err)
	index, err := client.GetOrBindKVStore(ctx, constants.KVBucketNameGroupWeeklyBriefUIDIndex)
	require.NoError(t, err)

	// A race between two first generations of a window left two briefs in the bucket; the index names the second.
	first := storetest.NewBrief("b-first", "c-1", storetest.FixtureWindow)
	second := storetest.NewBrief("b-second", "c-1", storetest.FixtureWindow)
	other := storetest.NewBrief("b-other", "c-2", storetest.FixtureWindow)
	firstRevision := putJSON(t, briefs, "b-first", first)
	secondRevision := putJSON(t, briefs, "b-second", second)
	otherRevision := putJSON(t, briefs, "b-other", other)
	_, err = index.Put(ctx, buildBriefIndexKey("c-1", model.WindowDateKey(storetest.FixtureWindow)), []byte("b-second"))
	require.NoError(t, err)
	_, err = index.Put(ctx, buildBriefIndexKey("c-2", model.WindowDateKey(storetest.FixtureWindow)), []byte("b-other"))
	require.NoError(t, err)

	var order []string
	revisions := map[string]uint64{}
	require.NoError(t, s.EachBriefExport(ctx, func(b *model.GroupWeeklyBrief, revision uint64) error {
		order = append(order, b.UID)
		revisions[b.UID] = revision
		assert.Equal(t, revision, b.Revision, "the brief carries the revision it was read at")
		return nil
	}))

	assert.Equal(t, map[string]uint64{"b-first": firstRevision, "b-second": secondRevision, "b-other": otherRevision}, revisions)
	require.Len(t, order, 3)
	assert.Equal(t, "b-first", order[2], "a brief the index does not serve comes after those it does")

	t.Run("a brief that cannot be read is reported and the others are still streamed", func(t *testing.T) {
		_, err := briefs.Put(ctx, "b-bad", []byte("{not json"))
		require.NoError(t, err)
		seen := 0
		err = s.EachBriefExport(ctx, func(*model.GroupWeeklyBrief, uint64) error { seen++; return nil })
		require.Error(t, err)
		assert.Equal(t, 3, seen)
	})
}

func TestEachBriefAndThrottleExportOfBucketsThatDoNotExist(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	js, err := jetstream.New(client.conn)
	require.NoError(t, err)
	for _, bucket := range []string{constants.KVBucketNameGroupWeeklyBriefs, constants.KVBucketNameGroupWeeklyBriefUIDIndex, constants.KVBucketNameGroupWeeklyBriefThrottle} {
		require.NoError(t, js.DeleteKeyValue(ctx, bucket))
	}
	s := NewStorageBackend(client)

	// A deployment that never generated a brief has no brief buckets: that is an empty export, not a failure.
	require.NoError(t, s.EachBriefExport(ctx, func(*model.GroupWeeklyBrief, uint64) error { t.Fatal("no brief"); return nil }))
	require.NoError(t, s.EachThrottleExport(ctx, func(*model.GroupWeeklyBriefThrottle, uint64) error { t.Fatal("no throttle"); return nil }))

	// A bucket that cannot be bound for any other reason is an error: an outage is not an empty bucket.
	require.NoError(t, client.Close())
	assert.Error(t, s.EachBriefExport(ctx, func(*model.GroupWeeklyBrief, uint64) error { return nil }))
	assert.Error(t, s.EachThrottleExport(ctx, func(*model.GroupWeeklyBriefThrottle, uint64) error { return nil }))
}
