// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

// Storage-only retries explicitly reselect the durable snapshot. Current-policy
// validation is separately exercised through the real bulk use case below.
func resumeMemberCreate(s *storage, member *model.CommitteeMember, intent port.MemberCreateIntent) (*port.MemberCreateResult, error) {
	var result *port.MemberCreateResult
	var err error
	for range 4 {
		selected, findErr := s.FindMemberCreate(context.Background(), member.CommitteeUID, member.Email)
		if findErr != nil {
			return nil, findErr
		}
		if selected != nil && selected.Pending {
			member, intent = selected.Member, selected.Intent
		}
		result, err = s.CreateMemberRecoverable(context.Background(), member, intent)
		if err == nil {
			return result, nil
		}
	}
	return result, err
}

func TestStorage_CreateMemberRecoverable(t *testing.T) {
	ctx := context.Background()
	member := func(uid string) *model.CommitteeMember {
		return testMember(uid, "committee-1", "first.last@example.com", "first-last", testOrgSFID)
	}
	intent := port.MemberCreateIntent{SkipNotification: true, CreatedBy: "first-last"}
	for _, landed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy absent owner after unreadable write", true: "legacy landed owner after unreadable write"}[landed], func(t *testing.T) {
			s, kv := newMemberLockStorage()
			if landed {
				kv.ambiguousFor = "old"
			} else {
				kv.failCreateFor = "old"
			}
			kv.getErrFor = "old"
			require.Error(t, s.CreateMember(ctx, member("old")))
			kv.getErrFor, kv.failCreateFor, kv.ambiguousFor = "", "", ""
			outcome, err := resumeMemberCreate(s, member("retry"), intent)
			require.NoError(t, err)
			require.Equal(t, !landed, outcome.Added, "a visible legacy membership is not a new addition")
			if landed {
				require.Equal(t, "old", outcome.Member.UID)
			} else {
				require.Equal(t, "retry", outcome.Member.UID)
				require.Error(t, s.CreateMember(ctx, member("old")), "fenced old UID cannot write later")
			}
			for _, key := range memberKeyList(ctx, outcome.Member) {
				require.Equal(t, []byte(outcome.Member.UID), kv.values[key])
			}
			result, err := s.CreateMemberRecoverable(ctx, member("retry-again"), port.MemberCreateIntent{})
			require.NoError(t, err)
			require.False(t, result.Added)
			require.Equal(t, outcome.Member.UID, result.Member.UID)
		})
	}
	t.Run("unreadable reservation owner stays temporary", func(t *testing.T) {
		s, kv := newMemberLockStorage()
		key := buildMemberLookupKeys(ctx, member("old")).unique
		_, err := kv.Create(ctx, key, []byte("old"))
		require.NoError(t, err)
		kv.getErrFor = "old"
		outcome, err := s.CreateMemberRecoverable(ctx, member("retry"), intent)
		var unavailable errs.ServiceUnavailable
		require.ErrorAs(t, err, &unavailable)
		require.Nil(t, outcome)
		require.Equal(t, []byte("old"), kv.values[key])
	})
	t.Run("late legacy writer wins primary fence race", func(t *testing.T) {
		s, kv := newMemberLockStorage()
		key := buildMemberLookupKeys(ctx, member("old")).unique
		_, err := kv.Create(ctx, key, []byte("old"))
		require.NoError(t, err)
		kv.beforeCreate = map[string]func(){"old": func() {
			data, err := json.Marshal(member("old"))
			require.NoError(t, err)
			kv.values["old"], kv.revs["old"] = data, 1
		}}
		outcome, err := resumeMemberCreate(s, member("retry"), intent)
		require.NoError(t, err)
		require.Equal(t, "old", outcome.Member.UID)
		require.Equal(t, []byte("old"), kv.values[key])
	})
	t.Run("pending create resumes immutable snapshot and original intent", func(t *testing.T) {
		s, kv := newMemberLockStorage()
		original := member("old")
		kv.failCreateFor = buildMemberLookupKeys(ctx, original).byEmail
		_, err := s.CreateMemberRecoverable(ctx, original, intent)
		require.Error(t, err)
		_, _, err = s.GetMember(ctx, "old")
		var notFound errs.NotFound
		require.ErrorAs(t, err, &notFound)
		_, err = s.GetMemberRevision(ctx, "old")
		require.ErrorAs(t, err, &notFound)
		kv.failCreateFor = ""
		retry := member("retry")
		retry.Username = "untrusted-replacement"
		_, err = s.CreateMemberRecoverable(ctx, retry, port.MemberCreateIntent{})
		require.Error(t, err, "an unselected snapshot must not silently complete another operation")
		outcome, err := resumeMemberCreate(s, retry, port.MemberCreateIntent{})
		require.NoError(t, err)
		require.True(t, outcome.Added)
		require.Equal(t, original.CommitteeMemberBase, outcome.Member.CommitteeMemberBase)
		require.Equal(t, intent, outcome.Intent)
		require.NotContains(t, string(kv.values["old"]), "created_by")
		require.NotContains(t, string(kv.values["old"]), "skip_notification")
	})
	t.Run("another completion wins while original is paused", func(t *testing.T) {
		s, kv := newMemberLockStorage()
		var winner *port.MemberCreateResult
		kv.beforeUpdate = func() {
			var err error
			winner, err = resumeMemberCreate(s, member("retry"), port.MemberCreateIntent{})
			require.NoError(t, err)
		}
		loser, err := s.CreateMemberRecoverable(ctx, member("old"), intent)
		require.NoError(t, err)
		require.True(t, winner.Added)
		require.False(t, loser.Added)
		require.Equal(t, winner.Member.UID, loser.Member.UID)
	})
	for _, action := range []string{"update", "delete"} {
		t.Run("paused completion cannot undo "+action, func(t *testing.T) {
			s, kv := newMemberLockStorage()
			kv.beforeUpdate = func() {
				winner, err := resumeMemberCreate(s, member("retry"), intent)
				require.NoError(t, err)
				_, revision, err := s.GetMember(ctx, winner.Member.UID)
				require.NoError(t, err)
				if action == "delete" {
					require.NoError(t, s.DeleteMember(ctx, winner.Member.UID, revision))
				} else {
					winner.Member.Email = "first.last+changed@example.com"
					_, err = s.UpdateMember(ctx, winner.Member, revision)
					require.NoError(t, err)
				}
			}
			outcome, err := s.CreateMemberRecoverable(ctx, member("old"), intent)
			require.Error(t, err)
			require.Nil(t, outcome)
			if action == "delete" {
				_, _, err := s.GetMember(ctx, "old")
				require.Error(t, err)
				require.Error(t, s.CreateMember(ctx, member("old")), "delete fence must block a delayed Create")
			} else {
				stored, _, err := s.GetMember(ctx, "old")
				require.NoError(t, err)
				require.Equal(t, "first.last+changed@example.com", stored.Email)
				require.NotContains(t, kv.values, buildMemberLookupKeys(ctx, member("old")).byEmail)
			}
		})
	}
	t.Run("ambiguous commit elects its token only once", func(t *testing.T) {
		s, kv := newMemberLockStorage()
		kv.ambiguousFor = "old"
		outcome, err := s.CreateMemberRecoverable(ctx, member("old"), intent)
		require.NoError(t, err)
		require.True(t, outcome.Added)
		outcome, err = s.CreateMemberRecoverable(ctx, member("retry"), intent)
		require.NoError(t, err)
		require.False(t, outcome.Added)
	})
	t.Run("changed reservation revision is never released", func(t *testing.T) {
		client := newKVClient(t)
		kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
		key := buildMemberLookupKeys(ctx, member("old")).unique
		rev, err := kv.Create(ctx, key, []byte("old"))
		require.NoError(t, err)
		client.kvStore[constants.KVBucketNameCommitteeMembers] = &beforeMemberPurgeKV{KeyValue: kv, before: func() {
			_, err := kv.Update(ctx, key, []byte("new-owner"), rev)
			require.NoError(t, err)
		}}
		s := NewStorageBackend(client)
		_, err = s.CreateMemberRecoverable(ctx, member("retry"), intent)
		require.Error(t, err)
		lock, err := kv.Get(ctx, key)
		require.NoError(t, err)
		require.Equal(t, "new-owner", string(lock.Value()))
	})
	t.Run("real concurrent retries create one visible member and elect one publisher", func(t *testing.T) {
		s := NewStorageBackend(newKVClient(t))
		var added atomic.Int32
		var group sync.WaitGroup
		for range 12 {
			group.Add(1)
			go func() {
				defer group.Done()
				outcome, err := resumeMemberCreate(s, member(uuid.NewString()), intent)
				if err != nil {
					t.Errorf("create: %v", err)
					return
				}
				if outcome.Added {
					added.Add(1)
				}
			}()
		}
		group.Wait()
		require.EqualValues(t, 1, added.Load())
		members, err := s.ListAllMembers(ctx)
		require.NoError(t, err)
		require.Len(t, members, 1)
		for _, each := range []func(context.Context, func(*model.CommitteeMember) error) error{s.EachMember, s.EachMemberStrict} {
			count := 0
			require.NoError(t, each(ctx, func(*model.CommitteeMember) error { count++; return nil }))
			require.Equal(t, 1, count)
		}
	})
}

