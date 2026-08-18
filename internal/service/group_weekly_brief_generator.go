// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// GroupWeeklyBriefGenerateInput captures the explicit inputs the handler passes
// in (caller-facing payload + ambient "now"). Sources are not in here — the
// orchestrator owns source gathering through its injected ports.
type GroupWeeklyBriefGenerateInput struct {
	CommitteeUID  string
	CommitteeName string
	ProjectUID    string
	ProjectName   string
	Force         bool
	Now           time.Time
	// MembersHidden is true when the committee's member_visibility setting is
	// "hidden". When set, member names are never included in the generated brief
	// — only counts ("1 new member", "3 new members"). This applies to every
	// rendering of the brief (on-screen and mailing-list share alike) because
	// the brief is generated once and stored as a single text.
	MembersHidden bool
}

// GroupWeeklyBriefGenerateOutput is what the handler shapes for the wire.
type GroupWeeklyBriefGenerateOutput struct {
	Brief    *model.GroupWeeklyBrief
	Throttle *model.GroupWeeklyBriefThrottle
}

// GenerateWeeklyBriefRequestedEvent is the payload published on
// GenerateWeeklyBriefRequestedSubject after a brief is claimed. The durable
// generate consumer decodes it and calls Fulfill. RequestedAt pins the window
// so the async phase computes exactly the same window as the synchronous claim.
type GenerateWeeklyBriefRequestedEvent struct {
	CommitteeUID  string    `json:"committee_uid"`
	CommitteeName string    `json:"committee_name,omitempty"`
	ProjectUID    string    `json:"project_uid,omitempty"`
	ProjectName   string    `json:"project_name,omitempty"`
	Force         bool      `json:"force"`
	RequestedAt   time.Time `json:"requested_at"`
	MembersHidden bool      `json:"members_hidden,omitempty"`
}

// GroupWeeklyBriefGenerator is the orchestration port the HTTP handler and the
// durable generate consumer depend on. The interface exists so handler/consumer
// tests can stub it without spinning up the full graph.
//
// Generation is split into two phases so the HTTP request returns promptly while
// the (potentially slow) LLM call runs out-of-band:
//   - Claim is synchronous: it enforces the edited-brief guard and throttle
//     limits and persists the brief in the "generating" state.
//   - Fulfill is asynchronous: driven by the durable generate consumer, it
//     gathers sources, calls the AI adapter, and finalizes the brief.
type GroupWeeklyBriefGenerator interface {
	// Claim validates the request, applies the edited-brief guard and throttle
	// limits, increments the throttle, and persists the brief in the
	// "generating" state — returning it so the handler can respond 202.
	Claim(ctx context.Context, in GroupWeeklyBriefGenerateInput) (*GroupWeeklyBriefGenerateOutput, error)
	// Fulfill gathers sources, calls the AI adapter, and finalizes the
	// "generating" brief to "generated" (or "error" on no-activity / AI
	// failure). A nil return ACKs the consumer message; a non-nil return NAKs it
	// for retry (used for infrastructure errors).
	Fulfill(ctx context.Context, in GroupWeeklyBriefGenerateInput) error
}

// ActivitySources bundles all external data-source ports for the weekly-brief
// generator into a single value. Required fields: Meetings, MailingLists, Votes,
// MemberReader. Optional fields (nil = graceful degrade): AISummaries,
// VoteResults, Surveys. Adding a new source costs one struct field here rather
// than a generator field + constructor option + nil-check spread across 5 places.
type ActivitySources struct {
	Meetings           port.MeetingSource
	AISummaries        port.MeetingAISummarySource
	MailingLists       port.MailingListSource
	Votes              port.VoteSource
	VoteResults        port.VoteResultSource
	Surveys            port.SurveySource
	ProjectMemberships port.ProjectMembershipSource
	MemberReader       port.CommitteeWeeklyMemberReader
}

type groupWeeklyBriefGenerator struct {
	briefReader   port.GroupWeeklyBriefReader
	briefWriter   port.GroupWeeklyBriefWriter
	sources       ActivitySources
	ai            port.AIAdapter
	publisher     port.CommitteePublisher
	committeeName func(ctx context.Context, uid string) (committeeName, projectName string, err error)
}

// GroupWeeklyBriefGeneratorOption configures the orchestrator.
type GroupWeeklyBriefGeneratorOption func(*groupWeeklyBriefGenerator)

// WithGroupWeeklyBriefReaderForGenerator wires the brief lookup port used to
// detect the "edited brief exists" precondition.
func WithGroupWeeklyBriefReaderForGenerator(r port.GroupWeeklyBriefReader) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.briefReader = r }
}

// WithGroupWeeklyBriefWriter wires the persistence + throttle CAS port.
func WithGroupWeeklyBriefWriter(w port.GroupWeeklyBriefWriter) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.briefWriter = w }
}

// WithActivitySources wires all external data-source ports at once. Required
// fields (Meetings, MailingLists, Votes, MemberReader) are validated by
// NewGroupWeeklyBriefGeneratorOrchestrator. Optional fields (AISummaries,
// VoteResults, Surveys, ProjectMemberships) degrade gracefully to zero when nil.
func WithActivitySources(s ActivitySources) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.sources = s }
}

// WithAIAdapter wires the AI adapter used to compose the brief.
func WithAIAdapter(a port.AIAdapter) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.ai = a }
}

