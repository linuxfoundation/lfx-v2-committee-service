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

// MeetingAISummaryActivity is a normalised view of an approved AI-generated
// meeting summary consumed by the weekly-brief generator.
type MeetingAISummaryActivity struct {
	// MeetingAndOccurrenceID is the combined meeting+occurrence identifier used
	// to link back to the parent past meeting record.
	MeetingAndOccurrenceID string
	// Title is the meeting topic or AI-generated summary title.
	Title string
	// StartTime is the start of the summarised meeting session (UTC).
	StartTime time.Time
	// Content is the consolidated markdown content of the AI summary.
	// It is sanitized by the generator before being passed to the AI adapter.
	Content string
	// Private is true when the summary's access level is not "public".
	// When true, the generator marks PrivateSourcePresent on the brief.
	Private bool
}

// MeetingAISummarySource fetches approved AI-generated meeting summaries for a
// committee in a window. Live implementations query the query-service for
// v1_past_meeting_summary resources tagged by committee_uid; only records where
// approved == true are returned. Test/mock implementations return canned data.
//
// Implementations MUST NOT propagate the caller's bearer token — access is
// brokered by service identity.
type MeetingAISummarySource interface {
	ListAISummariesForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]MeetingAISummaryActivity, error)
}

// MailingListActivity is the per-thread view consumed by the brief generator.
type MailingListActivity struct {
	ThreadID string
	Subject  string
	URL      string
	Excerpt  string
	Private  bool
	// SubscriberCount is the number of list subscribers at index time.
	// Zero means the field was absent or unavailable from the source.
	SubscriberCount int
}

// MailingListSource returns mailing-list threads active in the window.
//
// A live M2M-backed implementation (m2m.MailingListSource) calls the
// query-service for the configured resource type (overridable via
// QUERY_MAILING_LIST_TYPE); mock implementations return canned data for
// tests. When QUERY_SERVICE_URL is unset the live impl degrades to zero
// threads.
type MailingListSource interface {
	ListMailingListActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]MailingListActivity, error)
}

// VoteChoiceResult is the per-choice vote count for one answer option,
// returned by VoteResultSource.
type VoteChoiceResult struct {
	ChoiceID   string
	ChoiceText string
	VoteCount  int
	Percentage float64
}

// VoteTally is the aggregated result for a single closed vote, returned by
// VoteResultSource. It covers the first (and usually only) poll question.
type VoteTally struct {
	NumRecipients int
	NumVotesCast  int
	NumAbstained  int
	// ChoiceResults is the per-choice breakdown for the first poll question.
	// For simple yes/no votes this has two entries; ranked-choice and
	// multi-question votes may have more.
	ChoiceResults []VoteChoiceResult
}

// VoteActivity is one vote/poll record relevant to the window.
type VoteActivity struct {
	VoteID          string
	Name            string
	URL             string
	Status          string // "Open", "Closed", etc. as returned by the voting service
	ResponseCount   int    // num_response_received
	InvitationCount int    // total_voting_request_invitations
	// Tally is populated by the generator after fetching vote results from
	// VoteResultSource. Nil when the vote is still open or results are
	// unavailable (graceful degrade — brief is still generated without tally).
	Tally *VoteTally
}

// VoteSource returns vote/poll activity in the window.
//
// A live M2M-backed implementation (m2m.VoteSource) calls the query-service
// for the configured resource type (overridable via QUERY_VOTE_TYPE); mock
// implementations return canned data for tests. When QUERY_SERVICE_URL is
// unset the live impl degrades to zero votes.
type VoteSource interface {
	ListVoteActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]VoteActivity, error)
}

// VoteResultSource fetches aggregated vote results for a single closed vote.
// It queries the query service for indexed vote_result resources
// (?type=vote_result&tags=vote_uid:{uid}). The source is optional; when nil
// or when QUERY_SERVICE_URL is unset, the generator omits tally data from the
// brief and degrades gracefully to participation-count-only claim labels.
type VoteResultSource interface {
	GetVoteResults(ctx context.Context, voteUID string) (*VoteTally, error)
}

// SurveyActivity is one survey record relevant to the window.
type SurveyActivity struct {
	// SurveyUID is the survey v2 UUID (used for citation links).
	SurveyUID string
	// Title is the survey title.
	Title string
	// Status is the survey status string as returned by the query service
	// (e.g. "closed", "complete").
	Status string
	// CutoffDate is the response cutoff / close date (UTC). The generator uses
	// this to confirm the survey closed within the window.
	CutoffDate time.Time
	// TotalRecipients is the per-committee recipient count.
	TotalRecipients int
	// TotalResponses is the per-committee response count.
	TotalResponses int
	// URL is the SurveyMonkey collector URL, used for deep-linking.
	URL string
}

// SurveySource returns closed survey activity for a committee in a window.
//
// A live M2M-backed implementation (m2m.SurveySource) calls the query service
// for the configured resource type (overridable via QUERY_SURVEY_TYPE) and
// returns surveys whose cutoff date falls within [windowStart, windowEnd].
// Mock implementations return canned data for tests. When QUERY_SERVICE_URL is
// unset the live impl degrades to zero surveys.
type SurveySource interface {
	ListSurveyActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]SurveyActivity, error)
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
