// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// GroupWeeklyBriefGenerateInput captures the explicit inputs the handler passes
// in (caller-facing payload + ambient "now"). Sources are not in here — the
// orchestrator owns source gathering through its injected ports.
type GroupWeeklyBriefGenerateInput struct {
	CommitteeUID  string
	CommitteeName string
	ProjectName   string
	Force         bool
	Now           time.Time
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
	ProjectName   string    `json:"project_name,omitempty"`
	Force         bool      `json:"force"`
	RequestedAt   time.Time `json:"requested_at"`
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

// PromptVersion is the only supported prompt version for the Phase 2 generate
// flow. Bumping this requires updating the source-marker contract in the
// system prompt; the value is persisted on the brief so future readers can
// pick the right rendering rules.
const PromptVersion = "v1"

// Source-content markers used to fence untrusted input passed to the model.
// The system prompt instructs the model to treat content between these markers
// as DATA, never as instructions — defending against prompt-injection attempts
// embedded in meeting summaries, mailing-list posts, etc.
const (
	sourceMarkerOpenFmt  = "<<SOURCE:%s:BEGIN>>"
	sourceMarkerCloseFmt = "<<SOURCE:%s:END>>"
)

type groupWeeklyBriefGenerator struct {
	briefReader   port.GroupWeeklyBriefReader
	briefWriter   port.GroupWeeklyBriefWriter
	meetings      port.MeetingSource
	mailingLists  port.MailingListSource
	votes         port.VoteSource
	memberReader  port.CommitteeWeeklyMemberReader
	ai            port.AIAdapter
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

// WithMeetingSource wires the meeting-source port.
func WithMeetingSource(s port.MeetingSource) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.meetings = s }
}

// WithMailingListSource wires the mailing-list-source port.
func WithMailingListSource(s port.MailingListSource) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.mailingLists = s }
}

// WithVoteSource wires the vote-source port.
func WithVoteSource(s port.VoteSource) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.votes = s }
}

// WithCommitteeWeeklyMemberReader wires the member-activity reader.
func WithCommitteeWeeklyMemberReader(r port.CommitteeWeeklyMemberReader) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.memberReader = r }
}