// WithGroupWeeklyBriefPublisher wires the publisher used to emit indexer
// messages after each successful brief state transition in Fulfill.
func WithGroupWeeklyBriefPublisher(p port.CommitteePublisher) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.publisher = p }
}

// WithCommitteeNameLookup wires the function the orchestrator uses to
// hydrate committee and project names for the prompt. The lookup is optional —
// if absent the brief still generates, just with generic labels.
func WithCommitteeNameLookup(f func(ctx context.Context, uid string) (string, string, error)) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.committeeName = f }
}

// NewGroupWeeklyBriefGeneratorOrchestrator builds the orchestrator. All ports
// except the lookup are required.
func NewGroupWeeklyBriefGeneratorOrchestrator(opts ...GroupWeeklyBriefGeneratorOption) GroupWeeklyBriefGenerator {
	g := &groupWeeklyBriefGenerator{}
	for _, opt := range opts {
		opt(g)
	}
	if g.briefReader == nil {
		panic("group-weekly-brief generator: brief reader is required")
	}
	if g.briefWriter == nil {
		panic("group-weekly-brief generator: brief writer is required")
	}
	if g.sources.MemberReader == nil {
		panic("group-weekly-brief generator: member reader is required")
	}
	if g.ai == nil {
		panic("group-weekly-brief generator: AI adapter is required")
	}
	if g.sources.Meetings == nil {
		panic("group-weekly-brief generator: meeting source is required")
	}
	if g.sources.MailingLists == nil {
		panic("group-weekly-brief generator: mailing-list source is required")
	}
	if g.sources.Votes == nil {
		panic("group-weekly-brief generator: vote source is required")
	}
	return g
}

