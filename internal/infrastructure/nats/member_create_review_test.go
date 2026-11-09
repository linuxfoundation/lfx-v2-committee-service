// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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

// retirementFailureKV injects a failure only after a primary's committed CAS,
// distinguishing cleanup errors from the accepted process-crash delivery gap.
type retirementFailureKV struct {
	jetstream.KeyValue
	failure           string
	committed, failed atomic.Bool
}

func (k *retirementFailureKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if k.failure == "read" && k.committed.Load() && strings.HasPrefix(key, fmt.Sprintf(constants.KVLookupMemberCreatePrefix, "")) && k.failed.CompareAndSwap(false, true) {
		return nil, errors.New("retirement read unavailable")
	}
	return k.KeyValue.Get(ctx, key)
}

func (k *retirementFailureKV) Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error) {
	if k.failure == "write" && k.committed.Load() && strings.HasPrefix(key, fmt.Sprintf(constants.KVLookupMemberCreatePrefix, "")) && k.failed.CompareAndSwap(false, true) {
		return 0, errors.New("retirement write unavailable")
	}
	rev, err := k.KeyValue.Update(ctx, key, value, revision)
	if err == nil && !strings.HasPrefix(key, "lookup/") {
		record, decodeErr := decodeMemberCreateRecord(value)
		if decodeErr == nil && record.Create != nil && record.Create.State == memberCreateCommitted {
			k.committed.Store(true)
		}
	}
	return rev, err
}

func (k *retirementFailureKV) Purge(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	if k.failure == "index" && k.committed.Load() && k.failed.CompareAndSwap(false, true) {
		return errors.New("retirement index cleanup unavailable")
	}
	return k.KeyValue.Purge(ctx, key, opts...)
}

func TestBulkMemberRecoveryRetirementFailure(t *testing.T) {
	for _, failure := range []string{"read", "write", "index"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "first-last")
			client := newKVClient(t)
			kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
			fault := &retirementFailureKV{KeyValue: kv, failure: failure}
			client.kvStore[constants.KVBucketNameCommitteeMembers] = fault
			s := NewStorageBackend(client)
			base := model.CommitteeBase{UID: "committee-1", Name: "Example Committee", Category: "Other"}
			data, err := json.Marshal(base)
			require.NoError(t, err)
			_, err = client.kvStore[constants.KVBucketNameCommittees].Create(ctx, base.UID, data)
			require.NoError(t, err)
			publisher := &mock.MockCommitteePublisher{}
			writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s), service.WithCommitteeRetriever(s), service.WithCommitteePublisher(publisher))
			row := func() *model.CommitteeMember {
				return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{Email: "first.last@example.com", Username: "first-last", FirstName: "First", LastName: "Last"}, SkipNotification: true}
			}
			results, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row()}, service.BulkMemberCreateOptions{SkipEnrichment: true})
			require.NoError(t, err)
			require.True(t, fault.failed.Load(), "failure must occur after committed CAS")
			require.Equal(t, model.BulkMemberAdded, results[0].Status)
			require.Len(t, publisher.IndexerMessages, 1)
			require.Equal(t, 1, publisher.MemberPutCallCount)
			created := publisher.LastEvent.(*model.CommitteeEvent).Data.(*model.CommitteeMemberCreatedEventData)
			require.True(t, created.SkipNotification)
			require.Equal(t, "first-last", created.CreatedBy)
			repeated, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row()}, service.BulkMemberCreateOptions{SkipEnrichment: true})
			require.NoError(t, err)
			require.Equal(t, model.BulkReasonAlreadyMember, repeated[0].Reason)
			require.Len(t, publisher.IndexerMessages, 1, "completed retries never replay publication")
			require.Equal(t, 1, publisher.MemberPutCallCount)
			op, err := kv.Get(ctx, memberCreateOperationKey(ctx, base.UID, row().Email))
			require.NoError(t, err)
			require.NotContains(t, string(op.Value()), "identity")
			require.NotContains(t, string(op.Value()), row().Email)
		})
	}
}

