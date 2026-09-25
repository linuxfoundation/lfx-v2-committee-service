// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

func sampleInput() port.WeeklyBriefInput {
	return port.WeeklyBriefInput{
		CommitteeID:   "cmt-123",
		CommitteeName: "Technical Steering Committee",
		ProjectName:   "Acme",
		PeriodStart:   "2026-05-15T00:00:00Z",
		PeriodEnd:     "2026-05-22T00:00:00Z",
		Claims: []port.ClaimEvidence{
			{
				ID:      "claim-mailing-list-1",
				Summary: "Two RFCs merged this week",
				Sources: []port.SourceRef{
					{Type: "mailing-list", ID: "msg-001"},
					{Type: "issue", ID: "gh-42"},
				},
			},
			{
				ID:      "claim-meeting-1",
				Summary: "TSC voted to adopt the new release cadence",
				Sources: []port.SourceRef{
					{Type: "meeting", ID: "mtg-2026-05-20"},
				},
			},
		},
	}
}

func TestFakeAdapter_Determinism(t *testing.T) {
	t.Parallel()
	a := NewFakeAdapter()
	ctx := context.Background()

	out1, err := a.GenerateWeeklyBrief(ctx, sampleInput())
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	out2, err := a.GenerateWeeklyBrief(ctx, sampleInput())
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}

	if !reflect.DeepEqual(out1, out2) {
		t.Fatalf("fake adapter is not deterministic\nfirst:  %+v\nsecond: %+v", out1, out2)
	}
}

func TestFakeAdapter_SchemaValidity(t *testing.T) {
	t.Parallel()
	a := NewFakeAdapter()
	out, err := a.GenerateWeeklyBrief(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out.ClaimIDs) < 1 {
		t.Errorf("expected >=1 claim_id, got %d", len(out.ClaimIDs))
	}
	if len(out.SourceRefs) < 1 {
		t.Errorf("expected >=1 source_ref, got %d", len(out.SourceRefs))
	}
	if strings.TrimSpace(out.BriefText) == "" {
		t.Errorf("expected non-empty BriefText")
	}
	paras := strings.Split(out.BriefText, "\n\n")
	nonEmpty := 0
	for _, p := range paras {
		if strings.TrimSpace(p) != "" {
			nonEmpty++
		}
	}
	if nonEmpty < 2 {
		t.Errorf("expected >=2 paragraphs in BriefText, got %d (text=%q)", nonEmpty, out.BriefText)
	}
}

func TestFakeAdapter_HandlesEmptyClaims(t *testing.T) {
	t.Parallel()
	a := NewFakeAdapter()
	out, err := a.GenerateWeeklyBrief(context.Background(), port.WeeklyBriefInput{
		CommitteeID:   "cmt-empty",
		CommitteeName: "Empty WG",
		ProjectName:   "Acme",
		PeriodStart:   "2026-05-15T00:00:00Z",
		PeriodEnd:     "2026-05-22T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.ClaimIDs) < 1 {
		t.Fatalf("expected synthesised claim id when no claims supplied")
	}
	if len(out.SourceRefs) < 1 {
		t.Fatalf("expected synthesised source ref when no claims supplied")
	}
	paras := strings.Split(out.BriefText, "\n\n")
	if len(paras) < 2 {
		t.Fatalf("expected >=2 paragraphs even with empty claims")
	}
}

func TestFakeAdapter_StableAcrossClaimOrdering(t *testing.T) {
	t.Parallel()
	a := NewFakeAdapter()
	base := sampleInput()
	shuffled := sampleInput()
	// Swap claim order.
	shuffled.Claims[0], shuffled.Claims[1] = shuffled.Claims[1], shuffled.Claims[0]

	out1, err := a.GenerateWeeklyBrief(context.Background(), base)
	if err != nil {
		t.Fatalf("base call: %v", err)
	}
	out2, err := a.GenerateWeeklyBrief(context.Background(), shuffled)
	if err != nil {
		t.Fatalf("shuffled call: %v", err)
	}
	if !reflect.DeepEqual(out1, out2) {
		t.Fatalf("fake adapter output should be stable across claim ordering\nbase:     %+v\nshuffled: %+v", out1, out2)
	}
}