// Claim is the synchronous phase of a generate request. It validates the
// request, applies the edited-brief guard and throttle limits, increments the
// throttle, and persists the brief in the "generating" state — then returns it
// so the handler can respond 202. The source gather + LLM call run later in
// Fulfill, driven by the durable generate consumer.
//
// The throttle is incremented BEFORE persisting the brief so that a throttle
// write failure leaves no brief behind and a retry is a clean generate (the
// caller isn't charged quota for a failed request). The only remaining edge —
// the brief persist failing after the throttle bump — over-counts the throttle
// by one for the window, which is fail-safe and self-corrects at window reset.
func (g *groupWeeklyBriefGenerator) Claim(ctx context.Context, in GroupWeeklyBriefGenerateInput) (*GroupWeeklyBriefGenerateOutput, error) {
	if in.CommitteeUID == "" {
		return nil, errors.NewValidation("committee_uid is required")
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	windowStart, windowEnd := model.WeeklyWindow(in.Now)
	windowReset := model.NextWindowReset(in.Now)

	// 1. Read existing brief (drives edited-guard + regeneration accounting).
	existing, _, err := g.briefReader.GetGroupWeeklyBriefForWindow(ctx, in.CommitteeUID, model.GroupWeeklyBrief{
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	})
	if err != nil {
		return nil, err
	}

	// 2. Edited-brief guard.
	if existing != nil && existing.State == model.GroupWeeklyBriefStateEdited && !in.Force {
		return nil, errors.NewEditedBriefExists(existing.Revision)
	}

	// 3. Throttle pre-check.
	throttle, err := g.briefWriter.GetGroupWeeklyBriefThrottle(ctx, in.CommitteeUID, windowStart)
	if err != nil {
		return nil, err
	}
	if throttle == nil {
		throttle = &model.GroupWeeklyBriefThrottle{
			CommitteeUID:   in.CommitteeUID,
			WindowStart:    windowStart,
			WindowEnd:      windowEnd,
			WindowResetsAt: windowReset,
		}
	}
	isRegeneration := existing != nil
	if isRegeneration {
		if throttle.RegenerationsUsed >= model.GroupWeeklyBriefRegenerationLimit {
			return nil, errors.NewTooManyRequests(
				"regeneration limit reached for this window",
				throttle.GeneratesUsed,
				model.GroupWeeklyBriefGenerateLimit,
				throttle.RegenerationsUsed,
				model.GroupWeeklyBriefRegenerationLimit,
				windowReset.UTC().Format(time.RFC3339),
			)
		}
	} else {
		if throttle.GeneratesUsed >= model.GroupWeeklyBriefGenerateLimit {
			return nil, errors.NewTooManyRequests(
				"generation limit reached for this window",
				throttle.GeneratesUsed,
				model.GroupWeeklyBriefGenerateLimit,
				throttle.RegenerationsUsed,
				model.GroupWeeklyBriefRegenerationLimit,
				windowReset.UTC().Format(time.RFC3339),
			)
		}
	}

	// 4. Throttle accounting — bumped before persisting the brief (see method doc).
	if isRegeneration {
		throttle.RegenerationsUsed++
	} else {
		throttle.GeneratesUsed++
	}
	throttle.WindowResetsAt = windowReset
	updatedThrottle, errTh := g.briefWriter.PutGroupWeeklyBriefThrottle(ctx, throttle)
	if errTh != nil {
		slog.ErrorContext(ctx, "failed to update weekly-brief throttle counter",
			"committee_uid", in.CommitteeUID, "error", errTh)
		return nil, errTh
	}

	// 5. Persist the brief in the "generating" state; Fulfill finalizes it.
	brief := &model.GroupWeeklyBrief{
		CommitteeUID: in.CommitteeUID,
		WindowStart:  windowStart,
		WindowEnd:    windowEnd,
		State:        model.GroupWeeklyBriefStateGenerating,
	}
	if existing != nil {
		brief.UID = existing.UID
		brief.CreatedAt = existing.CreatedAt
		brief.RegenerationCount = existing.RegenerationCount + 1
		brief.Revision = existing.Revision
	}
	persisted, errPut := g.briefWriter.PutGroupWeeklyBrief(ctx, brief)
	if errPut != nil {
		return nil, errPut
	}

	return &GroupWeeklyBriefGenerateOutput{
		Brief:    persisted,
		Throttle: updatedThrottle,
	}, nil
}

// Fulfill is the asynchronous phase, driven by the durable generate consumer.
// It re-reads the "generating" brief for the window, gathers sources, calls the
// AI adapter, and finalizes the brief to "generated" — or to "error" when there
// is no activity or the AI call fails. Terminal outcomes are persisted and the
// message is ACKed (nil return); only infrastructure errors are returned so the
// consumer retries with backoff.
func (g *groupWeeklyBriefGenerator) Fulfill(ctx context.Context, in GroupWeeklyBriefGenerateInput) error {
	if in.CommitteeUID == "" {
		return errors.NewValidation("committee_uid is required")
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	windowStart, windowEnd := model.WeeklyWindow(in.Now)

	// Re-read the claimed brief. If it's gone or no longer "generating", a
	// concurrent worker already handled it — ACK and move on.
	brief, _, err := g.briefReader.GetGroupWeeklyBriefForWindow(ctx, in.CommitteeUID, model.GroupWeeklyBrief{
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	})
	if err != nil {
		return err // infrastructure error → retry
	}
	if brief == nil {
		slog.WarnContext(ctx, "weekly-brief fulfill: no brief found for window — skipping",
			"committee_uid", in.CommitteeUID)
		return nil
	}
	if brief.State != model.GroupWeeklyBriefStateGenerating {
		// A claimed brief should still be "generating" when its event is handled.
		// If it isn't, another worker already finalized it (or it was edited) yet
		// we still received the message — worth a warning, not a silent skip.
		slog.WarnContext(ctx, "weekly-brief fulfill: brief not in generating state — skipping",
			"committee_uid", in.CommitteeUID, "state", brief.State.String())
		return nil
	}

	// Source gathering. Internal (member) source failure is fatal — return it so
	// the consumer retries. External M2M sources use gatherDegradable: a single
	// upstream outage degrades to empty rather than masquerading as "no activity".
	meetings, errMeetings := gatherDegradable(ctx, in.CommitteeUID,
		"weekly-brief fulfill: meeting source failed; continuing with zero meetings",
		func() ([]port.MeetingActivity, error) {
			return g.sources.Meetings.ListMeetingsForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
		})
	members, errMembers := g.sources.MemberReader.ListMemberActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
	if errMembers != nil {
		slog.ErrorContext(ctx, "weekly-brief fulfill: member source failed; will retry",
			"committee_uid", in.CommitteeUID, "error", errMembers)
		return errMembers // internal source error → retry
	}
	mailing, errMailing := gatherDegradable(ctx, in.CommitteeUID,
		"weekly-brief fulfill: mailing list source failed; continuing with zero threads",
		func() ([]port.MailingListActivity, error) {
			return g.sources.MailingLists.ListMailingListActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
		})
	votes, errVotes := gatherDegradable(ctx, in.CommitteeUID,
		"weekly-brief fulfill: vote source failed; continuing with zero votes",
		func() ([]port.VoteActivity, error) {
			return g.sources.Votes.ListVoteActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
		})

	// Enrich closed votes with per-choice tallies. Result fetch errors are
	// non-fatal: the brief still generates with name and participation counts.
	if g.sources.VoteResults != nil {
		g.enrichVotesWithResults(ctx, votes)
	}

	var surveys []port.SurveyActivity
	var errSurveys error
	if g.sources.Surveys != nil {
		surveys, errSurveys = gatherDegradable(ctx, in.CommitteeUID,
			"weekly-brief fulfill: survey source failed; continuing with zero surveys",
			func() ([]port.SurveyActivity, error) {
				return g.sources.Surveys.ListSurveyActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
			})
	}

	// Project memberships are optional — nil source or fetch error both degrade
	// to zero memberships without blocking the brief. An empty ProjectUID is
	// also a soft degrade (committee not yet associated with a project).
	var projectMemberships []port.ProjectMembershipActivity
	var errMemberships error
	if g.sources.ProjectMemberships != nil {
		projectMemberships, errMemberships = g.sources.ProjectMemberships.ListMembershipActivityForWindow(ctx, in.ProjectUID, windowStart, windowEnd)
		if errMemberships != nil {
			slog.ErrorContext(ctx, "weekly-brief fulfill: project membership source failed; continuing with zero memberships",
				"committee_uid", in.CommitteeUID, "project_uid", in.ProjectUID, "error", errMemberships)
			projectMemberships = nil
		}
	}

	// AI summaries are optional — nil source or fetch error both degrade to
	// zero summaries without blocking the brief. A missing summary source is
	// not logged at error level because the source is wired optionally.
	var summaries []port.MeetingAISummaryActivity
	var errSummaries error
	if g.sources.AISummaries != nil {
		summaries, errSummaries = gatherDegradable(ctx, in.CommitteeUID,
			"weekly-brief fulfill: AI summary source failed; continuing with zero summaries",
			func() ([]port.MeetingAISummaryActivity, error) {
				return g.sources.AISummaries.ListAISummariesForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
			})
	}

	memberCount := len(members.Joined) + len(members.Updated)

	// No-source handling. A genuinely empty window (every source returned zero
	// rows with no error) is terminal: the brief is finalized as "no_sources"
	// and the message is ACKed (nil return), since re-delivering the same empty
	// window would just fail again. But if the window is empty ONLY because one
	// or more external sources failed to fetch, that's a transient upstream
	// outage — it must NOT masquerade as "no activity", so we retry instead.
	if len(meetings) == 0 && memberCount == 0 && len(mailing) == 0 && len(votes) == 0 && len(summaries) == 0 && len(surveys) == 0 && len(projectMemberships) == 0 {
		if errMeetings != nil || errMailing != nil || errVotes != nil || errSummaries != nil || errSurveys != nil || errMemberships != nil {
			slog.ErrorContext(ctx, "weekly-brief fulfill: no activity but one or more external sources errored; will retry",
				"committee_uid", in.CommitteeUID,
				"meetings_errored", errMeetings != nil,
				"mailing_errored", errMailing != nil,
				"votes_errored", errVotes != nil,
				"summaries_errored", errSummaries != nil,
				"surveys_errored", errSurveys != nil,
				"memberships_errored", errMemberships != nil,
			)
			// Surface the underlying source error so the consumer retries rather
			// than finalizing a terminal brief over a transient outage.
			retryErr := errMeetings
			if retryErr == nil {
				retryErr = errMailing
			}
			if retryErr == nil {
				retryErr = errVotes
			}
			if retryErr == nil {
				retryErr = errSummaries
			}
			if retryErr == nil {
				retryErr = errSurveys
			}
			if retryErr == nil {
				retryErr = errMemberships
			}
			return retryErr
		}
		slog.InfoContext(ctx, "weekly-brief fulfill: no activity found in window — finalizing brief as error",
			"committee_uid", in.CommitteeUID)
		return g.finalizeError(ctx, brief, "no_sources")
	}

	// Prompt construction (fenced source markers prevent injection).
	committeeName, projectName := in.CommitteeName, in.ProjectName
	if (committeeName == "" || projectName == "") && g.committeeName != nil {
		if cn, pn, errLookup := g.committeeName(ctx, in.CommitteeUID); errLookup == nil {
			if committeeName == "" {
				committeeName = cn
			}
			if projectName == "" {
				projectName = pn
			}
		}
	}
	if len(summaries) > maxSummaryCount {
		summaries = summaries[:maxSummaryCount]
	}
	claims, sourceRefs := buildClaimsAndRefs(meetings, summaries, members, mailing, votes, surveys, projectMemberships, in.MembersHidden)

	aiInput := port.WeeklyBriefInput{
		CommitteeID:   in.CommitteeUID,
		CommitteeName: committeeName,
		ProjectName:   projectName,
		PeriodStart:   windowStart.UTC().Format(time.RFC3339),
		PeriodEnd:     windowEnd.UTC().Format(time.RFC3339),
		Claims:        claims,
		RawContext:    buildRawContext(summaries),
	}

	aiOut, errAI := g.ai.GenerateWeeklyBrief(ctx, aiInput)
	if errAI != nil {
		// Mark the brief as errored so it doesn't stay "generating" forever; the
		// caller can re-trigger generation. (A bounded retry policy could be
		// added later.)
		slog.ErrorContext(ctx, "weekly-brief fulfill: AI generation failed",
			"committee_uid", in.CommitteeUID, "error", errAI)
		return g.finalizeError(ctx, brief, "ai_error")
	}

	// Finalize → generated.
	brief.State = model.GroupWeeklyBriefStateGenerated
	brief.BriefText = aiOut.BriefText
	brief.PromptVersion = g.ai.PromptVersion()
	brief.Model = modelLabelFromAdapter(g.ai)
	brief.PrivateSourcePresent = derivePrivateSourcePresent(memberCount, meetings, summaries, mailing, votes, surveys, projectMemberships)
	brief.SourceRefs = append([]model.SourceRef(nil), sourceRefs...)
	persisted, errPut := g.briefWriter.PutGroupWeeklyBrief(ctx, brief)
	if errPut != nil {
		return errPut // infrastructure / CAS error → retry
	}
	publishGroupWeeklyBriefIndex(ctx, g.publisher, persisted)
	return nil
}

// gatherDegradable calls fetch and returns its result. On error it logs failMsg at
// error level (with committee_uid and error attrs), returns the zero value, and
// passes the error back so Fulfill's no-source check can distinguish a transient
// upstream outage from a genuinely empty window.
func gatherDegradable[T any](ctx context.Context, committeeUID, failMsg string, fetch func() (T, error)) (T, error) {
	result, err := fetch()
	if err != nil {
		slog.ErrorContext(ctx, failMsg, "committee_uid", committeeUID, "error", err)
		var zero T
		return zero, err
	}
	return result, nil
}

// finalizeError transitions the brief to the "error" state and persists it. A
// persist failure is returned so the consumer retries; otherwise it ACKs.
func (g *groupWeeklyBriefGenerator) finalizeError(ctx context.Context, brief *model.GroupWeeklyBrief, reason string) error {
	slog.WarnContext(ctx, "weekly-brief fulfill: finalizing brief in error state",
		"committee_uid", brief.CommitteeUID, "reason", reason)
	brief.State = model.GroupWeeklyBriefStateError
	brief.ErrorReason = reason
	persisted, err := g.briefWriter.PutGroupWeeklyBrief(ctx, brief)
	if err != nil {
		return err
	}
	publishGroupWeeklyBriefIndex(ctx, g.publisher, persisted)
	return nil
}

// buildClaimsAndRefs turns the per-source slices into ClaimEvidence rows and a
// parallel set of source refs persisted on the brief. When membersHidden is
// true (committee's member_visibility == "hidden"), member names are replaced
// with count-only phrases so the stored brief never contains roster data.
func buildClaimsAndRefs(meetings []port.MeetingActivity, summaries []port.MeetingAISummaryActivity, members port.WeeklyMemberActivity, mailing []port.MailingListActivity, votes []port.VoteActivity, surveys []port.SurveyActivity, projectMemberships []port.ProjectMembershipActivity, membersHidden bool) ([]port.ClaimEvidence, []model.SourceRef) {
	claims := make([]port.ClaimEvidence, 0, len(meetings)+len(summaries)+len(mailing)+len(votes)+len(surveys)+len(projectMemberships)+2)
	refs := make([]model.SourceRef, 0, len(meetings)+len(summaries)+len(mailing)+len(votes)+len(surveys)+len(projectMemberships)+2)

	// IMPORTANT: do NOT pass raw untrusted source text (meeting summaries,
	// mailing-list excerpts, vote outcomes) directly into ClaimEvidence.Summary.
	// Claim summaries flow through the AI adapter and may be echoed back in the
	// output, so only sanitized labels (titles via claimLabel) travel through
	// claims. Raw excerpts ARE persisted into SourceRef.Excerpt for the response
	// (sanitized + length-capped) but are not surfaced through claims. Rich
	// AI summary content is passed separately via port.WeeklyBriefInput.RawContext
	// (sanitized via buildRawContext) to keep injection risk contained.
	for _, m := range meetings {
		ref := model.SourceRef{Kind: "meeting", ID: m.UID, Title: m.Title, Excerpt: cleanSummary(m.Summary)}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "meeting-" + m.UID,
			Summary: claimLabel("meeting", m.Title),
			Sources: []port.SourceRef{{Type: "meeting", ID: m.UID}},
		})
	}
	for _, s := range summaries {
		ref := model.SourceRef{Kind: "meeting-summary", ID: s.MeetingAndOccurrenceID, Title: s.Title, Excerpt: cleanSummary(s.Content)}
		refs = append(refs, ref)
		// The claim label is still a sanitized title only — rich content is
		// delivered via RawContext, not through claims (defense-in-depth).
		claims = append(claims, port.ClaimEvidence{
			ID:      "meeting-summary-" + s.MeetingAndOccurrenceID,
			Summary: claimLabel("meeting summary", s.Title),
			Sources: []port.SourceRef{{Type: "meeting-summary", ID: s.MeetingAndOccurrenceID}},
		})
	}
	for _, ml := range mailing {
		ref := model.SourceRef{Kind: "mailing-list", ID: ml.ThreadID, Title: ml.Subject, Excerpt: cleanSummary(ml.Excerpt)}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "mailing-" + ml.ThreadID,
			Summary: claimLabel("mailing-list thread", ml.Subject),
			Sources: []port.SourceRef{{Type: "mailing-list", ID: ml.ThreadID}},
		})
	}
	for _, v := range votes {
		// The excerpt captures participation so it is available in source_refs
		// even when tally detail is unavailable.
		excerpt := voteParticipationExcerpt(v)
		ref := model.SourceRef{Kind: "vote", ID: v.VoteID, Title: v.Name, Excerpt: excerpt}
		refs = append(refs, ref)
		// Vote counts are server-computed integers; choice labels (ChoiceText/
		// ChoiceID) are source-derived free text sanitized in voteTallyLabel via
		// TrimSpace, newline removal, and an 80-rune cap. Name passes through
		// claimLabel for the same treatment.
		claims = append(claims, port.ClaimEvidence{
			ID:      "vote-" + v.VoteID,
			Summary: claimLabel("vote", v.Name) + voteTallyLabel(v),
			Sources: []port.SourceRef{{Type: "vote", ID: v.VoteID}},
		})
	}

	for _, sv := range surveys {
		// Survey title is source-derived free text — sanitize via claimLabel.
		// Response counts are server-computed integers, safe to format directly.
		excerpt := surveyParticipationExcerpt(sv)
		ref := model.SourceRef{Kind: "survey", ID: sv.SurveyUID, Title: sv.Title, Excerpt: excerpt}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "survey-" + sv.SurveyUID,
			Summary: claimLabel("survey", sv.Title) + surveyResponseLabel(sv),
			Sources: []port.SourceRef{{Type: "survey", ID: sv.SurveyUID}},
		})
	}

	for _, pm := range projectMemberships {
		// AccountName and Tier are from the query-service index — sanitize via
		// claimLabel. They are source-derived strings and must not flow
		// unsanitized into the AI prompt.
		title := claimLabel("membership purchased", pm.AccountName)
		if pm.Tier != "" {
			title += " (" + claimLabel("tier", pm.Tier) + ")"
		}
		ref := model.SourceRef{Kind: "project-membership", ID: pm.MembershipUID, Title: pm.AccountName, Excerpt: membershipExcerpt(pm)}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "project-membership-" + pm.MembershipUID,
			Summary: title,
			Sources: []port.SourceRef{{Type: "project-membership", ID: pm.MembershipUID}},
		})
	}

	if len(members.Joined) > 0 || len(members.Updated) > 0 {
		// TODO(member-link): deep-link URLs to member pages are not yet
		// available; the brief still cites by username, but consumers will
		// want a URL when one exists.
		var joinedStr, updatedStr string
		if membersHidden {
			joinedStr = countMemberList(len(members.Joined))
			updatedStr = countMemberList(len(members.Updated))
		} else {
			joinedStr = formatMemberList(members.Joined)
			updatedStr = formatMemberList(members.Updated)
		}

		summaryParts := []string{}
		if joinedStr != "" {
			summaryParts = append(summaryParts, "Members joined: "+joinedStr)
		}
		if updatedStr != "" {
			summaryParts = append(summaryParts, "Members updated: "+updatedStr)
		}
		summary := strings.Join(summaryParts, "; ")

		// Cap the persisted excerpt the same way meetings/mailing/votes are
		// capped (maxExcerptLen), so a week with very many member changes can't
		// exceed the API schema's 5000-rune excerpt contract.
		excerpt := cleanSummary(summary)
		refs = append(refs, model.SourceRef{Kind: "members", ID: "weekly-members", Title: "Member roster changes", Excerpt: excerpt})
		// Members source isn't untrusted (it's our own KV) so we DO surface a
		// real summary on the claim — usernames + counts only, never free
		// text the model could mistake for instructions.
		claims = append(claims, port.ClaimEvidence{
			ID:      "members-week",
			Summary: excerpt,
			Sources: []port.SourceRef{{Type: "members", ID: "weekly-members"}},
		})
	}

	return claims, refs
}