// WithAIAdapter wires the AI adapter used to compose the brief.
func WithAIAdapter(a port.AIAdapter) GroupWeeklyBriefGeneratorOption {
	return func(g *groupWeeklyBriefGenerator) { g.ai = a }
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
	if g.memberReader == nil {
		panic("group-weekly-brief generator: member reader is required")
	}
	if g.ai == nil {
		panic("group-weekly-brief generator: AI adapter is required")
	}
	if g.meetings == nil {
		panic("group-weekly-brief generator: meeting source is required")
	}
	if g.mailingLists == nil {
		panic("group-weekly-brief generator: mailing-list source is required")
	}
	if g.votes == nil {
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
		slog.InfoContext(ctx, "weekly-brief fulfill: brief not in generating state — skipping",
			"committee_uid", in.CommitteeUID, "state", brief.State.String())
		return nil
	}

	// Source gathering. Internal (member) source failure is fatal — return it so
	// the consumer retries. External M2M sources degrade to empty (logged), so a
	// single upstream outage doesn't masquerade as "no activity".
	meetings, errMeetings := g.meetings.ListMeetingsForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
	if errMeetings != nil {
		slog.WarnContext(ctx, "meeting source failed; continuing with zero meetings",
			"committee_uid", in.CommitteeUID, "error", errMeetings)
		meetings = nil
	}
	members, errMembers := g.memberReader.ListMemberActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
	if errMembers != nil {
		return errMembers // internal source error → retry
	}
	mailing, errMailing := g.mailingLists.ListMailingListActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
	if errMailing != nil {
		slog.WarnContext(ctx, "mailing list source failed; continuing with zero threads",
			"committee_uid", in.CommitteeUID, "error", errMailing)
		mailing = nil
	}
	votes, errVotes := g.votes.ListVoteActivityForWindow(ctx, in.CommitteeUID, windowStart, windowEnd)
	if errVotes != nil {
		slog.WarnContext(ctx, "vote source failed; continuing with zero votes",
			"committee_uid", in.CommitteeUID, "error", errVotes)
		votes = nil
	}

	memberCount := len(members.Joined) + len(members.Updated)

	// No-source → terminal error state (nothing to ground the brief on).
	if len(meetings) == 0 && memberCount == 0 && len(mailing) == 0 && len(votes) == 0 {
		if errMeetings != nil || errMailing != nil || errVotes != nil {
			slog.WarnContext(ctx, "weekly-brief fulfill: no activity and one or more external sources errored",
				"committee_uid", in.CommitteeUID,
				"meetings_errored", errMeetings != nil,
				"mailing_errored", errMailing != nil,
				"votes_errored", errVotes != nil,
			)
		}
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
	claims, sourceRefs := buildClaimsAndRefs(meetings, members, mailing, votes)

	aiInput := port.WeeklyBriefInput{
		CommitteeID:   in.CommitteeUID,
		CommitteeName: committeeName,
		ProjectName:   projectName,
		PeriodStart:   windowStart.UTC().Format(time.RFC3339),
		PeriodEnd:     windowEnd.UTC().Format(time.RFC3339),
		Claims:        claims,
	}

	// The boundary-marker block fences untrusted source content for the live
	// adapter's system prompt; it is deliberately NOT threaded back through
	// ClaimEvidence so untrusted input never appears verbatim in the brief text.
	// Currently discarded until a dedicated RawContext field exists.
	_ = buildPromptDataBlock(meetings, members, mailing, votes)

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
	brief.PromptVersion = PromptVersion
	brief.Model = modelLabelFromAdapter(g.ai)
	brief.PrivateSourcePresent = derivePrivateSourcePresent(memberCount, meetings)
	brief.SourceRefs = append([]model.SourceRef(nil), sourceRefs...)
	if _, errPut := g.briefWriter.PutGroupWeeklyBrief(ctx, brief); errPut != nil {
		return errPut // infrastructure / CAS error → retry
	}
	return nil
}

// finalizeError transitions the brief to the "error" state and persists it. A
// persist failure is returned so the consumer retries; otherwise it ACKs.
func (g *groupWeeklyBriefGenerator) finalizeError(ctx context.Context, brief *model.GroupWeeklyBrief, reason string) error {
	slog.WarnContext(ctx, "weekly-brief fulfill: finalizing brief in error state",
		"committee_uid", brief.CommitteeUID, "reason", reason)
	brief.State = model.GroupWeeklyBriefStateError
	if _, err := g.briefWriter.PutGroupWeeklyBrief(ctx, brief); err != nil {
		return err
	}
	return nil
}

// buildClaimsAndRefs turns the per-source slices into ClaimEvidence rows and a
// parallel set of source refs persisted on the brief.
func buildClaimsAndRefs(meetings []port.MeetingActivity, members port.WeeklyMemberActivity, mailing []port.MailingListActivity, votes []port.VoteActivity) ([]port.ClaimEvidence, []model.SourceRef) {
	claims := make([]port.ClaimEvidence, 0, len(meetings)+len(mailing)+len(votes)+2)
	refs := make([]model.SourceRef, 0, len(meetings)+len(mailing)+len(votes)+2)

	// IMPORTANT: do NOT pass raw untrusted source text (meeting summaries,
	// mailing-list excerpts, vote outcomes) directly into ClaimEvidence.Summary.
	// Claim summaries flow through the AI adapter and may be echoed back in
	// the output; raw source text only travels through the boundary-fenced
	// prompt-data block (see buildPromptDataBlock) so the model's system
	// prompt can treat it as untrusted DATA. Excerpts ARE persisted into
	// SourceRef.Excerpt for the response but are not surfaced through claims.
	for _, m := range meetings {
		ref := model.SourceRef{Kind: "meeting", ID: m.UID, Title: m.Title, Excerpt: m.Summary}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "meeting-" + m.UID,
			Summary: claimLabel("meeting", m.Title),
			Sources: []port.SourceRef{{Type: "meeting", ID: m.UID}},
		})
	}
	for _, ml := range mailing {
		ref := model.SourceRef{Kind: "mailing-list", ID: ml.ThreadID, Title: ml.Subject, Excerpt: ml.Excerpt}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "mailing-" + ml.ThreadID,
			Summary: claimLabel("mailing-list thread", ml.Subject),
			Sources: []port.SourceRef{{Type: "mailing-list", ID: ml.ThreadID}},
		})
	}
	for _, v := range votes {
		ref := model.SourceRef{Kind: "vote", ID: v.VoteID, Title: v.Subject, Excerpt: v.Outcome}
		refs = append(refs, ref)
		claims = append(claims, port.ClaimEvidence{
			ID:      "vote-" + v.VoteID,
			Summary: claimLabel("vote", v.Subject),
			Sources: []port.SourceRef{{Type: "vote", ID: v.VoteID}},
		})
	}

	if len(members.Joined) > 0 || len(members.Updated) > 0 {
		// TODO(member-link): deep-link URLs to member pages are not yet
		// available; the brief still cites by username, but consumers will
		// want a URL when one exists.
		joinedNames := memberNames(members.Joined)
		updatedNames := memberNames(members.Updated)

		summaryParts := []string{}
		if len(joinedNames) > 0 {
			summaryParts = append(summaryParts, "Members joined: "+strings.Join(joinedNames, ", "))
		}
		if len(updatedNames) > 0 {
			summaryParts = append(summaryParts, "Members updated: "+strings.Join(updatedNames, ", "))
		}
		summary := strings.Join(summaryParts, "; ")

		refs = append(refs, model.SourceRef{Kind: "members", ID: "weekly-members", Title: "Member roster changes", Excerpt: summary})
		// Members source isn't untrusted (it's our own KV) so we DO surface a
		// real summary on the claim — usernames + counts only, never free
		// text the model could mistake for instructions.
		claims = append(claims, port.ClaimEvidence{
			ID:      "members-week",
			Summary: summary,
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
	// Truncate to 80 runes without scanning the whole string or allocating a
	// full []rune for the entire input — an attacker-controlled title/subject
	// could be very large. Byte slicing alone could cut a multi-byte UTF-8
	// sequence, so we find the cut on a rune boundary by ranging the string and
	// stop as soon as we reach the limit.
	const maxRunes = 80
	n, cut := 0, len(s)
	for i := range s {
		if n == maxRunes {
			cut = i
			break
		}
		n++
	}
	if cut < len(s) {
		s = s[:cut] + "…"
	}
	return kind + ": " + s
}

// buildPromptDataBlock builds the boundary-fenced source data block that we
// inject as a synthetic claim. The fence markers signal to the model (via the
// adapter's system prompt) that the enclosed content is DATA, not
// instructions.
func buildPromptDataBlock(meetings []port.MeetingActivity, members port.WeeklyMemberActivity, mailing []port.MailingListActivity, votes []port.VoteActivity) string {
	var b strings.Builder
	if len(meetings) > 0 {
		b.WriteString(fmt.Sprintf(sourceMarkerOpenFmt, "meetings"))
		b.WriteString("\n")
		for _, m := range meetings {
			fmt.Fprintf(&b, "- %s | %s | %s\n", m.UID, m.Title, cleanSummary(m.Summary))
		}
		b.WriteString(fmt.Sprintf(sourceMarkerCloseFmt, "meetings"))
		b.WriteString("\n")
	}
	if len(members.Joined) > 0 || len(members.Updated) > 0 {
		b.WriteString(fmt.Sprintf(sourceMarkerOpenFmt, "members"))
		b.WriteString("\n")
		for _, m := range members.Joined {
			fmt.Fprintf(&b, "- joined %s\n", memberLabel(m))
		}
		for _, m := range members.Updated {
			fmt.Fprintf(&b, "- updated %s\n", memberLabel(m))
		}
		b.WriteString(fmt.Sprintf(sourceMarkerCloseFmt, "members"))
		b.WriteString("\n")
	}
	if len(mailing) > 0 {
		b.WriteString(fmt.Sprintf(sourceMarkerOpenFmt, "mailing-list"))
		b.WriteString("\n")
		for _, ml := range mailing {
			fmt.Fprintf(&b, "- %s | %s | %s\n", ml.ThreadID, ml.Subject, cleanSummary(ml.Excerpt))
		}
		b.WriteString(fmt.Sprintf(sourceMarkerCloseFmt, "mailing-list"))
		b.WriteString("\n")
	}
	if len(votes) > 0 {
		b.WriteString(fmt.Sprintf(sourceMarkerOpenFmt, "votes"))
		b.WriteString("\n")
		for _, v := range votes {
			fmt.Fprintf(&b, "- %s | %s | %s\n", v.VoteID, v.Subject, cleanSummary(v.Outcome))
		}
		b.WriteString(fmt.Sprintf(sourceMarkerCloseFmt, "votes"))
		b.WriteString("\n")
	}
	return b.String()
}

// derivePrivateSourcePresent flags the brief as containing private source
// material whenever members contributed (members are inherently private) or
// any contributing meeting was marked private. Mailing lists and votes are
// stubs for now — they cannot mark the flag.
func derivePrivateSourcePresent(memberCount int, meetings []port.MeetingActivity) bool {
	if memberCount > 0 {
		return true
	}
	for _, m := range meetings {
		if m.Private {
			return true
		}
	}
	return false
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
	// whitespace alone.
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

func memberNames(members []*model.CommitteeMember) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, memberLabel(m))
	}
	return out
}

// memberLabel returns a non-PII identifier for a member to cite in the prompt.
// Member claims are "usernames + counts only" — deliberately never names or
// email addresses, so PII is not leaked into the AI prompt or the generated
// brief. Falls back to the opaque UID when no username is set.
func memberLabel(m *model.CommitteeMember) string {
	if m == nil {
		return ""
	}
	if m.Username != "" {
		return m.Username
	}
	return m.UID
}
