// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// WeeklyBriefInput is the structured prompt input that drives weekly brief
// generation. Adapters MUST be deterministic with respect to this struct:
// identical input MUST produce identical output for fake/test adapters.
type WeeklyBriefInput struct {
	// CommitteeID identifies the committee the brief is generated for.
	CommitteeID string
	// CommitteeName is the human-readable committee name.
	CommitteeName string
	// ProjectName is the parent project's name (optional).
	ProjectName string
	// PeriodStart and PeriodEnd describe the reporting window (RFC3339 strings).
	// They are strings rather than time.Time so callers can pass canonical values
	// without time-zone ambiguity and so adapters never reach for time.Now().
	PeriodStart string
	PeriodEnd   string
	// Claims is the curated set of evidence rows the model should ground on.
	Claims []ClaimEvidence
	// RawContext is a sanitized, fenced block of source excerpts that the
	// adapter should include verbatim in the prompt for richer grounding.
	// Each entry is delimited by a header line so the model can cite by source.
	// Empty string when no approved AI summaries are available for the window.
	RawContext string
}

// ClaimEvidence is one grounded fact the adapter should weave into the brief.
type ClaimEvidence struct {
	// ID is a stable identifier for this claim (used in WeeklyBrief.ClaimIDs).
	ID string
	// Summary is a short natural-language sentence describing the claim.
	Summary string
	// Sources are the URLs/refs that back the claim.
	Sources []SourceRef
}

// SourceRef is a reference to a source artifact used to ground a claim.
type SourceRef struct {
	// Type is the source category, e.g. "mailing-list", "meeting", "issue", "doc".
	Type string
	// ID is the stable identifier inside that source (URL, message-id, etc.).
	ID string
}

// WeeklyBrief is the structured output returned by an AIAdapter.
// Every field is required to be populated by adapters: at least one claim ID,
// at least one source ref, and BriefText containing at least two paragraphs.
type WeeklyBrief struct {
	// ClaimIDs lists the claim IDs that the brief grounds itself on. Must be >=1.
	ClaimIDs []string
	// SourceRefs are the evidence references used to compose the brief. Must be >=1.
	SourceRefs []SourceRef
	// BriefText is the human-readable brief. Must contain at least two paragraphs
	// (paragraphs separated by a blank line).
	BriefText string
}

// AIAdapter is the port the application layer uses to generate weekly briefs.
// Implementations live under internal/infrastructure/ai and are selected at
// startup via the AI_SOURCE environment variable (see providers.go).
type AIAdapter interface {
	// GenerateWeeklyBrief returns a structured WeeklyBrief for the given input.
	// Implementations MUST validate that the returned WeeklyBrief has at least
	// one claim_id, one source_ref, and a two-paragraph brief.
	GenerateWeeklyBrief(ctx context.Context, in WeeklyBriefInput) (WeeklyBrief, error)

	// PromptVersion returns the version label of the prompt template currently
	// loaded by this adapter. The value is persisted on every generated brief
	// so regressions can be traced back to the exact prompt revision.
	// Implementations that do not use configurable prompts (e.g. FakeAdapter)
	// return a fixed label such as "fake".
	PromptVersion() string
}
