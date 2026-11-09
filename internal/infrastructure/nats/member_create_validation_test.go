// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

func TestMemberCreatePreparation(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"primary absent", "reservation absent", "ambiguous acquisition after reclaim"} {
		t.Run(failure, func(t *testing.T) {
			s, kv := newMemberLockStorage()
			member := testMember("original", "committee-1", "first.last@example.com", "first-last", "")
			unique := buildMemberLookupKeys(ctx, member).unique
			switch failure {
			case "primary absent":
				kv.failCreateFor = member.UID
			case "reservation absent":
				kv.failCreateFor = unique
			default:
				fence, err := json.Marshal(abortedMemberCreate("legacy"))
				require.NoError(t, err)
				_, err = kv.Create(ctx, "legacy", fence)
				require.NoError(t, err)
				_, err = kv.Create(ctx, unique, []byte("legacy"))
				require.NoError(t, err)
				kv.ambiguousFor = unique
			}
			_, err := s.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{SkipNotification: true, CreatedBy: "first-last"})
			require.Error(t, err)
			selected, err := s.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.True(t, selected.Pending)
			require.Equal(t, "original", selected.Member.UID)
			require.True(t, selected.Intent.SkipNotification)
			kv.failCreateFor, kv.ambiguousFor = "", ""
			retry := testMember("replacement", member.CommitteeUID, member.Email, "replacement-identity", "")
			outcome, err := resumeMemberCreate(s, retry, port.MemberCreateIntent{})
			require.NoError(t, err)
			require.True(t, outcome.Added)
			require.Equal(t, "original", outcome.Member.UID)
			require.True(t, outcome.Intent.SkipNotification)
			for key, data := range kv.values {
				require.NotContains(t, string(data), "created_by", key)
				require.NotContains(t, string(data), "skip_notification", key)
				if key != member.UID {
					require.NotContains(t, string(data), member.Email, key)
				}
			}
			require.NotContains(t, kv.values, "replacement")
		})
	}
}

type pausedLastMemberIndexKV struct {
	jetstream.KeyValue
	key    string
	landed chan struct{}
	resume chan struct{}
}

func (k *pausedLastMemberIndexKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	revision, err := k.KeyValue.Create(ctx, key, value, opts...)
	if key != k.key || err != nil {
		return revision, err
	}
	close(k.landed)
	select {
	case <-k.resume:
	case <-ctx.Done():
	}
	return 0, errors.New("simulated timeout after final index landed")
}

func TestLegacyMemberRollbackFence(t *testing.T) {
	for _, throughBulk := range []bool{false, true} {
		name := "storage"
		if throughBulk {
			name = "bulk pre-list"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := newKVClient(t)
			kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
			member := testMember("legacy", "committee-1", "first.last@example.com", "first-last", "")
			pause := &pausedLastMemberIndexKV{KeyValue: kv, key: buildMemberLookupKeys(ctx, member).byUsername, landed: make(chan struct{}), resume: make(chan struct{})}
			client.kvStore[constants.KVBucketNameCommitteeMembers] = pause
			s := NewStorageBackend(client)
			done := make(chan error, 1)
			go func() { done <- s.CreateMember(ctx, member) }()
			select {
			case <-pause.landed:
			case <-ctx.Done():
				t.Fatal("original create did not reach final index")
			}
			if throughBulk {
				base := model.CommitteeBase{UID: member.CommitteeUID, Name: "Example Committee", Category: "Other"}
				data, err := json.Marshal(base)
				require.NoError(t, err)
				_, err = client.kvStore[constants.KVBucketNameCommittees].Create(ctx, base.UID, data)
				require.NoError(t, err)
				publisher := &mock.MockCommitteePublisher{}
				writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s), service.WithCommitteeRetriever(s), service.WithCommitteePublisher(publisher))
				results, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{testMember("unused", base.UID, member.Email, "first-last", "")}, service.BulkMemberCreateOptions{SkipEnrichment: true})
				require.NoError(t, err)
				require.Equal(t, model.BulkReasonAlreadyMember, results[0].Reason)
				require.Equal(t, member.UID, results[0].MemberUID)
				require.Empty(t, publisher.IndexerMessages)
			} else {
				outcome, err := s.CreateMemberRecoverable(ctx, testMember("retry", member.CommitteeUID, member.Email, "first-last", ""), port.MemberCreateIntent{})
				require.NoError(t, err)
				require.False(t, outcome.Added)
				require.Equal(t, member.UID, outcome.Member.UID)
			}
			close(pause.resume)
			require.Error(t, <-done, "original index error is not hidden")
			stored, _, err := s.GetMember(ctx, member.UID)
			require.NoError(t, err, "original rollback must lose its revision guard")
			require.Equal(t, member.Email, stored.Email)
			lock, err := kv.Get(ctx, buildMemberLookupKeys(ctx, member).unique)
			require.NoError(t, err)
			require.Equal(t, member.UID, string(lock.Value()))
		})
	}
}