func TestBulkMemberRecoveryObsoletePreparation(t *testing.T) {
	for _, scenario := range []string{"deleted", "email moved", "missing terminal primary"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			client := newKVClient(t)
			s := NewStorageBackend(client)
			kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
			original := testMember("original", "committee-1", "first.last@example.com", "first-last", "")
			_, err := s.CreateMemberRecoverable(ctx, original, port.MemberCreateIntent{})
			require.NoError(t, err)
			if scenario == "email moved" {
				moved := *original
				moved.Email = "first.last+moved@example.com"
				_, err = s.UpdateMember(ctx, &moved, mustMemberRevision(t, s, original.UID))
				require.NoError(t, err)
			} else {
				require.NoError(t, s.DeleteMember(ctx, original.UID, mustMemberRevision(t, s, original.UID)))
				if scenario == "missing terminal primary" {
					require.NoError(t, kv.Purge(ctx, original.UID)) // Simulate older damaged state; discovery must restore the fence.
				}
			}
			current := testMember("current", original.CommitteeUID, original.Email, "first-last", "")
			require.NoError(t, s.CreateMember(ctx, current))
			base := model.CommitteeBase{UID: original.CommitteeUID, Name: "Example Committee", Category: "Other", EnableVoting: true}
			data, err := json.Marshal(base)
			require.NoError(t, err)
			_, err = client.kvStore[constants.KVBucketNameCommittees].Create(ctx, base.UID, data)
			require.NoError(t, err)
			publisher := &mock.MockCommitteePublisher{}
			writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(s), service.WithCommitteeRetriever(s), service.WithCommitteePublisher(publisher))
			results, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{{CommitteeMemberBase: model.CommitteeMemberBase{Email: original.Email}}},
				service.BulkMemberCreateOptions{Enrich: func(context.Context, *model.CommitteeMember) { t.Error("existing member must not require enrichment") }})
			require.NoError(t, err)
			require.Equal(t, model.BulkReasonAlreadyMember, results[0].Reason)
			require.Equal(t, current.UID, results[0].MemberUID)
			require.Empty(t, publisher.IndexerMessages)
			old, err := kv.Get(ctx, original.UID)
			require.NoError(t, err, "the old UID stays fenced")
			require.Contains(t, string(old.Value()), "_create")
		})
	}
}

// identityScanKV fails full scans after bootstrap and observes filtered reads.
type identityScanKV struct {
	jetstream.KeyValue
	fullScans     atomic.Int32
	filteredScans atomic.Int32
}

func (k *identityScanKV) WatchAll(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	if k.fullScans.Add(1) > 1 {
		return nil, errors.New("unexpected repeated full-bucket scan")
	}
	return k.KeyValue.WatchAll(ctx, opts...)
}
func (k *identityScanKV) Watch(ctx context.Context, filter string, opts ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	k.filteredScans.Add(1)
	return k.KeyValue.Watch(ctx, filter, opts...)
}

func TestStorage_MemberCreateIdentityIndexes(t *testing.T) {
	for _, scenario := range []string{"email", "username", "both", "mismatch", "index before preparation", "old retirement after new generation"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			client := newKVClient(t)
			kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
			counted := &identityScanKV{KeyValue: kv}
			client.kvStore[constants.KVBucketNameCommitteeMembers] = counted
			s := NewStorageBackend(client)
			// Bootstrap an empty bucket once; all following writes use indexes.
			require.NoError(t, s.AbortMemberCreatesForIdentity(ctx, "", "first.last@example.com"))
			member := testMember("pending", "committee-1", "first.last@example.com", "first-last", "")
			record := &memberCreateRecord{CommitteeMemberBase: member.CommitteeMemberBase, Create: &memberCreateState{State: memberCreatePending, Intent: &port.MemberCreateIntent{}}}
			require.NoError(t, s.indexMemberCreateIdentity(ctx, record))
			if scenario != "index before preparation" {
				data, err := json.Marshal(record)
				require.NoError(t, err)
				_, err = kv.Create(ctx, record.Create.Identity.OperationKey, data)
				require.NoError(t, err)
			}
			username, email := "", member.Email
			if scenario == "username" {
				username, email = member.Username, ""
			}
			if scenario == "both" {
				username = member.Username
			}
			if scenario == "mismatch" {
				username = "first-last-different"
			}
			require.NoError(t, s.AbortMemberCreatesForIdentity(ctx, username, email))
			if scenario == "mismatch" {
				selected, err := s.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
				require.NoError(t, err)
				require.True(t, selected.Pending)
				return
			}
			for _, key := range record.Create.Identity.keys() {
				_, err := kv.Get(ctx, key)
				require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
			}
			if scenario == "index before preparation" {
				_, err := s.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{})
				require.Error(t, err, "the early identity event fenced this UID")
			}
			if scenario == "old retirement after new generation" {
				newMember := *member
				newMember.UID = "new-generation"
				newRecord := &memberCreateRecord{CommitteeMemberBase: newMember.CommitteeMemberBase, Create: &memberCreateState{State: memberCreatePending, Intent: &port.MemberCreateIntent{}}}
				require.NoError(t, s.indexMemberCreateIdentity(ctx, newRecord))
				data, err := json.Marshal(newRecord)
				require.NoError(t, err)
				_, err = kv.Put(ctx, record.Create.Identity.OperationKey, data)
				require.NoError(t, err)
				require.NoError(t, s.retireMemberCreate(ctx, record.Create.Identity.OperationKey, member.UID, memberCreateAborted))
				for _, key := range newRecord.Create.Identity.keys() {
					_, err := kv.Get(ctx, key)
					require.NoError(t, err)
				}
			}
			require.NoError(t, s.AbortMemberCreatesForIdentity(ctx, "", "first.last+unrelated@example.com"))
			require.EqualValues(t, 1, counted.fullScans.Load())
			require.GreaterOrEqual(t, counted.filteredScans.Load(), int32(3))
		})
	}
}

