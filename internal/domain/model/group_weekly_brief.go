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
	if b.RegenerationCount < 0 {
		return fmt.Errorf("regeneration_count must be non-negative")
	}
	return b.State.Validate()
}

// GroupWeeklyBriefThrottle records per-committee/per-week throttle state.
// GeneratesUsed and RegenerationsUsed are tracked separately so the generate
// (fresh) and regenerate (force or follow-up) paths can have distinct limits
// enforced against the same window key.
type GroupWeeklyBriefThrottle struct {
	CommitteeUID string    `json:"committee_uid"`
	WindowStart  time.Time `json:"window_start"`
	WindowEnd    time.Time `json:"window_end"`
	// GeneratesUsed is the number of fresh generations consumed in this window.
	GeneratesUsed int `json:"generates_used"`
	// RegenerationsUsed is the number of regenerations consumed in this window.
	RegenerationsUsed int `json:"regenerations_used"`
	// WindowResetsAt is the timestamp at which the window resets.
	WindowResetsAt time.Time `json:"window_resets_at"`
	// Revision is the NATS KV revision used for compare-and-swap updates.
	// It is not persisted as JSON.
	Revision uint64 `json:"-"`
}

// Limits enforced per (committee, week):
//   - GenerateLimit:    fresh generations allowed before 429.
//   - RegenerationLimit: regenerations allowed before 429.
//
// Per-committee/per-week (NOT per-user) so two writers cannot bypass the cap
// by alternating calls.
const (
	GroupWeeklyBriefGenerateLimit     = 2
	GroupWeeklyBriefRegenerationLimit = 3
)

// NextWindowReset returns the next UTC Sunday 00:00:00 relative to now. This is
// the "throttle window resets at" timestamp surfaced in 429 responses.
func NextWindowReset(now time.Time) time.Time {
	nUTC := now.UTC()
	today := time.Date(nUTC.Year(), nUTC.Month(), nUTC.Day(), 0, 0, 0, 0, time.UTC)
	// Days forward to the next Sunday. If today is Sunday, jump 7 days.
	wd := int(today.Weekday()) // Sunday=0..Saturday=6
	daysToNextSun := (7 - wd) % 7
	if daysToNextSun == 0 {
		daysToNextSun = 7
	}
	return today.AddDate(0, 0, daysToNextSun)
}

// WeeklyWindow returns a UTC Sun 00:00:00 → Sat 23:59:59.999999999 window
// (inclusive end, nanosecond precision) for the given reference time, anchored
// on the most recent Saturday on or before today.
//
// For Sunday through Friday this is the previous, already-completed Sun→Sat
// week. If today *is* Saturday, the anchor is today, so the returned window is
// the current week — which is not yet complete: window_end is later today, in
// the future relative to now. Callers that require a fully-completed window
// must account for the Saturday case.
//
// Rule: find the last Saturday on/before today; the window starts six days
// before that Saturday. The end is computed as start + 7 days - 1ns, giving an
// inclusive Saturday 23:59:59.999999999 so range filters work without an
// off-by-one at the second-precision boundary.
func WeeklyWindow(now time.Time) (start, end time.Time) {
	nUTC := now.UTC()
	today := time.Date(nUTC.Year(), nUTC.Month(), nUTC.Day(), 0, 0, 0, 0, time.UTC)

	// Days back from today to the most recent Saturday (Sat→0, Sun→1, …, Fri→6).
	// time.Weekday is Sunday=0, Monday=1, …, Saturday=6.
	wd := int(today.Weekday())
	daysToSat := (wd - int(time.Saturday) + 7) % 7
	sat := today.AddDate(0, 0, -daysToSat)
	start = sat.AddDate(0, 0, -6)                      // Sunday 00:00:00 UTC
	end = start.AddDate(0, 0, 7).Add(-time.Nanosecond) // Saturday 23:59:59.999999999 UTC
	return start, end
}

// WindowDateKey formats the window start as YYYYMMDD for use in KV keys.
func WindowDateKey(windowStart time.Time) string {
	return windowStart.UTC().Format("20060102")
}