// claimLabel returns a short identifier safe to surface back to the model. It
// strips newlines/carriage returns and truncates to 80 runes. This reduces the
// risk of multi-line control-character tricks slipping through a meeting
// title — it is NOT a complete prompt-injection defence (an 80-rune label can
// still contain adversarial natural-language content). The model's system
// prompt remains the primary line of defence; this layer just hardens the
// surface.
func claimLabel(kind, raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if s == "" {
		return kind
	}
	// Truncate to 80 runes (rune-safe — byte slicing could cut a multi-byte
	// UTF-8 sequence mid-character).
	return kind + ": " + truncateRunes(s, 80)
}

// maxExcerptLen bounds persisted source excerpts to the API schema's excerpt
// maxLength (5000) so upstream text can't exceed the documented contract.
const maxExcerptLen = 5000

// truncateRunes returns s limited to at most maxRunes runes. The appended
// ellipsis counts toward that budget, so a truncated result is maxRunes-1 runes
// of content plus the ellipsis — never more than maxRunes runes total. It
// ranges the string and stops at the limit, so it neither scans the whole input
// nor allocates a full []rune for it. maxRunes <= 0 yields an empty string.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	n, cut := 0, 0
	for i := range s {
		if n == maxRunes-1 {
			cut = i // byte offset after the first maxRunes-1 runes
		}
		if n == maxRunes {
			// There is at least one rune beyond the budget, so truncate and
			// reserve the final rune for the ellipsis.
			return s[:cut] + "…"
		}
		n++
	}
	return s
}

