// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// memKV is a minimal in-memory KeyValue that records Create and Delete behavior. failCreateFor
// makes Create fail for a specific key so tests can exercise the "lookup key reserved, record write
// failed" path.
type memKV struct {
	jetstream.KeyValue
	keys          map[string][]byte
	failCreateFor string
	// ambiguousCreateFor makes Create store the value and then report an error, simulating a
	// timeout after the server already committed the write.
	ambiguousCreateFor string
	deleteErr          error
}

func newMemKV() *memKV { return &memKV{keys: map[string][]byte{}} }

func (m *memKV) Create(_ context.Context, key string, value []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	if key == m.failCreateFor {
		return 0, errors.New("simulated create failure")
	}
	if _, ok := m.keys[key]; ok {
		return 0, jetstream.ErrKeyExists
	}
	m.keys[key] = value
	if key == m.ambiguousCreateFor {
		return 0, errors.New("simulated timeout after the write was committed")
	}
	return uint64(len(m.keys)), nil
}

func (m *memKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	value, ok := m.keys[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return &mockEntry{value: value, rev: 1}, nil
}

func (m *memKV) Delete(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.keys, key)
	return nil
}

func (m *memKV) Purge(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.keys, key)
	return nil
}

func newTestStorageWithBucket(bucket string, kv jetstream.KeyValue) *storage {
	return &storage{client: &NATSClient{kvStore: map[string]jetstream.KeyValue{bucket: kv}}}
}

func testInvite(uid, email string) *model.CommitteeInvite {
	return &model.CommitteeInvite{UID: uid, CommitteeUID: "committee-1", InviteeEmail: email}
}

func testApplication(uid, email string) *model.CommitteeApplication {
	return &model.CommitteeApplication{UID: uid, CommitteeUID: "committee-1", ApplicantEmail: email}
}

func TestStorage_CreateInvite(t *testing.T) {
	ctx := context.Background()
	lookupKey := func(i *model.CommitteeInvite) string {
		return fmt.Sprintf(constants.KVLookupInvitePrefix, i.BuildIndexKey(ctx))
	}

	t.Run("writes the record and the uniqueness lookup key", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)
		invite := testInvite("invite-1", "first.last@example.com")

		require.NoError(t, s.CreateInvite(ctx, invite))

		assert.Contains(t, kv.keys, "invite-1")
		assert.Equal(t, []byte("invite-1"), kv.keys[lookupKey(invite)])
	})

	t.Run("duplicate email in the same committee is a conflict and changes nothing", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)
		require.NoError(t, s.CreateInvite(ctx, testInvite("invite-1", "first.last@example.com")))
		keysBefore := len(kv.keys)

		err := s.CreateInvite(ctx, testInvite("invite-2", " First.Last@Example.com "))

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.Len(t, kv.keys, keysBefore)
		assert.NotContains(t, kv.keys, "invite-2")
	})

	t.Run("same email in a different committee is allowed", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)
		require.NoError(t, s.CreateInvite(ctx, testInvite("invite-1", "first.last@example.com")))

		other := testInvite("invite-2", "first.last@example.com")
		other.CommitteeUID = "committee-2"

		require.NoError(t, s.CreateInvite(ctx, other))
	})

	t.Run("failed record write releases the lookup key so the pair is not locked", func(t *testing.T) {
		kv := newMemKV()
		kv.failCreateFor = "invite-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)

		err := s.CreateInvite(ctx, testInvite("invite-1", "first.last@example.com"))

		require.Error(t, err)
		assert.Empty(t, kv.keys)

		kv.failCreateFor = ""
		require.NoError(t, s.CreateInvite(ctx, testInvite("invite-1", "first.last@example.com")))
	})

	t.Run("ambiguous write error keeps the lookup key so a duplicate cannot be created", func(t *testing.T) {
		kv := newMemKV()
		kv.ambiguousCreateFor = "invite-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)
		invite := testInvite("invite-1", "first.last@example.com")

		require.Error(t, s.CreateInvite(ctx, invite))

		assert.Contains(t, kv.keys, "invite-1", "the record was committed")
		assert.Contains(t, kv.keys, lookupKey(invite), "the lookup key must be kept while the record may exist")

		var conflict errs.Conflict
		assert.ErrorAs(t, s.CreateInvite(ctx, testInvite("invite-2", "first.last@example.com")), &conflict)
	})

	t.Run("failure to release the lookup key does not mask the create error", func(t *testing.T) {
		kv := newMemKV()
		kv.failCreateFor = "invite-1"
		kv.deleteErr = errors.New("simulated delete failure")
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)

		err := s.CreateInvite(ctx, testInvite("invite-1", "first.last@example.com"))

		var unexpected errs.Unexpected
		assert.ErrorAs(t, err, &unexpected)
	})

	t.Run("nil invite is a validation error", func(t *testing.T) {
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, newMemKV())

		var validation errs.Validation
		assert.ErrorAs(t, s.CreateInvite(ctx, nil), &validation)
	})
}

