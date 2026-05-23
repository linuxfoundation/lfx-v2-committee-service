// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// MeetingActivity is a normalised view of a past-meeting record consumed by the
// weekly-brief generator. The MeetingSource port is responsible for translating
// the external query-service contract into this shape.
type MeetingActivity struct {
	// UID is the meeting UID (used for citation links).
	UID string
	// Title is the meeting title.
	Title string
	// StartTime is the start of the meeting (UTC).
	StartTime time.Time
	// Summary is a short natural-language summary or transcript excerpt.
	Summary string
	// Private indicates whether the meeting record is restricted-visibility.
	// When true, the generator marks PrivateSourcePresent on the brief.
	Private bool
	// URL is a deep-link to the meeting record, if available.
	URL string
}

// MeetingSource fetches past-meeting activity for a committee in a window.
// Live implementations make an M2M-authenticated call to the query-service;
// test/mock implementations return canned data.
//
// Implementations MUST NOT propagate the caller's bearer token — meeting access
// is brokered by service identity, not by user identity.
type MeetingSource interface {
	ListMeetingsForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]MeetingActivity, error)
}

// MailingListActivity is the per-thread view consumed by the brief generator.
type MailingListActivity struct {
	ThreadID string
	Subject  string
	URL      string
	Excerpt  string
	Private  bool
}

// MailingListSource returns mailing-list threads active in the window.
//
// MVP: stub returns empty until the upstream contract is defined.
type MailingListSource interface {
	ListMailingListActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]MailingListActivity, error)
}

// VoteActivity is one vote/poll record relevant to the window.
type VoteActivity struct {
	VoteID  string
	Subject string
	URL     string
	Outcome string
	Private bool
}

// VoteSource returns vote/poll activity in the window.
//
// MVP: stub returns empty until the upstream contract is defined.
type VoteSource interface {
	ListVoteActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]VoteActivity, error)
}

// WeeklyMemberActivity bundles the joined / updated member sets for one
// committee in one window. "Joined" = created during this week; "Updated" =
// updated this week but NOT created this week (avoids double-counting joins).
type WeeklyMemberActivity struct {
	Joined  []*model.CommitteeMember
	Updated []*model.CommitteeMember
}

// CommitteeWeeklyMemberReader returns the member-activity windows the
// generator weaves into the brief. The live implementation lists all members
// for the committee and partitions them by created_at / updated_at; the mock
// implementation returns whatever it is configured with.
type CommitteeWeeklyMemberReader interface {
	ListMemberActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) (WeeklyMemberActivity, error)
}

// GroupWeeklyBriefWriter persists weekly briefs and their throttle counters.
// The interface is intentionally narrow so test doubles can implement it
// without dragging in the rest of the storage adapter.
type GroupWeeklyBriefWriter interface {
	// PutGroupWeeklyBrief writes the brief and refreshes the {committee_uid}.{window}
	// → brief_uid index entry. Returns the new revision of the brief.
	PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error)

	// GetGroupWeeklyBriefThrottle returns the throttle entry for the given
	// (committee, window). Misses return (nil, nil) — NOT an error.
	GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error)

	// PutGroupWeeklyBriefThrottle writes or updates the throttle entry using
	// compare-and-swap on the carried Revision. Pass Revision == 0 for create.
	// Concurrent callers race here; the loser receives a conflict error and is
	// expected to retry by re-reading the throttle.
	PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error)
}