// derivePrivateSourcePresent flags the brief as containing private source
// material whenever members contributed (members are inherently private), any
// meeting, mailing-list thread, or vote was marked private, any AI meeting
// summary contributed (transcript content in a brief is always treated as
// private regardless of the summary's own access level), any survey
// contributed (surveys are always FGA access-controlled, never public), or
// any project membership contributed (the member-service indexer contract
// marks project_membership as public:false with access_check_relation:auditor).
func derivePrivateSourcePresent(memberCount int, meetings []port.MeetingActivity, summaries []port.MeetingAISummaryActivity, mailing []port.MailingListActivity, votes []port.VoteActivity, surveys []port.SurveyActivity, projectMemberships []port.ProjectMembershipActivity) bool {
	if memberCount > 0 {
		return true
	}
	if len(summaries) > 0 {
		return true
	}
	for _, m := range meetings {
		if m.Private {
			return true
		}
	}
	for _, ml := range mailing {
		if ml.Private {
			return true
		}
	}
	// Surveys are access-controlled (FGA access_check_object: survey:{uid}) so
	// any contributing survey makes the brief private.
	if len(surveys) > 0 {
		return true
	}
	// Project memberships are access-controlled (public:false,
	// access_check_relation:auditor on project_membership:{uid}) so any
	// contributing membership makes the brief private.
	if len(projectMemberships) > 0 {
		return true
	}
	// VoteActivity has no Private flag and is treated as non-private.
	_ = votes
	return false
}

