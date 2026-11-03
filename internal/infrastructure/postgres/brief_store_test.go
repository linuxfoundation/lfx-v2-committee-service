// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The window end the service computes is the last nanosecond of the week; the tests use the last
// microsecond, the precision the column keeps (see TestStore_WeeklyWindowEndPrecision).

// testWindow is the Sunday that starts the weekly window of 2026-01-04, in UTC.
var testWindow = time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)

// testBrief returns a brief with every field set, for the window starting at start.
func testBrief(uid, committeeUID string, start time.Time) *model.GroupWeeklyBrief {
	edited := start.Add(48 * time.Hour)
	return &model.GroupWeeklyBrief{
		UID: uid, CommitteeUID: committeeUID, WindowStart: start, WindowEnd: start.AddDate(0, 0, 7).Add(-time.Microsecond),
		State: model.GroupWeeklyBriefStateEdited, ErrorReason: "none", BriefText: "First paragraph.\n\nSecond paragraph.",
		SourceRefs:    []model.SourceRef{{Kind: "meeting", ID: "m-1", Title: "Weekly sync", Excerpt: "Notes"}, {Kind: "survey", ID: "s-1"}},
		PromptVersion: "v1", Model: "model-1", RegenerationCount: 2, PrivateSourcePresent: true,
		CreatedAt: start.Add(time.Hour), UpdatedAt: start.Add(2 * time.Hour), LastEditedAt: &edited, LastEditedBy: "first-last",
	}
}

func testThrottle(committeeUID string, start time.Time) *model.GroupWeeklyBriefThrottle {
	return &model.GroupWeeklyBriefThrottle{
		CommitteeUID: committeeUID, WindowStart: start, WindowEnd: start.AddDate(0, 0, 7).Add(-time.Microsecond),
		GeneratesUsed: 1, RegenerationsUsed: 2, WindowResetsAt: start.AddDate(0, 0, 7),
	}
}

// requireUnavailable checks the error KV reports for a lost compare-and-swap, which the caller retries.
func requireUnavailable(t *testing.T, err error) {
	t.Helper()
	var unavailable errs.ServiceUnavailable
	require.ErrorAs(t, err, &unavailable)
}

func TestStore_PutGroupWeeklyBrief(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	t.Run("creates the brief and round-trips every field", func(t *testing.T) {
		want := testBrief("b-1", "committee-1", testWindow)
		put, err := store.PutGroupWeeklyBrief(ctx, want)
		require.NoError(t, err)
		assert.NotZero(t, put.Revision)

		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, put.Revision, got.Revision)
	})

	t.Run("fills in the uid and times, and an unedited brief with no sources reads back empty", func(t *testing.T) {
		brief := &model.GroupWeeklyBrief{CommitteeUID: "committee-2", WindowStart: testWindow, WindowEnd: testWindow.AddDate(0, 0, 7), State: model.GroupWeeklyBriefStateEmpty}
		put, err := store.PutGroupWeeklyBrief(ctx, brief)
		require.NoError(t, err)
		assert.NotEmpty(t, put.UID)
		assert.False(t, put.CreatedAt.IsZero())
		assert.False(t, put.UpdatedAt.IsZero())

		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-2", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		assert.Nil(t, got.SourceRefs)
		assert.Nil(t, got.LastEditedAt)
	})

	t.Run("a revision updates the brief and replaces the revision", func(t *testing.T) {
		brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		oldRevision := brief.Revision
		brief.State = model.GroupWeeklyBriefStateApproved
		put, err := store.PutGroupWeeklyBrief(ctx, brief)
		require.NoError(t, err)
		assert.NotEqual(t, oldRevision, put.Revision)

		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		assert.Equal(t, model.GroupWeeklyBriefStateApproved, got.State)
	})

	t.Run("a stale or missing revision is a retryable conflict and changes nothing", func(t *testing.T) {
		brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		stale := *brief
		stale.Revision--
		stale.State = model.GroupWeeklyBriefStateError
		_, err = store.PutGroupWeeklyBrief(ctx, &stale)
		requireUnavailable(t, err)

		missing := *brief
		missing.UID = "no-such-brief"
		_, err = store.PutGroupWeeklyBrief(ctx, &missing)
		requireUnavailable(t, err)

		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		assert.Equal(t, model.GroupWeeklyBriefStateApproved, got.State)
	})

	t.Run("without a revision a brief with the same uid is replaced", func(t *testing.T) {
		replacement := testBrief("b-1", "committee-1", testWindow)
		replacement.BriefText = "Replaced."
		_, err := store.PutGroupWeeklyBrief(ctx, replacement)
		require.NoError(t, err)
		got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		assert.Equal(t, "Replaced.", got.BriefText)
	})

	t.Run("a window holds one brief: a second uid is retryable, other windows and committees are free", func(t *testing.T) {
		_, err := store.PutGroupWeeklyBrief(ctx, testBrief("b-dup", "committee-1", testWindow.Add(5*time.Hour)))
		requireUnavailable(t, err)
		_, err = store.PutGroupWeeklyBrief(ctx, testBrief("b-next", "committee-1", testWindow.AddDate(0, 0, 7)))
		require.NoError(t, err)
		_, err = store.PutGroupWeeklyBrief(ctx, testBrief("b-other", "committee-3", testWindow))
		require.NoError(t, err)
	})

	t.Run("a nil brief or missing committee and window are invalid", func(t *testing.T) {
		var validation errs.Validation
		_, err := store.PutGroupWeeklyBrief(ctx, nil)
		require.ErrorAs(t, err, &validation)
		_, err = store.PutGroupWeeklyBrief(ctx, testBrief("b-x", "", testWindow))
		require.ErrorAs(t, err, &validation)
		_, err = store.PutGroupWeeklyBrief(ctx, testBrief("b-x", "committee-1", time.Time{}))
		require.ErrorAs(t, err, &validation)
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		wins := raceWritersLosing(t, func() error {
			brief := *original
			_, err := store.PutGroupWeeklyBrief(ctx, &brief)
			return err
		}, func(t *testing.T, err error) { requireUnavailable(t, err) })
		assert.Equal(t, 1, wins)
	})
}

