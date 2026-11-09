// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

func TestStorage_FindMemberCreate(t *testing.T) {
	for _, path := range []string{"complete legacy", "missing legacy index", "bulk skips before enrichment"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			client := newKVClient(t)
			s := NewStorageBackend(client)
			kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
			member := testMember("legacy", "committee-1", "first.last@example.com", "first-last", "")
			require.NoError(t, s.CreateMember(ctx, member))
			if path != "complete legacy" {
				require.NoError(t, kv.Purge(ctx, buildMemberLookupKeys(ctx, member).byCommittee))
			}
			if path == "bulk skips before enrichment" {
				base := model.CommitteeBase{UID: member.CommitteeUID, Name: "Example Committee", Category: "Other", EnableVoting: true}
				data, err := json.Marshal(base)
				require.NoError(t, err)
				_, err = client.kvStore[constants.KVBucketNameCommittees].Create(ctx, base.UID, data)
				require.NoError(t, err)
				publisher := &mock.MockCommitteePublisher{}
				writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s), service.WithCommitteeRetriever(s), service.WithCommitteePublisher(publisher))
				for range 2 {
					row := testMember("replacement", member.CommitteeUID, member.Email, "first-last-new", "")
					row.Voting.Status = "Voting Rep" // Invalid replacement fields must not reject an existing seat.
					results, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row}, service.BulkMemberCreateOptions{
						Enrich: func(context.Context, *model.CommitteeMember) {
							t.Error("an existing member must not require enrichment")
						},
					})
					require.NoError(t, err)
					require.Equal(t, model.BulkReasonAlreadyMember, results[0].Reason)
					require.Equal(t, member.UID, results[0].MemberUID)
					require.Equal(t, model.BulkMemberSkipped, results[0].Status)
				}
				require.Empty(t, publisher.IndexerMessages)
				require.Zero(t, publisher.MemberPutCallCount)
			} else {
				selected, err := s.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
				require.NoError(t, err)
				require.NotNil(t, selected)
				require.False(t, selected.Pending)
				require.Equal(t, member.UID, selected.Member.UID)
			}
			stored, _, err := s.GetMember(ctx, member.UID)
			require.NoError(t, err, "legacy membership stays readable")
			require.Equal(t, member.Username, stored.Username)
			for _, key := range memberKeyList(ctx, member) {
				entry, err := kv.Get(ctx, key)
				require.NoError(t, err)
				require.Equal(t, member.UID, string(entry.Value()))
			}
			require.Error(t, s.AbortMemberCreate(ctx, member), "current import policy cannot abort a legacy seat")
			require.NoError(t, s.DeleteMember(ctx, member.UID, mustMemberRevision(t, s, member.UID)))
		})
	}
}

func mustMemberRevision(t *testing.T, s *storage, uid string) uint64 {
	t.Helper()
	_, revision, err := s.GetMember(context.Background(), uid)
	require.NoError(t, err)
	return revision
}

type cleanupTransportMessage struct {
	port.TransportMessenger
	data []byte
}

func (m cleanupTransportMessage) Data() []byte { return m.data }

type cleanupStreamMessage struct {
	port.StreamMessenger
	data []byte
}

func (m cleanupStreamMessage) Data() []byte  { return m.data }
func (cleanupStreamMessage) Subject() string { return constants.UserEmailChangedSubject }