// maxSummaryContentLen caps each AI summary's content contribution to the
// RawContext block. This prevents a single very long summary from consuming an
// excessive portion of the model's context window.
const maxSummaryContentLen = 3000

// maxSummaryCount caps the total number of AI summaries included in a brief.
// Each summary adds a claim, a persisted source reference, and up to
// maxSummaryContentLen runes of RawContext; an unbounded slice could exhaust
// the model's context window across many meetings.
const maxSummaryCount = 10

// collapseHyphens reduces every run of three or more consecutive hyphens to
// "--". A single strings.ReplaceAll("---", "--") is insufficient: "----"
// contains one non-overlapping "---" starting at position 0, which becomes
// "--", leaving "---" in the output. Iterating until stable handles all cases.
func collapseHyphens(s string) string {
	for strings.Contains(s, "---") {
		s = strings.ReplaceAll(s, "---", "--")
	}
	return s
}

// buildRawContext produces a fenced, sanitized block of AI meeting summary
// content for the weekly-brief prompt. Each summary is separated by a header
// line so the model can cite by meeting title and date. Returns an empty string
// when no summaries are provided.
//
// Sanitization: runs of three or more hyphens in titles and content are
// collapsed to "--" (preventing fence-header forgery), titles are passed
// through cleanSummary to strip newlines, and content is truncated to
// maxSummaryContentLen runes. At most maxSummaryCount summaries are rendered.
func buildRawContext(summaries []port.MeetingAISummaryActivity) string {
	if len(summaries) == 0 {
		return ""
	}
	if len(summaries) > maxSummaryCount {
		summaries = summaries[:maxSummaryCount]
	}
	var b strings.Builder
	for _, s := range summaries {
		// Normalize title: collapse whitespace, strip newlines, then cap length.
		title := collapseHyphens(cleanSummary(s.Title))
		if title == "" {
			title = "Untitled Meeting"
		}
		dateStr := ""
		if !s.StartTime.IsZero() {
			dateStr = s.StartTime.UTC().Format("2006-01-02")
		}
		header := "--- meeting: " + truncateRunes(title, 80)
		if dateStr != "" {
			header += " (" + dateStr + ")"
		}
		header += " ---"

		// Sanitize content: collapse hyphen runs that could break the fenced
		// block structure, then collapse whitespace and truncate.
		sanitized := collapseHyphens(s.Content)
		sanitized = cleanSummary(sanitized)
		sanitized = truncateRunes(sanitized, maxSummaryContentLen)

		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(sanitized)
	}
	return b.String()
}

