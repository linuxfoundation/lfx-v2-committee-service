// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build live
// +build live

// This file is built only when -tags=live is passed. It documents and
// implements the live-LLM eval path against a LiteLLM-compatible endpoint.
// CI never compiles this file; run it manually pre-release:
//
//	AI_SOURCE=live LITELLM_BASE_URL=... LITELLM_API_KEY=... LITELLM_MODEL=... \
//	  go test ./evals/weekly-brief/... -tags=live -run TestWeeklyBriefEvalLive

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
// against a live LiteLLM endpoint. It skips when any required env var is
// missing so it remains safe to run locally without credentials.
func TestWeeklyBriefEvalLive(t *testing.T) {
	baseURL := os.Getenv("LITELLM_BASE_URL")
	apiKey := os.Getenv("LITELLM_API_KEY")
	modelName := os.Getenv("LITELLM_MODEL")
	if baseURL == "" || apiKey == "" || modelName == "" {
		t.Skip("LITELLM_BASE_URL, LITELLM_API_KEY, LITELLM_MODEL must be set for the live eval; skipping")
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
			g, _ := buildOrchestrator(fx, adapter)

			out, err := g.Generate(context.Background(), service.GroupWeeklyBriefGenerateInput{
				CommitteeUID:  fx.CommitteeUID,
				CommitteeName: fx.CommitteeName,
				ProjectName:   fx.ProjectName,
				Now:           fx.Now,
			})
			require.NoErrorf(t, err, "[%s] live orchestrator returned error", fx.Name)
			require.NotNil(t, out)
			assertCommonBriefShape(t, fx, out.Brief)
			if tc.extra != nil {
				tc.extra(t, out.Brief)
			}
		})
	}
}
