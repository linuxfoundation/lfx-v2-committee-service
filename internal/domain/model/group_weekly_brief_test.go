// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupWeeklyBriefState_Validate(t *testing.T) {
	valid := []GroupWeeklyBriefState{
		GroupWeeklyBriefStateEmpty,
		GroupWeeklyBriefStateGenerating,
		GroupWeeklyBriefStateGenerated,
		GroupWeeklyBriefStateEdited,
		GroupWeeklyBriefStateApproved,
		GroupWeeklyBriefStateError,
	}
	for _, s := range valid {
		require.NoError(t, s.Validate(), "expected %q to be valid", s)
	}
	require.Error(t, GroupWeeklyBriefState("nonsense").Validate())
	require.Error(t, GroupWeeklyBriefState("").Validate())
}

func TestWeeklyWindow(t *testing.T) {
	// Reference dates chosen in May 2026 — calendar:
	// Sun 2026-05-10, Mon 2026-05-11, …, Sat 2026-05-16,
	// Sun 2026-05-17, Wed 2026-05-20.
	mustParse := func(s string) time.Time {
		t.Helper()
		got, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return got.UTC()
	}

	cases := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "mid-week Wednesday uses prior Sun→Sat",
			now:       mustParse("2026-05-20T15:04:05Z"),
			wantStart: mustParse("2026-05-10T00:00:00Z"),
			wantEnd:   mustParse("2026-05-16T23:59:59.999999999Z"),
		},
		{
			name:      "Monday uses prior Sun→Sat",
			now:       mustParse("2026-05-18T08:00:00Z"),
			wantStart: mustParse("2026-05-10T00:00:00Z"),
			wantEnd:   mustParse("2026-05-16T23:59:59.999999999Z"),
		},
		{
			name:      "Saturday uses Sunday→Saturday (current week ending today)",
			now:       mustParse("2026-05-16T12:00:00Z"),
			wantStart: mustParse("2026-05-10T00:00:00Z"),
			wantEnd:   mustParse("2026-05-16T23:59:59.999999999Z"),
		},
		{
			name:      "Sunday must use prior week (not current week)",
			now:       mustParse("2026-05-17T01:00:00Z"),
			wantStart: mustParse("2026-05-10T00:00:00Z"),
			wantEnd:   mustParse("2026-05-16T23:59:59.999999999Z"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd := WeeklyWindow(tc.now)
			assert.True(t, gotStart.Equal(tc.wantStart),
				"start: got %s want %s", gotStart, tc.wantStart)
			assert.True(t, gotEnd.Equal(tc.wantEnd),
				"end: got %s want %s", gotEnd, tc.wantEnd)
			// Window must always be 7 days minus 1ns (Sun 00:00:00 → Sat 23:59:59.999999999).
			assert.Equal(t, time.Sunday, gotStart.Weekday())
			assert.Equal(t, time.Saturday, gotEnd.Weekday())
		})
	}
}

func TestWindowDateKey(t *testing.T) {
	got := WindowDateKey(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, "20260512"[:4]+"05"+"10", got)
	assert.Equal(t, "20260510", got)
}

func TestGroupWeeklyBrief_Validate(t *testing.T) {
	now := time.Now().UTC()
	start, end := WeeklyWindow(now)

	b := &GroupWeeklyBrief{
		UID:          "brief-1",
		CommitteeUID: "c-1",
		WindowStart:  start,
		WindowEnd:    end,
		State:        GroupWeeklyBriefStateGenerated,
	}
	require.NoError(t, b.Validate())

	b.State = "bogus"
	require.Error(t, b.Validate())

	b.State = GroupWeeklyBriefStateGenerated
	b.UID = ""
	require.Error(t, b.Validate())
}