// modelLabelFromAdapter returns a short identifier for the AI adapter. The
// fake adapter has no model config, so we return "fake"; the live adapter's
// concrete type isn't imported here to avoid a layering cycle, so we look at
// the type name as a fallback.
func modelLabelFromAdapter(a port.AIAdapter) string {
	name := fmt.Sprintf("%T", a)
	if strings.Contains(strings.ToLower(name), "fake") {
		return "fake"
	}
	if strings.Contains(strings.ToLower(name), "litellm") {
		return "litellm"
	}
	return name
}

func cleanSummary(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Collapse newlines and carriage returns so the prompt is compact and no
	// stray control characters leak into the prompt-data block; leave other
	// whitespace alone. Bound the length to the API excerpt cap.
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	return truncateRunes(s, maxExcerptLen)
}

// countMemberList returns a count-only phrase used when member_visibility is
// "hidden" — names are never included. Zero returns an empty string.
func countMemberList(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 new member"
	default:
		return fmt.Sprintf("%d new members", n)
	}
}

// memberLabel returns a human-readable identifier for a member to cite in the
// brief. Priority: full name → first name → username → "a new member".
// Last-name-only is skipped ("welcomed Doe" reads like a typo). The raw UID
// is never returned (LFXV2-2990).
func memberLabel(m *model.CommitteeMember) string {
	if m == nil {
		return "a new member"
	}
	first := strings.TrimSpace(m.FirstName)
	last := strings.TrimSpace(m.LastName)
	username := strings.TrimSpace(m.Username)
	switch {
	case first != "" && last != "":
		return first + " " + last
	case first != "":
		return first
	case username != "":
		return username
	default:
		return "a new member"
	}
}

// formatMemberList renders a slice of members into a prose list for the brief
// summary. Members with a resolvable name are listed individually; unresolved
// members are aggregated so "a new member" never appears more than once inline.
//
// Examples:
//
//	["Jane Doe", "John Smith"]            → "Jane Doe, John Smith"
//	["Jane Doe"] + 2 unresolved          → "Jane Doe, and 2 others"
//	3 unresolved                         → "3 new members"
//	1 unresolved                         → "a new member"
func formatMemberList(members []*model.CommitteeMember) string {
	var named []string
	unnamed := 0
	for _, m := range members {
		label := memberLabel(m)
		if label == "a new member" {
			unnamed++
		} else {
			named = append(named, label)
		}
	}
	switch {
	case len(named) == 0 && unnamed == 0:
		return ""
	case len(named) == 0 && unnamed == 1:
		return "a new member"
	case len(named) == 0:
		return fmt.Sprintf("%d new members", unnamed)
	case unnamed == 0:
		return strings.Join(named, ", ")
	case unnamed == 1:
		return strings.Join(named, ", ") + ", and 1 other"
	default:
		return strings.Join(named, ", ") + fmt.Sprintf(", and %d others", unnamed)
	}
}

// enrichVotesWithResults calls GetVoteResults for each vote in the slice and
// populates Tally in-place. Errors per vote are logged and skipped — a failed
// tally fetch never blocks brief generation.
func (g *groupWeeklyBriefGenerator) enrichVotesWithResults(ctx context.Context, votes []port.VoteActivity) {
	for i := range votes {
		tally, err := g.sources.VoteResults.GetVoteResults(ctx, votes[i].VoteID)
		if err != nil {
			slog.WarnContext(ctx, "weekly-brief fulfill: vote result fetch failed; continuing without tally",
				"vote_id", votes[i].VoteID, "error", err)
			continue
		}
		votes[i].Tally = tally
	}
}

