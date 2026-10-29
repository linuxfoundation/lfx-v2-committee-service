// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
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

	// cancelOnCreate makes Create for that key cancel the request context and fail without storing,
	// simulating a client disconnect between reserving the lookup key and writing the record. Get
	// and Purge then honor the (cancelled) context they are given, like a real client would.
	cancelOnCreate string
	cancel         context.CancelFunc
}

func newMemKV() *memKV { return &memKV{keys: map[string][]byte{}} }

func (m *memKV) Create(_ context.Context, key string, value []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	if key == m.failCreateFor {
		return 0, errors.New("simulated create failure")
	}
	if key == m.cancelOnCreate {
		m.cancel()
		return 0, context.Canceled
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

func (m *memKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (m *memKV) Purge(ctx context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

	t.Run("a cancelled request still releases the lookup key", func(t *testing.T) {
		kv := newMemKV()
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		kv.cancel, kv.cancelOnCreate = cancel, "invite-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeInvites, kv)

		require.Error(t, s.CreateInvite(reqCtx, testInvite("invite-1", "first.last@example.com")))

		assert.Empty(t, kv.keys, "the lookup key must not stay locked because the request was cancelled")
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

	t.Run("a cancelled request still releases the lookup key", func(t *testing.T) {
		kv := newMemKV()
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		kv.cancel, kv.cancelOnCreate = cancel, "application-1"
		s := newTestStorageWithBucket(constants.KVBucketNameCommitteeApplications, kv)

		require.Error(t, s.CreateApplication(reqCtx, testApplication("application-1", "first.last@example.com")))

		assert.Empty(t, kv.keys, "the lookup key must not stay locked because the request was cancelled")
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

// lockKV is a minimal in-memory KeyValue with injectable failures, used to test how the storage
// layer reserves, moves, and releases committee lookup keys.
type lockKV struct {
	jetstream.KeyValue
	values map[string][]byte
	revs   map[string]uint64 // per-key revision: 1 on create, +1 on every update

	// Hooks to interleave another writer with an operation under test. Each runs once.
	beforeCreate   map[string]func() // at the start of Create for that key
	beforeUpdate   func()            // at the start of Update, before the revision check
	afterUpdate    func()            // after Update stored its value, before it returns
	afterAmbiguous func()            // after an ambiguous Update stored its value and before it reports the error

	failCreateFor string // Create fails without storing for this key
	ambiguousFor  string // Create/Update store the value, then report an error
	failUpdate    bool   // Update fails without storing
	purgeErr      error

	getErrFor          string // Get fails with a generic (not NotFound) error for this key
	failGetAfterUpdate bool   // once Update was attempted, Get fails with a generic error
	updateAttempted    bool
	deleteFailFor      string // Delete fails without applying for this key
	ambiguousDeleteFor string // Delete applies, then reports an error

	// cancelOnCreate makes Create for that key cancel the request context and fail without storing,
	// simulating a client disconnect. Get and Purge then honor the (cancelled) context they are given.
	cancelOnCreate string
	cancel         context.CancelFunc
}

func newLockKV() *lockKV { return &lockKV{values: map[string][]byte{}, revs: map[string]uint64{}} }

func (k *lockKV) Create(_ context.Context, key string, value []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	if hook, ok := k.beforeCreate[key]; ok {
		delete(k.beforeCreate, key)
		hook()
	}
	if key == k.failCreateFor {
		return 0, errors.New("simulated create failure")
	}
	if key == k.cancelOnCreate {
		k.cancel()
		return 0, context.Canceled
	}
	if _, ok := k.values[key]; ok {
		return 0, jetstream.ErrKeyExists
	}
	k.values[key] = value
	k.revs[key] = 1
	if key == k.ambiguousFor {
		if hook := k.afterAmbiguous; hook != nil {
			k.afterAmbiguous = nil
			hook()
		}
		return 0, errors.New("simulated timeout after the write was committed")
	}
	return 1, nil
}

func (k *lockKV) Update(_ context.Context, key string, value []byte, revision uint64) (uint64, error) {
	if hook := k.beforeUpdate; hook != nil {
		k.beforeUpdate = nil
		hook()
	}
	k.updateAttempted = true
	if _, ok := k.values[key]; !ok {
		return 0, jetstream.ErrKeyNotFound
	}
	if k.failUpdate {
		return 0, errors.New("simulated update failure")
	}
	if revision != k.revs[key] {
		return 0, errors.New("wrong last sequence")
	}
	k.values[key] = value
	k.revs[key]++
	if key == k.ambiguousFor {
		if hook := k.afterAmbiguous; hook != nil {
			k.afterAmbiguous = nil
			hook()
		}
		return 0, errors.New("simulated timeout after the write was committed")
	}
	if hook := k.afterUpdate; hook != nil {
		k.afterUpdate = nil
		hook()
	}
	return k.revs[key], nil
}

func (k *lockKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == k.getErrFor || (k.failGetAfterUpdate && k.updateAttempted) {
		return nil, errors.New("simulated read failure")
	}
	value, ok := k.values[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return &mockEntry{value: value, rev: k.revs[key]}, nil
}

func (k *lockKV) Delete(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if _, ok := k.values[key]; !ok {
		return jetstream.ErrKeyNotFound
	}
	if key == k.deleteFailFor {
		return errors.New("simulated delete failure")
	}
	delete(k.values, key)
	delete(k.revs, key)
	if key == k.ambiguousDeleteFor {
		return errors.New("simulated timeout after the delete was applied")
	}
	return nil
}

func (k *lockKV) Purge(ctx context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if k.purgeErr != nil {
		return k.purgeErr
	}
	delete(k.values, key)
	delete(k.revs, key)
	return nil
}

func newCommitteeLockStorage() (*storage, *lockKV, *lockKV) {
	committees, settings := newLockKV(), newLockKV()
	s := &storage{client: &NATSClient{kvStore: map[string]jetstream.KeyValue{
		constants.KVBucketNameCommittees:        committees,
		constants.KVBucketNameCommitteeSettings: settings,
	}}}
	return s, committees, settings
}

func testCommittee(uid, project, name, sso string) *model.Committee {
	return &model.Committee{
		CommitteeBase:     model.CommitteeBase{UID: uid, ProjectUID: project, Name: name, SSOGroupName: sso},
		CommitteeSettings: &model.CommitteeSettings{UID: uid},
	}
}

func nameLockKey(ctx context.Context, c *model.Committee) string {
	return fmt.Sprintf(constants.KVLookupPrefix, c.BuildIndexKey(ctx))
}

func ssoLockKey(sso string) string {
	return fmt.Sprintf(constants.KVLookupSSOGroupNamePrefix, sso)
}

func TestStorage_CreateCommittee(t *testing.T) {
	ctx := context.Background()

	t.Run("writes the committee, its settings and both lookup keys", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		c := testCommittee("c-1", "project-1", "Technical Steering Committee", "project-1-tsc")

		require.NoError(t, s.Create(ctx, c))

		assert.Contains(t, committees.values, "c-1")
		assert.Contains(t, settings.values, "c-1")
		assert.Equal(t, []byte("c-1"), committees.values[nameLockKey(ctx, c)])
		assert.Equal(t, []byte("c-1"), committees.values[ssoLockKey("project-1-tsc")])
	})

	t.Run("a committee without an SSO group name reserves only the name", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		c := testCommittee("c-1", "project-1", "Technical Steering Committee", "")

		require.NoError(t, s.Create(ctx, c))

		assert.Len(t, committees.values, 2, "the committee and its name lock")
	})

	t.Run("a duplicate name is a conflict and changes nothing", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))
		committeeKeys, settingsKeys := len(committees.values), len(settings.values)

		err := s.Create(ctx, testCommittee("c-2", "project-1", "TSC", "project-1-tsc-2"))

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.NotErrorIs(t, err, errs.ErrSSOGroupNameTaken)
		assert.Len(t, committees.values, committeeKeys)
		assert.Len(t, settings.values, settingsKeys)
	})

	t.Run("the same name in another project is allowed", func(t *testing.T) {
		s, _, _ := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "")))

		require.NoError(t, s.Create(ctx, testCommittee("c-2", "project-2", "TSC", "")))
	})

	t.Run("a taken SSO group name wraps the sentinel and releases the name lock", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))
		other := testCommittee("c-2", "project-1", "Other", "project-1-tsc")

		err := s.Create(ctx, other)

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)
		assert.NotContains(t, committees.values, nameLockKey(ctx, other), "the name lock must not be left behind")
		assert.NotContains(t, committees.values, "c-2")
	})

	t.Run("a failed committee write releases both lookup keys", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		committees.failCreateFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")

		require.Error(t, s.Create(ctx, c))
		assert.Empty(t, committees.values)

		committees.failCreateFor = ""
		require.NoError(t, s.Create(ctx, c), "the name and SSO group name must be reusable")
	})

	t.Run("a failed settings write removes the committee and releases the lookup keys", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		settings.failCreateFor = "c-1"

		require.Error(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))

		assert.Empty(t, committees.values)
		assert.Empty(t, settings.values)
	})

	t.Run("an ambiguous committee write that landed is treated as created", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		committees.ambiguousFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")

		require.NoError(t, s.Create(ctx, c))

		assert.Contains(t, settings.values, "c-1", "the create continues and writes the settings")
		assert.Contains(t, committees.values, nameLockKey(ctx, c))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("a failed committee write whose state cannot be read keeps the lookup keys", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		committees.failCreateFor = "c-1"
		committees.getErrFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")

		require.Error(t, s.Create(ctx, c))

		assert.Contains(t, committees.values, nameLockKey(ctx, c), "an unconfirmed outcome must keep the reservation")
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("a cancelled request still releases the lookup keys", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		committees.cancel, committees.cancelOnCreate = cancel, "c-1"

		require.Error(t, s.Create(reqCtx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))

		assert.Empty(t, committees.values, "the locks must not stay held because the request was cancelled")
	})

	t.Run("an ambiguous committee write changed by another writer before the read-back still counts as created", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		committees.ambiguousFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		committees.afterAmbiguous = func() {
			stored := &model.CommitteeBase{}
			require.NoError(t, json.Unmarshal(committees.values["c-1"], stored))
			stored.HasMailingList = true
			changed, err := json.Marshal(stored)
			require.NoError(t, err)
			committees.values["c-1"] = changed
		}

		require.NoError(t, s.Create(ctx, c))

		assert.Contains(t, committees.values, nameLockKey(ctx, c), "the live committee must keep its name lock")
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
		assert.Contains(t, settings.values, "c-1", "the create continues and writes the settings")
	})

	t.Run("a committee write that something unrelated replaced releases the reservations", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		committees.ambiguousFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		other := testCommittee("c-1", "project-9", "Something Else", "")
		committees.afterAmbiguous = func() {
			changed, err := json.Marshal(other.CommitteeBase)
			require.NoError(t, err)
			committees.values["c-1"] = changed
		}

		require.Error(t, s.Create(ctx, c))

		assert.NotContains(t, committees.values, nameLockKey(ctx, c))
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("a committee changed by another writer is kept when the settings write then fails", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		committees.ambiguousFor = "c-1"
		settings.failCreateFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		committees.afterAmbiguous = func() {
			stored := &model.CommitteeBase{}
			require.NoError(t, json.Unmarshal(committees.values["c-1"], stored))
			stored.HasMailingList = true
			changed, err := json.Marshal(stored)
			require.NoError(t, err)
			committees.values["c-1"] = changed
		}

		require.Error(t, s.Create(ctx, c))

		assert.Contains(t, committees.values, "c-1", "the other writer's changes must not be rolled back")
		assert.Contains(t, committees.values, nameLockKey(ctx, c))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("an ambiguous settings write that landed is treated as created", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		settings.ambiguousFor = "c-1"

		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))

		assert.Contains(t, committees.values, "c-1")
		assert.Contains(t, settings.values, "c-1")
	})

	t.Run("a failed settings write whose committee cannot be removed keeps the committee and its lookup keys", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		settings.failCreateFor = "c-1"
		committees.deleteFailFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")

		require.Error(t, s.Create(ctx, c))

		assert.Contains(t, committees.values, "c-1")
		assert.Contains(t, committees.values, nameLockKey(ctx, c))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("nil committee is a validation error", func(t *testing.T) {
		s, _, _ := newCommitteeLockStorage()

		var validation errs.Validation
		assert.ErrorAs(t, s.Create(ctx, nil), &validation)
	})
}

