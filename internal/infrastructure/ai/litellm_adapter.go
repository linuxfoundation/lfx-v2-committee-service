// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// LiteLLMConfig is the runtime configuration for the live LiteLLM adapter.
// All fields are sourced from environment variables in providers.go.
type LiteLLMConfig struct {
	BaseURL string        // LITELLM_BASE_URL, e.g. https://litellm.example.com
	APIKey  string        // LITELLM_API_KEY
	Model   string        // LITELLM_MODEL, e.g. "anthropic/claude-sonnet-4"
	Timeout time.Duration // optional, default 60s
}

// LiteLLMAdapter is the live AIAdapter implementation. It performs a minimal
// chat-completions HTTP call against a LiteLLM-compatible endpoint and parses
// the response into a structured WeeklyBrief.
//
// TODO(litellm-client): the current implementation is intentionally minimal —
// it issues a single chat-completions call and expects the model to return
// JSON matching the WeeklyBrief schema. Once we settle on prompt strategy,
// retry policy, and structured-output schema enforcement, this should be
// replaced with a richer client (system prompt, tool/JSON-schema mode,
// streaming, rate-limit handling, observability).
type LiteLLMAdapter struct {
	cfg    LiteLLMConfig
	client *http.Client
}

// NewLiteLLMAdapter constructs a live adapter. It does NOT validate that
// required env vars are present — that is the caller's responsibility (see
// providers.go) so wiring code can decide whether to fail fast.
func NewLiteLLMAdapter(cfg LiteLLMConfig) *LiteLLMAdapter {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &LiteLLMAdapter{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// chatRequest is the minimal OpenAI/LiteLLM-compatible chat-completions payload.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// briefPayload mirrors port.WeeklyBrief for JSON unmarshalling.
type briefPayload struct {
	ClaimIDs   []string         `json:"claim_ids"`
	SourceRefs []port.SourceRef `json:"source_refs"`
	BriefText  string           `json:"brief_text"`
}

// GenerateWeeklyBrief implements port.AIAdapter.
func (a *LiteLLMAdapter) GenerateWeeklyBrief(ctx context.Context, in port.WeeklyBriefInput) (port.WeeklyBrief, error) {
	if a.cfg.BaseURL == "" || a.cfg.APIKey == "" || a.cfg.Model == "" {
		return port.WeeklyBrief{}, fmt.Errorf(
			"litellm adapter: missing required configuration (LITELLM_BASE_URL=%q, LITELLM_API_KEY set=%t, LITELLM_MODEL=%q)",
			a.cfg.BaseURL, a.cfg.APIKey != "", a.cfg.Model,
		)
	}

	prompt := buildPrompt(in)
	body, err := json.Marshal(chatRequest{
		Model: a.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: weeklyBriefSystemPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: marshal request: %w", err)
	}

	url := strings.TrimRight(a.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)

	slog.DebugContext(ctx, "litellm adapter: sending request",
		"url", url, "model", a.cfg.Model, "claims", len(in.Claims),
	)

	resp, err := a.client.Do(req)
	if err != nil {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bound the response body read (1 MiB) so a misbehaving upstream or proxy
	// error page can't return an unbounded payload and exhaust memory.
	const maxResponseBytes = 1 << 20
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: unmarshal chat response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: empty choices in response")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	// Tolerate fenced ```json blocks.
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var bp briefPayload
	if err := json.Unmarshal([]byte(content), &bp); err != nil {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: model returned non-JSON content: %w", err)
	}
	if len(bp.ClaimIDs) == 0 {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: model returned 0 claim_ids")
	}
	if len(bp.SourceRefs) == 0 {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: model returned 0 source_refs")
	}
	if strings.TrimSpace(bp.BriefText) == "" {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: model returned empty brief_text")
	}
	// The port contract (and system prompt) require at least two paragraphs
	// separated by a blank line. Reject schema-invalid briefs here.
	paragraphs := 0
	for _, p := range strings.Split(bp.BriefText, "\n\n") {
		if strings.TrimSpace(p) != "" {
			paragraphs++
		}
	}
	if paragraphs < 2 {
		return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: brief_text must contain at least two paragraphs separated by a blank line")
	}

	return port.WeeklyBrief{
		ClaimIDs:   bp.ClaimIDs,
		SourceRefs: bp.SourceRefs,
		BriefText:  bp.BriefText,
	}, nil
}

const weeklyBriefSystemPrompt = `You are a writing assistant that produces concise weekly briefs for open-source committee working groups.
Respond ONLY with a JSON object matching this schema:
{
  "claim_ids": ["string", ...],   // at least one claim id, taken from the supplied claims
  "source_refs": [{"type": "string", "id": "string"}, ...],  // at least one
  "brief_text": "string"           // two paragraphs separated by a blank line
}
Do not include any prose outside the JSON object.`

func buildPrompt(in port.WeeklyBriefInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Committee: %s (%s)\nProject: %s\nPeriod: %s to %s\n\nClaims:\n",
		in.CommitteeName, in.CommitteeID, in.ProjectName, in.PeriodStart, in.PeriodEnd)

	claims := append([]port.ClaimEvidence(nil), in.Claims...)
	sort.Slice(claims, func(i, j int) bool { return claims[i].ID < claims[j].ID })
	for _, c := range claims {
		fmt.Fprintf(&b, "- id=%s summary=%q sources=", c.ID, c.Summary)
		for i, s := range c.Sources {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "%s:%s", s.Type, s.ID)
		}
		b.WriteString("\n")
	}
	return b.String()
}
