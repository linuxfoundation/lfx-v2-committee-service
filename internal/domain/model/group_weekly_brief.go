// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"fmt"
	"time"
)

// GroupWeeklyBriefState represents the lifecycle state of a working-group weekly brief.
type GroupWeeklyBriefState string

// GroupWeeklyBriefState enum values.
const (
	GroupWeeklyBriefStateEmpty      GroupWeeklyBriefState = "empty"
	GroupWeeklyBriefStateGenerating GroupWeeklyBriefState = "generating"
	GroupWeeklyBriefStateGenerated  GroupWeeklyBriefState = "generated"
	GroupWeeklyBriefStateEdited     GroupWeeklyBriefState = "edited"
	GroupWeeklyBriefStateApproved   GroupWeeklyBriefState = "approved"
	GroupWeeklyBriefStateError      GroupWeeklyBriefState = "error"
)

// IsValid reports whether s is a recognised GroupWeeklyBriefState value.
func (s GroupWeeklyBriefState) IsValid() bool {
	switch s {
	case GroupWeeklyBriefStateEmpty,
		GroupWeeklyBriefStateGenerating,
		GroupWeeklyBriefStateGenerated,
		GroupWeeklyBriefStateEdited,
		GroupWeeklyBriefStateApproved,
		GroupWeeklyBriefStateError:
		return true
	}
	return false
}

// Validate returns an error if s is not a recognised GroupWeeklyBriefState.
func (s GroupWeeklyBriefState) Validate() error {
	if !s.IsValid() {
		return fmt.Errorf("invalid GroupWeeklyBriefState: %q", string(s))
	}
	return nil
}

// String returns the underlying string value.
func (s GroupWeeklyBriefState) String() string { return string(s) }

// SourceRef is a reference to a single source document considered by the
// weekly-brief generator. Defined here for Phase 1; Phase 2 may reconcile this
// with the fake-AI source adapter on the feat/wg-weekly-brief-fake-ai-source
// branch — keep the shape minimal so the merge is mechanical.
type SourceRef struct {
	// Kind is the source category (e.g. "meeting", "mailing-list", "doc").
	Kind string `json:"kind"`
	// ID is the source-system identifier (URL or UID).
	ID string `json:"id"`
	// Title is a short human label for the source.
	Title string `json:"title,omitempty"`
	// Excerpt is the snippet the generator consumed, if any.
	Excerpt string `json:"excerpt,omitempty"`
}

// GroupWeeklyBrief is a working-group weekly brief for a single (committee,
// window) pair. One brief exists per committee per UTC Sun→Sat window.
//
// Field set is derived from the indexer contract (see
// feat/wg-weekly-brief-indexer-contract); Revision is internal-only and used
// for optimistic concurrency on KV updates.
type GroupWeeklyBrief struct {
	UID                  string                `json:"uid"`
	CommitteeUID         string                `json:"committee_uid"`
	WindowStart          time.Time             `json:"window_start"`
	WindowEnd            time.Time             `json:"window_end"`
	State                GroupWeeklyBriefState `json:"state"`
	BriefText            string                `json:"brief_text,omitempty"`
	SourceRefs           []SourceRef           `json:"source_refs,omitempty"`
	PromptVersion        string                `json:"prompt_version,omitempty"`
	Model                string                `json:"model,omitempty"`
	RegenerationCount    int                   `json:"regeneration_count"`
	PrivateSourcePresent bool                  `json:"private_source_present"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
	// Revision is the NATS KV revision used for optimistic concurrency.
	// It is not part of the indexer contract and is not surfaced externally.
	Revision uint64 `json:"-"`
}

// Validate performs lightweight structural validation on the brief.
func (b *GroupWeeklyBrief) Validate() error {
	if b == nil {
		return fmt.Errorf("brief is nil")
	}
	if b.UID == "" {
		return fmt.Errorf("uid is required")
	}
	if b.CommitteeUID == "" {
		return fmt.Errorf("committee_uid is required")
	}
	if b.WindowStart.IsZero() || b.WindowEnd.IsZero() {
		return fmt.Errorf("window_start and window_end are required")
	}
	if !b.WindowEnd.After(b.WindowStart) {
		return fmt.Errorf("window_end must be after window_start")
	}
	return b.State.Validate()
}

// GroupWeeklyBriefThrottle records per-window regeneration throttling state.
// Phase 1 does not write or read this struct; it is defined so Phase 2 can
// land its throttle logic without touching this file's KV-bucket wiring.
type GroupWeeklyBriefThrottle struct {
	CommitteeUID string    `json:"committee_uid"`
	WindowStart  time.Time `json:"window_start"`
	WindowEnd    time.Time `json:"window_end"`
	// Count is the number of regeneration attempts in this window.
	Count int `json:"count"`
	// LastAttemptAt is the timestamp of the most recent attempt.
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
}

// WeeklyWindow returns the most recently completed UTC Sun 00:00:00 →
// Sat 23:59:59.999999999 window (inclusive end, nanosecond precision) for the
// given reference time. If now is a Sunday a new week has just started, so the
// returned window is the prior Sun→Sat that just ended.
//
// Rule: find the last Saturday on/before today (if today is Sunday, the last
// Saturday is yesterday); the window starts six days before that Saturday.
// The end is computed as start + 7 days - 1ns, giving an inclusive Saturday
// 23:59:59.999999999 so range filters work without an off-by-one at the
// second-precision boundary.
func WeeklyWindow(now time.Time) (start, end time.Time) {
	nUTC := now.UTC()
	today := time.Date(nUTC.Year(), nUTC.Month(), nUTC.Day(), 0, 0, 0, 0, time.UTC)

	// Days back from today to the most recent Saturday.
	// Mon→2, Tue→3, …, Sat→0, Sun→1.
	wd := int(today.Weekday())
	var daysToSat int
	if wd == int(time.Saturday) {
		daysToSat = 0
	} else {
		// time.Weekday: Sunday=0, Monday=1, …, Saturday=6.
		// Distance back to Saturday is (wd + 1) mod 7.
		daysToSat = (wd + 1) % 7
	}
	sat := today.AddDate(0, 0, -daysToSat)
	start = sat.AddDate(0, 0, -6)                                      // Sunday 00:00:00 UTC
	end = start.AddDate(0, 0, 6).Add(24*time.Hour - 1*time.Nanosecond) // Saturday 23:59:59.999999999 UTC
	return start, end
}

// WindowDateKey formats the window start as YYYYMMDD for use in KV keys.
func WindowDateKey(windowStart time.Time) string {
	return windowStart.UTC().Format("20060102")
}