type beforeMemberPurgeKV struct {
	jetstream.KeyValue
	before func()
}

func (k *beforeMemberPurgeKV) Purge(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	k.before()
	return k.KeyValue.Purge(ctx, key, opts...)
}

type failMemberIndexOnceKV struct {
	jetstream.KeyValue
	failed atomic.Bool
}

func (k *failMemberIndexOnceKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	if strings.HasPrefix(key, "lookup/committee-members-by-committee/") && k.failed.CompareAndSwap(false, true) {
		return 0, errors.New("simulated temporary index outage")
	}
	return k.KeyValue.Create(ctx, key, value, opts...)
}

func TestBulkMemberRecovery(t *testing.T) {
	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "first-last")
	client := newKVClient(t)
	kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
	client.kvStore[constants.KVBucketNameCommitteeMembers] = &failMemberIndexOnceKV{KeyValue: kv}
	s := NewStorageBackend(client)
	base := &model.CommitteeBase{UID: "committee-1", Name: "Example Committee", Category: "Other"}
	data, err := json.Marshal(base)
	require.NoError(t, err)
	_, err = client.kvStore[constants.KVBucketNameCommittees].Create(ctx, base.UID, data)
	require.NoError(t, err)
	publisher := &mock.MockCommitteePublisher{}
	writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s), service.WithCommitteeRetriever(s), service.WithCommitteePublisher(publisher), service.WithUserReader(&currentRecoveryUser{}))
	row := func(skip bool) *model.CommitteeMember {
		return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{Email: "first.last@example.com", FirstName: "First", LastName: "Last", AppointedBy: "None", Status: "Active"}, SkipNotification: skip}
	}
	opts := service.BulkMemberCreateOptions{SkipEnrichment: true}
	first, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row(true)}, opts)
	require.NoError(t, err)
	require.Equal(t, model.BulkReasonTemporary, first[0].Reason)
	require.True(t, first[0].Retryable)
	require.Empty(t, publisher.IndexerMessages)
	second, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row(false)}, opts)
	require.NoError(t, err)
	require.Equal(t, model.BulkMemberAdded, second[0].Status)
	require.Len(t, publisher.IndexerMessages, 1)
	event := publisher.LastEvent.(*model.CommitteeEvent)
	created := event.Data.(*model.CommitteeMemberCreatedEventData)
	require.True(t, created.SkipNotification, "retry must preserve original opt-out")
	require.Equal(t, "first-last", created.CreatedBy)
	third, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row(false)}, opts)
	require.NoError(t, err)
	require.Equal(t, model.BulkReasonAlreadyMember, third[0].Reason)
	require.Equal(t, second[0].MemberUID, third[0].MemberUID)
	require.Len(t, publisher.IndexerMessages, 1, "completed retry must not replay publication")
	members, err := s.ListMembersByCommittee(ctx, base.UID)
	require.NoError(t, err)
	require.Len(t, members, 1)
}