func TestStore_GetGroupWeeklyBriefForWindow(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_, err := store.PutGroupWeeklyBrief(ctx, testBrief("b-1", "committee-1", testWindow))
	require.NoError(t, err)

	t.Run("a miss is not an error", func(t *testing.T) {
		for name, window := range map[string]struct {
			committee string
			start     time.Time
		}{
			"another committee": {"committee-2", testWindow},
			"another week":      {"committee-1", testWindow.AddDate(0, 0, 7)},
		} {
			brief, throttle, err := store.GetGroupWeeklyBriefForWindow(ctx, window.committee, model.GroupWeeklyBrief{WindowStart: window.start})
			require.NoError(t, err, name)
			assert.Nil(t, brief, name)
			assert.Nil(t, throttle, name)
		}
	})

	t.Run("the window is the UTC day of its start, whatever the zone or time of day", func(t *testing.T) {
		sameDay := time.Date(2026, 1, 4, 23, 30, 0, 0, time.UTC)
		brief, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: sameDay})
		require.NoError(t, err)
		require.NotNil(t, brief)

		// 2026-01-04T22:00-05:00 is 2026-01-05 in UTC: another day.
		behind := time.FixedZone("UTC-5", -5*3600)
		brief, _, err = store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: time.Date(2026, 1, 4, 22, 0, 0, 0, behind)})
		require.NoError(t, err)
		assert.Nil(t, brief)
	})

	t.Run("returns the throttle JSON only when the window has one", func(t *testing.T) {
		_, throttleJSON, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		assert.Nil(t, throttleJSON)

		want := testThrottle("committee-1", testWindow)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, want)
		require.NoError(t, err)
		_, throttleJSON, err = store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
		require.NoError(t, err)
		var got model.GroupWeeklyBriefThrottle
		require.NoError(t, json.Unmarshal(throttleJSON, &got))
		got.Revision = want.Revision // the revision is not part of the JSON
		assert.Equal(t, *want, got)
	})
}

func TestStore_ListGroupWeeklyBriefIndexKeys(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	keys, err := store.ListGroupWeeklyBriefIndexKeys(ctx)
	require.NoError(t, err)
	assert.Nil(t, keys)

	for _, b := range []*model.GroupWeeklyBrief{
		testBrief("b-1", "committee-1", testWindow),
		testBrief("b-2", "committee-1", testWindow.AddDate(0, 0, 7)),
		testBrief("b-3", "committee/with: odd chars", testWindow),
	} {
		_, err := store.PutGroupWeeklyBrief(ctx, b)
		require.NoError(t, err)
	}
	keys, err = store.ListGroupWeeklyBriefIndexKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"committee-1.20260104", "committee-1.20260111", "committee.with..odd.chars.20260104"}, keys)
}