func TestStorage_CreateApplication(t *testing.T) {
	ctx := context.Background()
	lookupKey := func(a *model.CommitteeApplication) string {
		return fmt.Sprintf(constants.KVLookupApplicationPrefix, a.BuildIndexKey(ctx))
	}

	t.Run("writes the record and the uniqueness lookup key", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, kv)
		application := testApplication("application-1", "first.last@example.com")

		require.NoError(t, s.CreateApplication(ctx, application))

		assert.Contains(t, kv.keys, "application-1")
		assert.Equal(t, []byte("application-1"), kv.keys[lookupKey(application)])
	})

	t.Run("duplicate applicant in the same committee is a conflict and changes nothing", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, kv)
		require.NoError(t, s.CreateApplication(ctx, testApplication("application-1", "first.last@example.com")))
		keysBefore := len(kv.keys)

		err := s.CreateApplication(ctx, testApplication("application-2", "first.last@example.com"))

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.Len(t, kv.keys, keysBefore)
		assert.NotContains(t, kv.keys, "application-2")
	})

	t.Run("failed record write releases the lookup key so the pair is not locked", func(t *testing.T) {
		kv := newMemKV()
		kv.failCreateFor = "application-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, kv)

		err := s.CreateApplication(ctx, testApplication("application-1", "first.last@example.com"))

		require.Error(t, err)
		assert.Empty(t, kv.keys)

		kv.failCreateFor = ""
		require.NoError(t, s.CreateApplication(ctx, testApplication("application-1", "first.last@example.com")))
	})

	t.Run("ambiguous write error keeps the lookup key so a duplicate cannot be created", func(t *testing.T) {
		kv := newMemKV()
		kv.ambiguousCreateFor = "application-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, kv)
		application := testApplication("application-1", "first.last@example.com")

		require.Error(t, s.CreateApplication(ctx, application))

		assert.Contains(t, kv.keys, lookupKey(application))

		var conflict errs.Conflict
		assert.ErrorAs(t, s.CreateApplication(ctx, testApplication("application-2", "first.last@example.com")), &conflict)
	})

	t.Run("delete releases the lookup key so the applicant can apply again", func(t *testing.T) {
		kv := newMemKV()
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, kv)
		application := testApplication("application-1", "first.last@example.com")
		require.NoError(t, s.CreateApplication(ctx, application))

		require.NoError(t, s.DeleteApplication(ctx, application))

		assert.Empty(t, kv.keys)
		require.NoError(t, s.CreateApplication(ctx, testApplication("application-2", "first.last@example.com")))
	})

	t.Run("nil application is a validation error", func(t *testing.T) {
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, newMemKV())

		var validation errs.Validation
		assert.ErrorAs(t, s.CreateApplication(ctx, nil), &validation)
	})
}