func TestStorage_UpdateBaseCommittee(t *testing.T) {
	ctx := context.Background()

	seed := func(t *testing.T) (*storage, *lockKV, *model.Committee) {
		t.Helper()
		s, committees, _ := newCommitteeLockStorage()
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		require.NoError(t, s.Create(ctx, c))
		return s, committees, c
	}
	renamed := func(name, sso string) *model.Committee {
		return testCommittee("c-1", "project-1", name, sso)
	}

	t.Run("a rename moves the name lock", func(t *testing.T) {
		s, committees, original := seed(t)
		updated := renamed("Steering Committee", "project-1-tsc")

		require.NoError(t, s.UpdateBase(ctx, updated, 1))

		assert.NotContains(t, committees.values, nameLockKey(ctx, original), "the old name must be released")
		assert.Equal(t, []byte("c-1"), committees.values[nameLockKey(ctx, updated)])
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"), "an unchanged SSO group name keeps its lock")
	})

	t.Run("moving to another project moves the name lock", func(t *testing.T) {
		s, committees, original := seed(t)
		moved := testCommittee("c-1", "project-2", "TSC", "project-1-tsc")

		require.NoError(t, s.UpdateBase(ctx, moved, 1))

		assert.NotContains(t, committees.values, nameLockKey(ctx, original))
		assert.Contains(t, committees.values, nameLockKey(ctx, moved))
	})

	t.Run("renaming to a taken name is a conflict and changes nothing", func(t *testing.T) {
		s, committees, original := seed(t)
		require.NoError(t, s.Create(ctx, testCommittee("c-2", "project-1", "Other", "")))
		before := len(committees.values)

		err := s.UpdateBase(ctx, renamed("Other", "project-1-tsc"), 1)

		var conflict errs.Conflict
		require.ErrorAs(t, err, &conflict)
		assert.Len(t, committees.values, before)
		assert.Contains(t, committees.values, nameLockKey(ctx, original))
	})

	t.Run("a new SSO group name moves the SSO lock", func(t *testing.T) {
		s, committees, _ := seed(t)

		require.NoError(t, s.UpdateBase(ctx, renamed("TSC", "project-1-tsc-2"), 1))

		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc"))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc-2"))
	})

	t.Run("a taken SSO group name wraps the sentinel and releases the new name lock", func(t *testing.T) {
		s, committees, _ := seed(t)
		require.NoError(t, s.Create(ctx, testCommittee("c-2", "project-1", "Other", "project-1-other")))
		updated := renamed("Steering Committee", "project-1-other")

		err := s.UpdateBase(ctx, updated, 1)

		require.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)
		assert.NotContains(t, committees.values, nameLockKey(ctx, updated), "the new name lock must not be left behind")
	})

	t.Run("an update that changes neither name nor SSO group name reserves nothing", func(t *testing.T) {
		s, committees, _ := seed(t)
		before := len(committees.values)

		require.NoError(t, s.UpdateBase(ctx, renamed("TSC", "project-1-tsc"), 1))

		assert.Len(t, committees.values, before)
	})

	t.Run("a failed write releases the new lookup keys and keeps the old ones", func(t *testing.T) {
		s, committees, original := seed(t)
		committees.failUpdate = true
		updated := renamed("Steering Committee", "project-1-tsc-2")

		require.Error(t, s.UpdateBase(ctx, updated, 1))

		assert.NotContains(t, committees.values, nameLockKey(ctx, updated))
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc-2"))
		assert.Contains(t, committees.values, nameLockKey(ctx, original))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("an ambiguous write that landed is treated as updated and releases the old lookup keys", func(t *testing.T) {
		s, committees, original := seed(t)
		committees.ambiguousFor = "c-1"
		updated := renamed("Steering Committee", "project-1-tsc-2")

		require.NoError(t, s.UpdateBase(ctx, updated, 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, updated))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc-2"))
		assert.NotContains(t, committees.values, nameLockKey(ctx, original), "the old name must not stay locked")
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc"), "the old SSO group name must not stay locked")
	})

	t.Run("a failed write whose state cannot be read keeps the new lookup keys", func(t *testing.T) {
		s, committees, _ := seed(t)
		committees.failUpdate = true
		committees.failGetAfterUpdate = true // the initial read succeeds; the read-back after the failed write does not
		updated := renamed("Steering Committee", "project-1-tsc")

		require.Error(t, s.UpdateBase(ctx, updated, 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, updated), "an unconfirmed outcome must keep the reservation")
	})

	t.Run("a cancelled request still releases the new lookup keys", func(t *testing.T) {
		s, committees, _ := seed(t)
		committees.failUpdate = true
		updated := renamed("Steering Committee", "project-1-tsc-2")
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		// Cancel after the initial read and the lock reservations, just before the write, like a client
		// that disconnects mid-request.
		committees.beforeUpdate = cancel

		require.Error(t, s.UpdateBase(reqCtx, updated, 1))

		require.True(t, committees.updateAttempted, "the update must have been attempted, so the rollback ran")
		assert.NotContains(t, committees.values, nameLockKey(ctx, updated))
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc-2"))
	})

	t.Run("moving back to a name whose lock the committee already owns is not a conflict", func(t *testing.T) {
		s, committees, original := seed(t)
		// A lock left over from earlier data: this committee already owns the target name's lock.
		target := renamed("Steering Committee", "project-1-tsc")
		committees.values[nameLockKey(ctx, target)] = []byte("c-1")

		require.NoError(t, s.UpdateBase(ctx, target, 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, target), "an already-owned lock must be kept")
		assert.NotContains(t, committees.values, nameLockKey(ctx, original))
	})

	t.Run("a lock owned by another committee is not released", func(t *testing.T) {
		s, committees, original := seed(t)
		committees.values[nameLockKey(ctx, original)] = []byte("some-other-committee")

		require.NoError(t, s.UpdateBase(ctx, renamed("Steering Committee", "project-1-tsc"), 1))

		assert.Equal(t, []byte("some-other-committee"), committees.values[nameLockKey(ctx, original)])
	})

	t.Run("a failed SSO reservation does not release a name lock a concurrent update committed", func(t *testing.T) {
		s, committees, _ := seed(t)
		require.NoError(t, s.Create(ctx, testCommittee("c-2", "project-1", "Other", "project-1-other")))
		losing := renamed("Steering Committee", "project-1-other") // its SSO group name is taken by c-2
		winning := renamed("Steering Committee", "project-1-tsc")
		winning.Category = "Winner"
		// The losing update reserves the new name first; before it tries the SSO group name, the winning
		// update reuses that name lock and commits.
		committees.beforeCreate = map[string]func(){
			ssoLockKey("project-1-other"): func() { require.NoError(t, s.UpdateBase(ctx, winning, 1)) },
		}

		err := s.UpdateBase(ctx, losing, 1)

		require.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)
		assert.Contains(t, committees.values, nameLockKey(ctx, winning), "the stored committee must keep its name lock")
	})

	t.Run("a stale revision fails the update and releases the new lookup keys", func(t *testing.T) {
		s, committees, _ := seed(t)
		require.NoError(t, s.UpdateBase(ctx, renamed("Steering Committee", "project-1-tsc"), 1))
		stale := renamed("Technical Committee", "project-1-tsc-2")

		require.Error(t, s.UpdateBase(ctx, stale, 1), "revision 1 is stale after the first update")

		assert.NotContains(t, committees.values, nameLockKey(ctx, stale))
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc-2"))
	})

	t.Run("a losing concurrent update does not release a lock the winning update uses", func(t *testing.T) {
		s, committees, _ := seed(t)
		losing := renamed("Steering Committee", "project-1-tsc")
		winning := renamed("Steering Committee", "project-1-tsc")
		winning.Category = "Winner"
		// The losing update reserves the new name first; the winning update then reuses that lock and
		// commits at the same revision before the losing update's write.
		committees.beforeUpdate = func() {
			require.NoError(t, s.UpdateBase(ctx, winning, 1))
		}

		require.Error(t, s.UpdateBase(ctx, losing, 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, winning), "the stored committee must keep its name lock")
		stored := &model.CommitteeBase{}
		require.NoError(t, json.Unmarshal(committees.values["c-1"], stored))
		assert.Equal(t, "Winner", stored.Category)
	})

	t.Run("an ambiguous rename that is later changed by another writer keeps the locks the committee uses", func(t *testing.T) {
		s, committees, original := seed(t)
		committees.ambiguousFor = "c-1"
		updated := renamed("Steering Committee", "project-1-tsc-2")
		// Another writer (for example a member count sync) changes the record before the read-back,
		// so the stored bytes no longer equal what the update wrote, but the rename is live.
		committees.afterAmbiguous = func() {
			stored := &model.CommitteeBase{}
			require.NoError(t, json.Unmarshal(committees.values["c-1"], stored))
			stored.TotalMembers = 5
			changed, err := json.Marshal(stored)
			require.NoError(t, err)
			committees.values["c-1"] = changed
		}

		require.Error(t, s.UpdateBase(ctx, updated, 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, updated))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc-2"))
		assert.NotContains(t, committees.values, nameLockKey(ctx, original), "the rename is live, so the old name must not stay locked")
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc"), "the old SSO group name must not stay locked")
	})

	t.Run("a stale lock the committee holds again by cleanup time is kept", func(t *testing.T) {
		s, committees, original := seed(t)
		originalBytes := committees.values["c-1"]
		// Another update renames the committee back to its original name before this update's cleanup.
		committees.afterUpdate = func() { committees.values["c-1"] = originalBytes }

		require.NoError(t, s.UpdateBase(ctx, renamed("Steering Committee", "project-1-tsc"), 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, original), "the name the committee holds again must stay locked")
	})

	t.Run("clearing the SSO group name releases the old SSO lock", func(t *testing.T) {
		s, committees, _ := seed(t)

		require.NoError(t, s.UpdateBase(ctx, renamed("TSC", ""), 1))

		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc"))
	})

	t.Run("an unknown committee is not found", func(t *testing.T) {
		s, _, _ := newCommitteeLockStorage()

		var notFound errs.NotFound
		assert.ErrorAs(t, s.UpdateBase(ctx, renamed("TSC", ""), 1), &notFound)
	})
}

