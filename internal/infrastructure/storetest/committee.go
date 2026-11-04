// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// CommitteeFactory builds a fresh, empty committee store.
type CommitteeFactory func(t *testing.T) port.CommitteeReaderWriter

// RunCommitteeContract runs the committee and settings cases against the backend: create, read,
// update, delete, the name and SSO group name uniqueness, the lookups and the counters.
func RunCommitteeContract(t *testing.T, backend Backend, newStore CommitteeFactory) {
	ctx := context.Background()

	t.Run("create and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		want := Committee("uid-1", "project-1", "Committee One")
		require.NoError(t, store.Create(ctx, want))

		base, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.NotZero(t, revision)
		assert.Equal(t, normalizeBase(&want.CommitteeBase), normalizeBase(base))

		settings, settingsRevision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		assert.NotZero(t, settingsRevision)
		assert.Equal(t, "uid-1", settings.UID, "settings take the committee uid")
		wantSettings := *want.CommitteeSettings
		wantSettings.UID = "uid-1"
		assert.Equal(t, normalizeSettings(&wantSettings), normalizeSettings(settings))
	})

	t.Run("a committee with no optional fields reads back empty", func(t *testing.T) {
		store := newStore(t)
		minimal := &model.Committee{CommitteeBase: model.CommitteeBase{
			UID: "uid-min", ProjectUID: "project-1", Name: "Minimal",
			CreatedAt: Committee("x", "p", "n").CommitteeBase.CreatedAt, UpdatedAt: Committee("x", "p", "n").CommitteeBase.UpdatedAt,
		}}
		require.NoError(t, store.Create(ctx, minimal))
		base, _, err := store.GetBase(ctx, "uid-min")
		require.NoError(t, err)
		assert.Equal(t, normalizeBase(&minimal.CommitteeBase), normalizeBase(base))
		assert.Nil(t, base.Scope)
		assert.Nil(t, base.Charter)
		assert.Nil(t, base.Website)

		_, _, err = store.GetSettings(ctx, "uid-min")
		requireNotFound(t, err)
	})

	t.Run("get of a missing committee is not found, and an empty uid is invalid", func(t *testing.T) {
		store := newStore(t)
		_, _, err := store.GetBase(ctx, "missing")
		requireNotFound(t, err)
		_, _, err = store.GetSettings(ctx, "missing")
		requireNotFound(t, err)
		_, _, err = store.GetBase(ctx, "")
		requireValidation(t, err)
	})

	t.Run("a committee name is unique per project", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		requireConflict(t, store.Create(ctx, Committee("uid-2", "project-1", "Committee One")))
		require.NoError(t, store.Create(ctx, Committee("uid-3", "project-2", "Committee One")), "the same name in another project")
		require.NoError(t, store.Create(ctx, Committee("uid-4", "project-1", "Committee Two")))

		_, _, err := store.GetBase(ctx, "uid-2")
		requireNotFound(t, err)
	})

	t.Run("an SSO group name is unique, and the conflict says why", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))

		clash := Committee("uid-2", "project-2", "Committee Two")
		clash.SSOGroupName = "sso-uid-1"
		err := store.Create(ctx, clash)
		requireConflict(t, err)
		assert.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)

		// A failed create reserves nothing: its name is still free.
		require.NoError(t, store.Create(ctx, Committee("uid-3", "project-2", "Committee Two")))
	})

	t.Run("ListAllUIDs lists every committee", func(t *testing.T) {
		store := newStore(t)
		uids, err := store.ListAllUIDs(ctx)
		require.NoError(t, err)
		assert.Empty(t, uids)

		for _, c := range []*model.Committee{Committee("uid-1", "p", "One"), Committee("uid-2", "p", "Two"), Committee("uid-3", "q", "One")} {
			require.NoError(t, store.Create(ctx, c))
		}
		uids, err = store.ListAllUIDs(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"uid-1", "uid-2", "uid-3"}, uids)
	})

	t.Run("FindUIDByProjectAndName follows creates, renames and deletes", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))

		uid, err := store.FindUIDByProjectAndName(ctx, "project-1", "Committee One")
		require.NoError(t, err)
		assert.Equal(t, "uid-1", uid)
		_, err = store.FindUIDByProjectAndName(ctx, "project-1", "Other")
		requireNotFound(t, err)
		_, err = store.FindUIDByProjectAndName(ctx, "project-2", "Committee One")
		requireNotFound(t, err)

		base, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		base.Name = "Renamed"
		require.NoError(t, store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
		_, err = store.FindUIDByProjectAndName(ctx, "project-1", "Committee One")
		requireNotFound(t, err)
		uid, err = store.FindUIDByProjectAndName(ctx, "project-1", "Renamed")
		require.NoError(t, err)
		assert.Equal(t, "uid-1", uid)

		_, revision, err = store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		require.NoError(t, store.Delete(ctx, "uid-1", revision))
		_, err = store.FindUIDByProjectAndName(ctx, "project-1", "Renamed")
		requireNotFound(t, err)
	})

	t.Run("UpdateBase replaces the committee and the revision", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		base, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)

		base.Description = "Updated"
		base.Scope = nil
		require.NoError(t, store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

		got, newRevision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "Updated", got.Description)
		assert.Nil(t, got.Scope)
		assert.NotEqual(t, revision, newRevision)
	})

	t.Run("UpdateBase with a stale revision fails and changes nothing", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		base, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		base.Description = "first writer"
		require.NoError(t, store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

		base.Description = "second writer"
		backend.requireStale(t, store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
		got, _, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.Description)
	})

	t.Run("UpdateBase of a missing committee is not found", func(t *testing.T) {
		store := newStore(t)
		ghost := Committee("ghost", "project-1", "Ghost")
		requireNotFound(t, store.UpdateBase(ctx, ghost, 1))
	})

	t.Run("UpdateBase enforces the name and SSO group name, and frees the old ones", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		require.NoError(t, store.Create(ctx, Committee("uid-2", "project-1", "Committee Two")))

		base, revision, err := store.GetBase(ctx, "uid-2")
		require.NoError(t, err)
		base.Name = "Committee One"
		requireConflict(t, store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

		base.Name = "Committee Two"
		base.SSOGroupName = "sso-uid-1"
		err = store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision)
		requireConflict(t, err)
		assert.ErrorIs(t, err, errs.ErrSSOGroupNameTaken)

		// Moving to a new name frees the old one for another committee.
		base.SSOGroupName = "sso-uid-2"
		base.Name = "Committee Three"
		require.NoError(t, store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
		require.NoError(t, store.Create(ctx, Committee("uid-3", "project-1", "Committee Two")))
	})

	t.Run("UpdateSetting replaces the settings and the revision", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		settings, revision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)

		settings.MemberVisibility = "hidden"
		settings.Writers = nil
		require.NoError(t, store.UpdateSetting(ctx, settings, revision))
		got, newRevision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "hidden", got.MemberVisibility)
		assert.NotEqual(t, revision, newRevision)

		settings.MemberVisibility = "stale writer"
		backend.requireStale(t, store.UpdateSetting(ctx, settings, revision))
		got, _, err = store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, "hidden", got.MemberVisibility)
	})

	t.Run("settings keep a list that was never set apart from one set to empty", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		settings, revision, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)

		settings.Writers, settings.Auditors = nil, []model.CommitteeUser{}
		require.NoError(t, store.UpdateSetting(ctx, settings, revision))
		got, _, err := store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		assert.Nil(t, got.Writers)
		assert.NotNil(t, got.Auditors)
		assert.Empty(t, got.Auditors)
	})

	t.Run("Delete removes the committee and its settings and frees its names", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		_, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)

		backend.requireStale(t, store.Delete(ctx, "uid-1", revision+1000))
		_, _, err = store.GetBase(ctx, "uid-1")
		require.NoError(t, err, "a rejected delete changes nothing")

		require.NoError(t, store.Delete(ctx, "uid-1", revision))
		_, _, err = store.GetBase(ctx, "uid-1")
		requireNotFound(t, err)
		_, _, err = store.GetSettings(ctx, "uid-1")
		requireNotFound(t, err)
		requireNotFound(t, store.Delete(ctx, "uid-1", revision))
		require.NoError(t, store.Create(ctx, Committee("uid-2", "project-1", "Committee One")), "the name is free again")
	})

	t.Run("the counters write only when the value changes", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		_, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)

		got, changed, err := store.UpdateTotalMembers(ctx, "uid-1", 9)
		require.NoError(t, err)
		require.True(t, changed)
		assert.Equal(t, 9, got.TotalMembers)
		_, newRevision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.NotEqual(t, revision, newRevision)

		got, changed, err = store.UpdateTotalMembers(ctx, "uid-1", 9)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Nil(t, got)
		_, sameRevision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.Equal(t, newRevision, sameRevision, "an unchanged value writes nothing")

		got, changed, err = store.UpdateHasMailingList(ctx, "uid-1", false)
		require.NoError(t, err)
		require.True(t, changed)
		assert.False(t, got.HasMailingList)
		_, changed, err = store.UpdateHasMailingList(ctx, "uid-1", false)
		require.NoError(t, err)
		assert.False(t, changed)

		_, _, err = store.UpdateTotalMembers(ctx, "missing", 1)
		requireNotFound(t, err)
		_, _, err = store.UpdateHasMailingList(ctx, "missing", true)
		requireNotFound(t, err)
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.Create(ctx, Committee("uid-1", "project-1", "Committee One")))
		base, revision, err := store.GetBase(ctx, "uid-1")
		require.NoError(t, err)

		wins := race(t, func(writer int) error {
			update := *base
			update.Description = fmt.Sprintf("writer %d", writer)
			return store.UpdateBase(ctx, &model.Committee{CommitteeBase: update}, revision)
		}, backend.requireStale)
		assert.Equal(t, 1, wins)
	})
}
