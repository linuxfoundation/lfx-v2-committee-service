// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build live
// +build live

// This file is built only when -tags=live is passed. It documents and
// implements the live-LLM eval path against a LiteLLM-compatible endpoint.
// CI never compiles this file; run it manually pre-release.
//
// The -tags flag is a build flag and MUST appear before the package pattern,
// otherwise the test binary rejects it as an unknown flag:
//
//	LITELLM_BASE_URL=... LITELLM_API_KEY=... LITELLM_MODEL=... \
//	  go test -tags=live -run TestWeeklyBriefEvalLive ./evals/weekly-brief/...

package weeklybriefeval

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/ai"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

// TestWeeklyBriefEvalLive runs the same fixtures as the fake-AI eval, but
// against a live LiteLLM endpoint. Passing -tags=live is an explicit opt-in,
// so missing required env vars are a hard failure rather than a silent skip —
// otherwise the run would appear green without anything actually executing.
func TestWeeklyBriefEvalLive(t *testing.T) {
	baseURL := os.Getenv("LITELLM_BASE_URL")
	apiKey := os.Getenv("LITELLM_API_KEY")
	modelName := os.Getenv("LITELLM_MODEL")
	var missing []string
	if baseURL == "" {
		missing = append(missing, "LITELLM_BASE_URL")
	}
	if apiKey == "" {
		missing = append(missing, "LITELLM_API_KEY")
	}
	if modelName == "" {
		missing = append(missing, "LITELLM_MODEL")
	}
	if len(missing) > 0 {
		t.Fatalf("live eval requires %v to be set — these must be provided when running with -tags=live", missing)
	}

	adapter := ai.NewLiteLLMAdapter(ai.LiteLLMConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
		Timeout: 120 * time.Second,
	})

	cases := []struct {
		fixtureName string
		extra       func(t *testing.T, brief *model.GroupWeeklyBrief)
	}{
		{fixtureName: "normal_week"},
		{fixtureName: "low_data_week"},
		{
			fixtureName: "prompt_injection",
			extra: func(t *testing.T, brief *model.GroupWeeklyBrief) {
				assertPromptInjectionContained(t, brief)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.fixtureName, func(t *testing.T) {
			fx := loadFixture(t, tc.fixtureName)
			g, bw, _ := buildOrchestrator(fx, adapter)

			in := service.GroupWeeklyBriefGenerateInput{
				CommitteeUID:  fx.CommitteeUID,
				CommitteeName: fx.CommitteeName,
				ProjectName:   fx.ProjectName,
				Now:           fx.Now,
			}
			_, err := g.Claim(context.Background(), in)
			require.NoErrorf(t, err, "[%s] live claim returned error", fx.Name)
			require.NoErrorf(t, g.Fulfill(context.Background(), in), "[%s] live fulfill returned error", fx.Name)

			brief := bw.lastBrief
			require.NotNilf(t, brief, "[%s] no brief was persisted", fx.Name)
			assertCommonBriefShape(t, fx, brief)
			if tc.extra != nil {
				tc.extra(t, brief)
			}
		})
	}
}