type incompleteMemberCreateWatcher struct{ updates chan jetstream.KeyValueEntry }

func (w *incompleteMemberCreateWatcher) Updates() <-chan jetstream.KeyValueEntry { return w.updates }
func (*incompleteMemberCreateWatcher) Stop() error                               { return nil }

type incompleteBootstrapKV struct{ jetstream.KeyValue }

func (*incompleteBootstrapKV) WatchAll(context.Context, ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	updates := make(chan jetstream.KeyValueEntry)
	close(updates) // A closed channel without the nil sentinel is not a complete snapshot.
	return &incompleteMemberCreateWatcher{updates: updates}, nil
}

func TestStorage_MemberCreateIdentityBootstrapFailure(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
	client.kvStore[constants.KVBucketNameCommitteeMembers] = &incompleteBootstrapKV{KeyValue: kv}
	s := NewStorageBackend(client)
	require.Error(t, s.AbortMemberCreatesForIdentity(ctx, "", "first.last@example.com"))
	_, err := kv.Get(ctx, constants.KVMemberCreateIdentityIndexReady)
	require.ErrorIs(t, err, jetstream.ErrKeyNotFound, "an incomplete bootstrap must not be marked ready")
	client.kvStore[constants.KVBucketNameCommitteeMembers] = kv
	require.NoError(t, s.AbortMemberCreatesForIdentity(ctx, "", "first.last@example.com"))
	_, err = kv.Get(ctx, constants.KVMemberCreateIdentityIndexReady)
	require.NoError(t, err)
}

type identityEventBeforePreparationKV struct {
	jetstream.KeyValue
	key    string
	before func()
	once   atomic.Bool
}

func (k *identityEventBeforePreparationKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	if key == k.key && k.once.CompareAndSwap(false, true) {
		k.before()
	}
	return k.KeyValue.Create(ctx, key, value, opts...)
}

func TestStorage_MemberCreateIdentityEarlyEvent(t *testing.T) {
	ctx := context.Background()
	client := newKVClient(t)
	kv := client.kvStore[constants.KVBucketNameCommitteeMembers]
	s := NewStorageBackend(client)
	member := testMember("pending", "committee-1", "first.last@example.com", "first-last", "")
	opKey := memberCreateOperationKey(ctx, member.CommitteeUID, member.Email)
	client.kvStore[constants.KVBucketNameCommitteeMembers] = &identityEventBeforePreparationKV{KeyValue: kv, key: opKey,
		before: func() { require.NoError(t, s.AbortMemberCreatesForIdentity(ctx, member.Username, member.Email)) }}
	_, err := s.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{})
	require.Error(t, err)
	entry, err := kv.Get(ctx, opKey)
	require.NoError(t, err)
	require.NotContains(t, string(entry.Value()), member.Email, "the fenced writer must retire its delayed preparation")
	require.NotContains(t, string(entry.Value()), "intent")
	for _, key := range newMemberCreateIdentity(ctx, member).keys() {
		_, err := kv.Get(ctx, key)
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
	}
}