func TestStorage_AbortMemberCreatesForIdentity(t *testing.T) {
	for _, scenario := range []string{"preparation only", "pending primary", "user deleted", "email changed", "different email", "different username"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			client := newKVClient(t)
			s := NewStorageBackend(client)
			kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
			member := testMember("pending", "committee-1", "first.last@example.com", "first-last", "")
			record := memberCreateRecord{CommitteeMemberBase: member.CommitteeMemberBase,
				Create: &memberCreateState{State: memberCreatePending, Intent: &port.MemberCreateIntent{CreatedBy: "first-last", SkipNotification: true}}}
			data, err := json.Marshal(record)
			require.NoError(t, err)
			opKey := memberCreateOperationKey(ctx, member.CommitteeUID, member.Email)
			_, err = kv.Create(ctx, opKey, data)
			require.NoError(t, err)
			if scenario != "preparation only" {
				_, err = kv.Create(ctx, member.UID, data)
				require.NoError(t, err)
				_, err = reserveOwnedKey(ctx, kv, member.UID, buildMemberLookupKeys(ctx, member).unique)
				require.NoError(t, err)
			}
			writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s))
			handler := service.NewUserEventHandler(nil, writer, nil)
			switch scenario {
			case "user deleted":
				_, err = handler.HandleUserDeleted(ctx, cleanupTransportMessage{data: []byte(`{"username":"first-last","email":"first.last@example.com"}`)})
			case "email changed":
				err = handler.HandleUserEmailChanged(ctx, cleanupStreamMessage{data: []byte(`{"type":"alternate_email_removed","email":"first.last@example.com"}`)})
			default:
				username, email := " FIRST-LAST ", " FIRST.LAST@EXAMPLE.COM "
				if scenario == "different email" {
					email = "first.last+different@example.com"
				}
				if scenario == "different username" {
					username = "first-last-different"
				}
				err = s.AbortMemberCreatesForIdentity(ctx, username, email)
			}
			require.NoError(t, err)
			if strings.HasPrefix(scenario, "different") {
				selected, err := s.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
				require.NoError(t, err)
				require.NotNil(t, selected)
				require.True(t, selected.Pending, "reuse guards preserve unrelated pending operations")
				return
			}
			for _, key := range []string{opKey, member.UID} {
				entry, err := kv.Get(ctx, key)
				require.NoError(t, err)
				require.NotContains(t, string(entry.Value()), member.Email)
				require.NotContains(t, string(entry.Value()), "created_by")
				require.Contains(t, string(entry.Value()), memberCreateAborted)
			}
			outcome, err := s.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{})
			require.Error(t, err, "a delayed completion may not resurrect the aborted UID")
			require.Nil(t, outcome)
			_, _, err = s.GetMember(ctx, member.UID)
			var notFound errs.NotFound
			require.ErrorAs(t, err, &notFound)
		})
	}
}

type deleteDuringMemberReadKV struct {
	jetstream.KeyValue
	uid    string
	reads  int
	before int
	fenced bool
}

func (k *deleteDuringMemberReadKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if key == k.uid {
		k.reads++
		if k.reads == k.before && k.before == 2 {
			if err := k.removeCurrent(ctx, key); err != nil {
				return nil, err
			}
		}
	}
	return k.KeyValue.Get(ctx, key)
}

func (k *deleteDuringMemberReadKV) removeCurrent(ctx context.Context, key string) error {
	entry, err := k.KeyValue.Get(ctx, key)
	if err != nil {
		return err
	}
	if k.fenced {
		data, _ := json.Marshal(abortedMemberCreate(key))
		_, err = k.KeyValue.Update(ctx, key, data, entry.Revision())
	} else {
		err = k.Delete(ctx, key, jetstream.LastRevision(entry.Revision()))
	}
	return err
}

func (k *deleteDuringMemberReadKV) Update(ctx context.Context, key string, data []byte, revision uint64) (uint64, error) {
	if key == k.uid && k.before == 3 {
		k.before = 0
		if err := k.removeCurrent(ctx, key); err != nil {
			return 0, err
		}
	}
	return k.KeyValue.Update(ctx, key, data, revision)
}

func testMemberUpdateDeleteRaces(t *testing.T) {
	for _, fenced := range []bool{false, true} {
		for _, before := range []int{2, 3} {
			name := "legacy"
			if fenced {
				name = "fenced"
			}
			t.Run(name+string(rune('0'+before)), func(t *testing.T) {
				ctx := context.Background()
				client := newKVClient(t)
				s := NewStorageBackend(client)
				member := testMember("member", "committee-1", "first.last@example.com", "first-last", "")
				require.NoError(t, s.CreateMember(ctx, member))
				if fenced {
					selected, err := s.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
					require.NoError(t, err)
					require.NotNil(t, selected)
				}
				_, revision, err := s.GetMember(ctx, member.UID)
				require.NoError(t, err)
				client.kvStore[constants.KVBucketNameCommitteeMembers] = &deleteDuringMemberReadKV{
					KeyValue: client.kvStore[constants.KVBucketNameCommitteeMembers], uid: member.UID, before: before, fenced: fenced,
				}
				member.FirstName = "First Updated"
				_, err = s.UpdateMember(ctx, member, revision)
				var notFound errs.NotFound
				require.True(t, errors.As(err, &notFound), "logical deletion must return NotFound, got %v", err)
			})
		}
	}
}