// voteParticipationExcerpt returns a short participation summary for the
// source_ref excerpt. When tally data is available it prefers the numeric
// breakdown; otherwise it falls back to the response/invitation counts from
// the query-service index.
func voteParticipationExcerpt(v port.VoteActivity) string {
	if v.Tally != nil {
		return fmt.Sprintf("%d of %d voted, %d abstained",
			v.Tally.NumVotesCast, v.Tally.NumRecipients, v.Tally.NumAbstained)
	}
	if v.InvitationCount > 0 {
		return fmt.Sprintf("%d of %d responded", v.ResponseCount, v.InvitationCount)
	}
	return ""
}

// voteTallyLabel returns a suffix for the claim summary when per-choice tally
// data is available. The leading space is included in the suffix so callers can
// concatenate directly after claimLabel output.
//
// Example output: " — 8 yes, 2 no (8 of 10 voted)"
//
// When no tally is available (Tally is nil or ChoiceResults is empty) an empty
// string is returned and the claim reads as just the vote name without tallies.
func voteTallyLabel(v port.VoteActivity) string {
	if v.Tally == nil || len(v.Tally.ChoiceResults) == 0 {
		return ""
	}
	parts := make([]string, 0, len(v.Tally.ChoiceResults))
	for _, c := range v.Tally.ChoiceResults {
		label := strings.TrimSpace(c.ChoiceText)
		if label == "" {
			label = strings.TrimSpace(c.ChoiceID)
		}
		label = strings.ReplaceAll(label, "\n", " ")
		label = strings.ReplaceAll(label, "\r", " ")
		label = truncateRunes(label, 80)
		parts = append(parts, fmt.Sprintf("%d %s", c.VoteCount, label))
	}
	tally := strings.Join(parts, ", ")
	return fmt.Sprintf(" — %s (%d of %d voted)", tally, v.Tally.NumVotesCast, v.Tally.NumRecipients)
}

// surveyResponseLabel returns a response-rate suffix for the claim summary.
// Example: " — 14 of 30 responded"
// Returns empty string when no recipient count is available.
func surveyResponseLabel(sv port.SurveyActivity) string {
	if sv.TotalRecipients == 0 {
		return ""
	}
	return fmt.Sprintf(" — %d of %d responded", sv.TotalResponses, sv.TotalRecipients)
}

// surveyParticipationExcerpt returns a short response-rate string for the
// source_ref excerpt, including percentage when recipients > 0.
// Example: "14 of 30 responded (47%)"
func surveyParticipationExcerpt(sv port.SurveyActivity) string {
	if sv.TotalRecipients == 0 {
		return ""
	}
	pct := int(math.Round(float64(sv.TotalResponses) / float64(sv.TotalRecipients) * 100))
	return fmt.Sprintf("%d of %d responded (%d%%)", sv.TotalResponses, sv.TotalRecipients, pct)
}

// membershipExcerpt returns a short description for a project_membership
// source_ref. It uses tier and purchase date so a reader can identify the
// record without following a link.
// Example: "Membership tier: Silver, purchased 2026-08-14"
// "Joined as" is intentionally avoided: the source may include renewals
// alongside new memberships (is_first_membership is not yet indexed in
// OpenSearch), so implying a first-time join would be inaccurate.
func membershipExcerpt(pm port.ProjectMembershipActivity) string {
	parts := []string{}
	if pm.Tier != "" {
		parts = append(parts, "membership tier: "+pm.Tier)
	}
	if !pm.PurchaseDate.IsZero() {
		parts = append(parts, "purchased "+pm.PurchaseDate.UTC().Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, ", ")
	if len(s) > 0 {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	return cleanSummary(s)
}

// publishGroupWeeklyBriefIndex emits a group_weekly_brief indexer message for
// the given brief. The publish is best-effort: a failure is logged as a warning
// but does not fail the calling operation because the brief is already persisted
// in NATS KV. The backfill CLI command (sync backfill-weekly-brief-index) is
// the recovery path for any missed emissions. When publisher is nil (mock-mode
// or tests without a publisher) the call is a no-op.
func publishGroupWeeklyBriefIndex(ctx context.Context, publisher port.CommitteePublisher, brief *model.GroupWeeklyBrief) {
	if publisher == nil || brief == nil {
		return
	}
	public := false
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             brief.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", brief.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", brief.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", brief.CommitteeUID)},
		Fulltext:             brief.BriefText,
		Tags:                 brief.Tags(),
		Public:               &public,
	}
	msg := model.CommitteeIndexerMessage{
		Action:         model.ActionUpdated,
		Tags:           brief.Tags(),
		IndexingConfig: indexingConfig,
	}
	built, err := msg.Build(ctx, brief)
	if err != nil {
		slog.WarnContext(ctx, "weekly-brief: failed to build indexer message; skipping publish",
			"brief_uid", brief.UID,
			"committee_uid", brief.CommitteeUID,
			"error", err,
		)
		return
	}
	if err := publisher.Indexer(ctx, constants.IndexGroupWeeklyBriefSubject, built, false); err != nil {
		slog.WarnContext(ctx, "weekly-brief: failed to publish indexer message; brief persisted but not indexed",
			"brief_uid", brief.UID,
			"committee_uid", brief.CommitteeUID,
			"error", err,
		)
	}
}