func TestStorage_DeleteCommittee(t *testing.T) {
	ctx := context.Background()

	t.Run("removes the committee and settings and releases both lookup keys", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		require.NoError(t, s.Create(ctx, c))

		require.NoError(t, s.Delete(ctx, "c-1", 1))

		assert.Empty(t, committees.values)
		assert.Empty(t, settings.values)
		require.NoError(t, s.Create(ctx, testCommittee("c-2", "project-1", "TSC", "project-1-tsc")), "the name and SSO group name must be reusable")
	})

	t.Run("still releases the lookup keys when the settings are already gone", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))
		delete(settings.values, "c-1")

		require.NoError(t, s.Delete(ctx, "c-1", 1))

		assert.Empty(t, committees.values)
	})

	t.Run("a settings delete failure still releases the lookup keys", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))
		settings.deleteFailFor = "c-1"

		err := s.Delete(ctx, "c-1", 1)

		require.Error(t, err)
		assert.NotContains(t, committees.values, "c-1")
		assert.Empty(t, committees.values, "the name and SSO group name must not stay locked after the committee is gone")
	})

	t.Run("a delete that was applied despite an error continues and releases the lookup keys", func(t *testing.T) {
		s, committees, settings := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))
		committees.ambiguousDeleteFor = "c-1"

		require.NoError(t, s.Delete(ctx, "c-1", 1))

		assert.Empty(t, committees.values)
		assert.Empty(t, settings.values)
	})

	t.Run("a failed delete that cannot be confirmed keeps the committee and its lookup keys", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		require.NoError(t, s.Create(ctx, c))
		committees.deleteFailFor = "c-1"

		require.Error(t, s.Delete(ctx, "c-1", 1))

		assert.Contains(t, committees.values, "c-1")
		assert.Contains(t, committees.values, nameLockKey(ctx, c))
	})

	t.Run("a lookup key owned by another committee is not released", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")
		require.NoError(t, s.Create(ctx, c))
		committees.values[nameLockKey(ctx, c)] = []byte("some-other-committee")

		require.NoError(t, s.Delete(ctx, "c-1", 1))

		assert.Equal(t, []byte("some-other-committee"), committees.values[nameLockKey(ctx, c)])
		assert.NotContains(t, committees.values, ssoLockKey("project-1-tsc"), "the SSO lock this committee owns is released")
	})

	t.Run("a failure to release a lookup key does not fail the delete", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		require.NoError(t, s.Create(ctx, testCommittee("c-1", "project-1", "TSC", "project-1-tsc")))
		committees.purgeErr = errors.New("simulated purge failure")

		require.NoError(t, s.Delete(ctx, "c-1", 1))

		assert.NotContains(t, committees.values, "c-1")
	})

	t.Run("an unknown committee is not found", func(t *testing.T) {
		s, _, _ := newCommitteeLockStorage()

		var notFound errs.NotFound
		assert.ErrorAs(t, s.Delete(ctx, "missing", 1), &notFound)
	})
}