type currentRecoveryUser struct {
	port.UserReader
	username string
}

func (r *currentRecoveryUser) UsernameByEmail(context.Context, string) (string, error) {
	return r.username, nil
}

func TestBulkMemberRecoveryCurrentValidation(t *testing.T) {
	for _, scenario := range []string{"identity changed", "identity absent", "voting enabled", "organization required", "irrelevant replacement fields"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			client := newKVClient(t)
			client.kvStore[constants.KVBucketNameCommitteeMembers] = &failMemberIndexOnceKV{KeyValue: client.kvStore[constants.KVBucketNameCommitteeMembers]}
			s := NewStorageBackend(client)
			base := &model.CommitteeBase{UID: "committee-1", Name: "Example Committee", Category: "Other"}
			storeBase := func() {
				data, err := json.Marshal(base)
				require.NoError(t, err)
				_, err = client.kvStore[constants.KVBucketNameCommittees].Put(ctx, base.UID, data)
				require.NoError(t, err)
			}
			storeBase()
			users := &currentRecoveryUser{username: "first-last"}
			publisher := &mock.MockCommitteePublisher{}
			writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s), service.WithCommitteeRetriever(s), service.WithCommitteePublisher(publisher), service.WithUserReader(users))
			row := func() *model.CommitteeMember {
				return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{Email: "first.last@example.com", FirstName: "First", LastName: "Last", Voting: model.CommitteeMemberVotingInfo{Status: "None"}}}
			}
			original := row()
			if scenario == "voting enabled" || scenario == "irrelevant replacement fields" {
				original.Organization = model.CommitteeMemberOrganization{Name: "Example Organization", Website: "https://example.com"}
			}
			if scenario == "irrelevant replacement fields" {
				original.Voting.Status = "Voting Rep"
			}
			first, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{original}, service.BulkMemberCreateOptions{})
			require.NoError(t, err)
			require.Equal(t, model.BulkReasonTemporary, first[0].Reason)
			retry := row()
			retry.Organization = model.CommitteeMemberOrganization{Name: "Example Organization", Website: "https://example.com"}
			retry.Voting.Status = "Voting Rep"
			switch scenario {
			case "identity changed":
				users.username = "first-last-new"
			case "identity absent":
				users.username = ""
			case "voting enabled", "irrelevant replacement fields":
				base.EnableVoting = true
				storeBase()
			case "organization required":
				data, err := json.Marshal(&model.CommitteeSettings{UID: base.UID, BusinessEmailRequired: true})
				require.NoError(t, err)
				_, err = client.kvStore[constants.KVBucketNameCommitteeSettings].Put(ctx, base.UID, data)
				require.NoError(t, err)
			}
			if scenario == "irrelevant replacement fields" {
				retry.Organization = model.CommitteeMemberOrganization{}
				retry.Voting.Status = "None"
			}
			second, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{retry}, service.BulkMemberCreateOptions{})
			require.NoError(t, err)
			if scenario == "irrelevant replacement fields" {
				require.Equal(t, model.BulkMemberAdded, second[0].Status)
				require.Equal(t, "Voting Rep", second[0].Member.Voting.Status)
				return
			}
			require.Equal(t, model.BulkMemberFailed, second[0].Status)
			require.Empty(t, publisher.IndexerMessages)
			require.Zero(t, publisher.MemberPutCallCount, "no stale identity grant")
			members, err := s.ListAllMembers(ctx)
			require.NoError(t, err)
			require.Empty(t, members)
			selected, err := s.FindMemberCreate(ctx, base.UID, original.Email)
			require.NoError(t, err)
			require.Nil(t, selected, "rejected original snapshot must be fenced, not left to commit later")
		})
	}
}
