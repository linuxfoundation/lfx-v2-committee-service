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

// lockKV is a minimal in-memory KeyValue with injectable failures, used to test how the storage
// layer reserves, moves, and releases committee lookup keys.
type lockKV struct {
	jetstream.KeyValue
	values map[string][]byte

	failCreateFor string // Create fails without storing for this key
	ambiguousFor  string // Create/Update store the value, then report an error
	failUpdate    bool   // Update fails without storing
	purgeErr      error
}

func newLockKV() *lockKV { return &lockKV{values: map[string][]byte{}} }

func (k *lockKV) Create(_ context.Context, key string, value []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	if key == k.failCreateFor {
		return 0, errors.New("simulated create failure")
	}
	if _, ok := k.values[key]; ok {
		return 0, jetstream.ErrKeyExists
	}
	k.values[key] = value
	if key == k.ambiguousFor {
		return 0, errors.New("simulated timeout after the write was committed")
	}
	return 1, nil
}

func (k *lockKV) Update(_ context.Context, key string, value []byte, _ uint64) (uint64, error) {
	if _, ok := k.values[key]; !ok {
		return 0, jetstream.ErrKeyNotFound
	}
	if k.failUpdate {
		return 0, errors.New("simulated update failure")
	}
	k.values[key] = value
	if key == k.ambiguousFor {
		return 0, errors.New("simulated timeout after the write was committed")
	}
	return 2, nil
}

func (k *lockKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	value, ok := k.values[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return &mockEntry{value: value, rev: 1}, nil
}

func (k *lockKV) Delete(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if _, ok := k.values[key]; !ok {
		return jetstream.ErrKeyNotFound
	}
	delete(k.values, key)
	return nil
}

func (k *lockKV) Purge(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	if k.purgeErr != nil {
		return k.purgeErr
	}
	delete(k.values, key)
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

	t.Run("an ambiguous committee write keeps the lookup keys so a duplicate cannot be created", func(t *testing.T) {
		s, committees, _ := newCommitteeLockStorage()
		committees.ambiguousFor = "c-1"
		c := testCommittee("c-1", "project-1", "TSC", "project-1-tsc")

		require.Error(t, s.Create(ctx, c))

		assert.Contains(t, committees.values, nameLockKey(ctx, c))
		assert.Contains(t, committees.values, ssoLockKey("project-1-tsc"))
		var conflict errs.Conflict
		assert.ErrorAs(t, s.Create(ctx, testCommittee("c-2", "project-1", "TSC", "")), &conflict)
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

	t.Run("an ambiguous write keeps the new lookup keys so a duplicate cannot be created", func(t *testing.T) {
		s, committees, _ := seed(t)
		committees.ambiguousFor = "c-1"
		updated := renamed("Steering Committee", "project-1-tsc")

		require.Error(t, s.UpdateBase(ctx, updated, 1))

		assert.Contains(t, committees.values, nameLockKey(ctx, updated))
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