func TestStore_GroupWeeklyBriefThrottle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	t.Run("a miss is nil, not an error", func(t *testing.T) {
		throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
		require.NoError(t, err)
		assert.Nil(t, throttle)
	})

	t.Run("a zero revision creates the entry and round-trips every field", func(t *testing.T) {
		want := testThrottle("committee-1", testWindow)
		put, err := store.PutGroupWeeklyBriefThrottle(ctx, want)
		require.NoError(t, err)
		assert.NotZero(t, put.Revision)

		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow.Add(3*time.Hour))
		require.NoError(t, err, "the window is the UTC day")
		assert.Equal(t, want, got)
	})

	t.Run("creating the same window again is retryable", func(t *testing.T) {
		_, err := store.PutGroupWeeklyBriefThrottle(ctx, testThrottle("committee-1", testWindow.Add(time.Hour)))
		requireUnavailable(t, err)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, testThrottle("committee-1", testWindow.AddDate(0, 0, 7)))
		require.NoError(t, err)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, testThrottle("committee-2", testWindow))
		require.NoError(t, err)
	})

	t.Run("a current revision updates the entry and replaces the revision", func(t *testing.T) {
		throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
		require.NoError(t, err)
		oldRevision := throttle.Revision
		throttle.GeneratesUsed = 2
		put, err := store.PutGroupWeeklyBriefThrottle(ctx, throttle)
		require.NoError(t, err)
		assert.NotEqual(t, oldRevision, put.Revision)

		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
		require.NoError(t, err)
		assert.Equal(t, 2, got.GeneratesUsed)
	})

	t.Run("a stale or missing revision is retryable and changes nothing", func(t *testing.T) {
		throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
		require.NoError(t, err)
		stale := *throttle
		stale.Revision--
		stale.GeneratesUsed = 99
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, &stale)
		requireUnavailable(t, err)

		missing := *throttle
		missing.CommitteeUID = "no-such-committee"
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, &missing)
		requireUnavailable(t, err)

		got, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
		require.NoError(t, err)
		assert.Equal(t, 2, got.GeneratesUsed)
	})

	t.Run("a nil entry or missing committee and window are invalid", func(t *testing.T) {
		var validation errs.Validation
		_, err := store.PutGroupWeeklyBriefThrottle(ctx, nil)
		require.ErrorAs(t, err, &validation)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, testThrottle("", testWindow))
		require.ErrorAs(t, err, &validation)
		_, err = store.PutGroupWeeklyBriefThrottle(ctx, testThrottle("committee-1", time.Time{}))
		require.ErrorAs(t, err, &validation)
	})

	t.Run("two writers with the same revision: one wins", func(t *testing.T) {
		original, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
		require.NoError(t, err)
		wins := raceWritersLosing(t, func() error {
			throttle := *original
			_, err := store.PutGroupWeeklyBriefThrottle(ctx, &throttle)
			return err
		}, func(t *testing.T, err error) { requireUnavailable(t, err) })
		assert.Equal(t, 1, wins)
	})
}

// WeeklyWindow ends at 23:59:59.999999999 UTC; TIMESTAMPTZ keeps microseconds, so the end of a
// window reads back as 23:59:59.999999. The window is still the same week.
func TestStore_WeeklyWindowEndPrecision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	start, end := model.WeeklyWindow(testWindow.AddDate(0, 0, 3))
	brief := testBrief("b-1", "committee-1", start)
	brief.WindowEnd = end
	_, err := store.PutGroupWeeklyBrief(ctx, brief)
	require.NoError(t, err)

	got, _, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: start})
	require.NoError(t, err)
	assert.Equal(t, end.Truncate(time.Microsecond), got.WindowEnd)
	assert.Equal(t, 999999999, end.Nanosecond())
	assert.Equal(t, 999999000, got.WindowEnd.Nanosecond())
}

// The window is the UTC day of its start whatever time zone the connection uses: here midnight UTC is
// still the previous day in the session's zone.
func TestStore_WindowIsUTCInAnySessionZone(t *testing.T) {
	ctx := context.Background()
	store := newTestStoreWith(t, map[string]string{"timezone": "America/Los_Angeles"})

	_, err := store.PutGroupWeeklyBrief(ctx, testBrief("b-1", "committee-1", testWindow))
	require.NoError(t, err)
	_, err = store.PutGroupWeeklyBriefThrottle(ctx, testThrottle("committee-1", testWindow))
	require.NoError(t, err)

	brief, throttleJSON, err := store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow})
	require.NoError(t, err)
	require.NotNil(t, brief)
	assert.NotNil(t, throttleJSON)
	throttle, err := store.GetGroupWeeklyBriefThrottle(ctx, "committee-1", testWindow)
	require.NoError(t, err)
	assert.NotNil(t, throttle)

	keys, err := store.ListGroupWeeklyBriefIndexKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"committee-1.20260104"}, keys)

	// The day before, in the session's zone, is not the same window.
	brief, _, err = store.GetGroupWeeklyBriefForWindow(ctx, "committee-1", model.GroupWeeklyBrief{WindowStart: testWindow.AddDate(0, 0, -1)})
	require.NoError(t, err)
	assert.Nil(t, brief)
}
