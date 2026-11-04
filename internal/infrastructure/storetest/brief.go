// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// BriefStore is what the weekly brief suite needs of a backend.
type BriefStore interface {
	port.GroupWeeklyBriefReader
	port.GroupWeeklyBriefWriter
}

// BriefFactory builds a fresh, empty weekly brief store.
type BriefFactory func(t *testing.T) BriefStore

// RunBriefContract runs the weekly brief and throttle cases against the backend: put with and without a
// revision, the read by committee and window, the index keys, and the throttle counter with its
// compare-and-swap. A lost swap is a service unavailable error, which the HTTP layer answers with a 503.
func RunBriefContract(t *testing.T, backend Backend, newStore BriefFactory) {
	t.Run(backend.Name, func(t *testing.T) { runBriefContract(t, newStore) })
}

func runBriefContract(t *testing.T, newStore BriefFactory) {
	ctx := context.Background()
	window := model.GroupWeeklyBrief{WindowStart: FixtureWindow}
	nextWeek := FixtureWindow.AddDate(0, 0, 7)

	t.Run("put and get round-trip every field", func(t *testing.T) {
		store := newStore(t)
		put, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", FixtureWindow))
		require.NoError(t, err)
		assert.NotZero(t, put.Revision)

		got, throttle, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Nil(t, throttle, "no throttle entry yet")
		assert.Equal(t, put.Revision, got.Revision)
		assert.Equal(t, normalizeBrief(put), normalizeBrief(got))
		assert.NotEqual(t, FixtureTime.Add(time.Hour), micro(got.UpdatedAt), "the store stamps the update time")
		assert.Equal(t, FixtureTime, micro(got.CreatedAt), "a creation time already set is kept")
	})

	t.Run("a brief with no sources or edit reads back empty", func(t *testing.T) {
		store := newStore(t)
		want := NewBrief("b-bare", "committee-1", FixtureWindow)
		want.SourceRefs, want.LastEditedAt, want.LastEditedBy = nil, nil, ""
		_, err := store.PutGroupWeeklyBrief(ctx, want)
		require.NoError(t, err)
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		assert.Equal(t, normalizeBrief(want), normalizeBrief(got))
		assert.Nil(t, got.LastEditedAt)
	})

	t.Run("put assigns a uid and the times that are missing", func(t *testing.T) {
		store := newStore(t)
		brief := NewBrief("", "committee-1", FixtureWindow)
		brief.CreatedAt = time.Time{}
		put, err := store.PutGroupWeeklyBrief(ctx, brief)
		require.NoError(t, err)
		assert.NotEmpty(t, put.UID)
		assert.False(t, put.CreatedAt.IsZero())
		assert.False(t, put.UpdatedAt.IsZero())
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		assert.Equal(t, put.UID, got.UID)
	})

	t.Run("a brief is found by committee and the UTC date of the window, and a miss is nil", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", FixtureWindow))
		require.NoError(t, err)

		got, throttle, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-2", window)
		require.NoError(t, err)
		assert.Nil(t, got)
		assert.Nil(t, throttle)
		got, _, err = store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: nextWeek})
		require.NoError(t, err)
		assert.Nil(t, got)

		// Any time on the same UTC day finds the brief; the offset is not the day.
		got, _, err = store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: FixtureWindow.Add(13 * time.Hour)})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "b-1", got.UID)
	})

	t.Run("put rejects a nil brief, and a missing committee or window", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBrief(ctx, nil)
		requireValidation(t, err)
		_, err = store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "", FixtureWindow))
		requireValidation(t, err)
		_, err = store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", time.Time{}))
		requireValidation(t, err)
	})

	t.Run("put with the revision it read replaces the brief, and a stale one is service unavailable", func(t *testing.T) {
		store := newStore(t)
		first, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", FixtureWindow))
		require.NoError(t, err)
		stale := first.Revision

		edit := NewBrief("b-1", "committee-1", FixtureWindow)
		edit.BriefText = "first writer"
		edit.Revision = stale
		second, err := store.PutGroupWeeklyBrief(ctx, edit)
		require.NoError(t, err)
		assert.NotEqual(t, stale, second.Revision)

		lost := NewBrief("b-1", "committee-1", FixtureWindow)
		lost.BriefText = "second writer"
		lost.Revision = stale
		_, err = store.PutGroupWeeklyBrief(ctx, lost)
		requireUnavailable(t, err)
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		assert.Equal(t, "first writer", got.BriefText)
		assert.Equal(t, second.Revision, got.Revision)
	})

	t.Run("put with a revision for a brief that does not exist is service unavailable", func(t *testing.T) {
		store := newStore(t)
		ghost := NewBrief("ghost", "committee-1", FixtureWindow)
		ghost.Revision = 1_000_000
		_, err := store.PutGroupWeeklyBrief(ctx, ghost)
		requireUnavailable(t, err)
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		assert.Nil(t, got, "a refused write stores nothing")
	})

	t.Run("a second brief with another uid for a window that has one leaves the window with one brief", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", FixtureWindow))
		require.NoError(t, err)
		other := NewBrief("b-2", "committee-1", FixtureWindow)
		other.BriefText = "other"
		// KV lets the second brief take over the window and PostgreSQL refuses it, which the unique index on
		// (committee, window) exists to do; the caller re-reads and reuses the uid it finds. Either way the
		// window reads one brief and has one index key.
		if _, err = store.PutGroupWeeklyBrief(ctx, other); err != nil {
			requireUnavailable(t, err)
		}
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Contains(t, []string{"b-1", "b-2"}, got.UID)
		keys, err := store.ListGroupWeeklyBriefIndexKeys(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"committee-1.20260104"}, keys)
	})

	t.Run("concurrent writers with the same revision: one wins", func(t *testing.T) {
		store := newStore(t)
		first, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", FixtureWindow))
		require.NoError(t, err)
		wins := race(t, func(writer int) error {
			edit := NewBrief("b-1", "committee-1", FixtureWindow)
			edit.BriefText = fmt.Sprintf("writer %d", writer)
			edit.Revision = first.Revision
			_, err := store.PutGroupWeeklyBrief(ctx, edit)
			return err
		}, requireUnavailable)
		assert.Equal(t, 1, wins)
	})

	t.Run("ListGroupWeeklyBriefIndexKeys lists committee and window", func(t *testing.T) {
		store := newStore(t)
		keys, err := store.ListGroupWeeklyBriefIndexKeys(ctx)
		require.NoError(t, err)
		assert.Empty(t, keys)

		for _, b := range []*model.GroupWeeklyBrief{
			NewBrief("b-1", "committee-1", FixtureWindow), NewBrief("b-2", "committee-1", nextWeek), NewBrief("b-3", "committee-2", FixtureWindow),
		} {
			_, err := store.PutGroupWeeklyBrief(ctx, b)
			require.NoError(t, err)
		}
		keys, err = store.ListGroupWeeklyBriefIndexKeys(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"committee-1.20260104", "committee-1.20260111", "committee-2.20260104"}, keys)
	})

	t.Run("a committee uid with a separator becomes dot-delimited in the index key", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee:one/two three", FixtureWindow))
		require.NoError(t, err)
		keys, err := store.ListGroupWeeklyBriefIndexKeys(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"committee.one.two.three.20260104"}, keys)
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee:one/two three", window)
		require.NoError(t, err)
		require.NotNil(t, got)
	})

	t.Run("throttle: a miss is nil, a create round-trips, and the read by any time on the UTC day finds it", func(t *testing.T) {
		store := newStore(t)
		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", FixtureWindow)
		require.NoError(t, err)
		assert.Nil(t, got)

		put, err := store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("committee-1", FixtureWindow))
		require.NoError(t, err)
		assert.NotZero(t, put.Revision)

		for _, at := range []time.Time{FixtureWindow, FixtureWindow.Add(13 * time.Hour)} {
			got, err = store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", at)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, put.Revision, got.Revision)
			assert.Equal(t, normalizeThrottle(put), normalizeThrottle(got))
		}
		got, err = store.GetGroupWeeklyBriefThrottle(ctx, "committee-2", FixtureWindow)
		require.NoError(t, err)
		assert.Nil(t, got)
		got, err = store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", nextWeek)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("the read of a brief carries its throttle entry as JSON", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBrief(ctx, NewBrief("b-1", "committee-1", FixtureWindow))
		require.NoError(t, err)
		want, err := store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("committee-1", FixtureWindow))
		require.NoError(t, err)

		_, raw, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", window)
		require.NoError(t, err)
		require.NotEmpty(t, raw)
		var got model.GroupWeeklyBriefThrottle
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Equal(t, normalizeThrottle(want), normalizeThrottle(&got))
	})

	t.Run("throttle put rejects a nil entry, and a missing committee or window", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBriefThrottle(ctx, nil)
		requireValidation(t, err)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("", FixtureWindow))
		requireValidation(t, err)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("committee-1", time.Time{}))
		requireValidation(t, err)
	})

	t.Run("throttle create twice is service unavailable, and changes nothing", func(t *testing.T) {
		store := newStore(t)
		_, err := store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("committee-1", FixtureWindow))
		require.NoError(t, err)

		again := NewThrottle("committee-1", FixtureWindow)
		again.GeneratesUsed = 5
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, again)
		requireUnavailable(t, err)
		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", FixtureWindow)
		require.NoError(t, err)
		assert.Equal(t, 1, got.GeneratesUsed)
	})

	t.Run("throttle update with the revision it read counts, and a stale one is service unavailable", func(t *testing.T) {
		store := newStore(t)
		first, err := store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("committee-1", FixtureWindow))
		require.NoError(t, err)
		stale := first.Revision

		bump := NewThrottle("committee-1", FixtureWindow)
		bump.GeneratesUsed, bump.Revision = 2, stale
		second, err := store.PutGroupWeeklyBriefThrottle(ctx, bump)
		require.NoError(t, err)
		assert.NotEqual(t, stale, second.Revision)

		lost := NewThrottle("committee-1", FixtureWindow)
		lost.GeneratesUsed, lost.Revision = 3, stale
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, lost)
		requireUnavailable(t, err)
		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", FixtureWindow)
		require.NoError(t, err)
		assert.Equal(t, 2, got.GeneratesUsed)
		assert.Equal(t, second.Revision, got.Revision)
	})

	t.Run("throttle update of an entry that does not exist is service unavailable", func(t *testing.T) {
		store := newStore(t)
		ghost := NewThrottle("committee-1", FixtureWindow)
		ghost.Revision = 1_000_000
		_, err := store.PutGroupWeeklyBriefThrottle(ctx, ghost)
		requireUnavailable(t, err)
		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", FixtureWindow)
		require.NoError(t, err)
		assert.Nil(t, got, "a refused write stores nothing")
	})

	t.Run("concurrent throttle creates: one wins, so the cap cannot be bypassed", func(t *testing.T) {
		store := newStore(t)
		wins := race(t, func(writer int) error {
			entry := NewThrottle("committee-1", FixtureWindow)
			entry.GeneratesUsed = writer + 1
			_, err := store.PutGroupWeeklyBriefThrottle(ctx, entry)
			return err
		}, requireUnavailable)
		assert.Equal(t, 1, wins)
	})

	t.Run("concurrent throttle updates with the same revision: one wins", func(t *testing.T) {
		store := newStore(t)
		first, err := store.PutGroupWeeklyBriefThrottle(ctx, NewThrottle("committee-1", FixtureWindow))
		require.NoError(t, err)
		wins := race(t, func(writer int) error {
			entry := NewThrottle("committee-1", FixtureWindow)
			entry.GeneratesUsed, entry.Revision = writer+10, first.Revision
			_, err := store.PutGroupWeeklyBriefThrottle(ctx, entry)
			return err
		}, requireUnavailable)
		assert.Equal(t, 1, wins)
	})
}
