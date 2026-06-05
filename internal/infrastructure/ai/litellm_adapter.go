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

// Robustness defaults for the live adapter. The model occasionally replies with
// prose or fenced JSON instead of a bare JSON object (see LFXV2-2134); these
// constants drive the bounded-retry policy that makes generation deterministic.
const (
	// defaultMaxAttempts is the total number of chat-completions attempts
	// (1 initial + retries) before giving up with a precise error.
	defaultMaxAttempts = 3
	// defaultRetryBackoff is the fixed, deterministic pause between attempts.
	defaultRetryBackoff = 500 * time.Millisecond
	// generationTemperature pins the call to deterministic decoding so a given
	// input maps to a stable output and the release-gate eval is not flaky.
	generationTemperature = 0.0
)

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

	// Retry policy. Set by NewLiteLLMAdapter; overridable by tests (same package)
	// to drive the retry path without real backoff delays.
	maxAttempts int
	backoff     time.Duration
	// sleep is the pause primitive between attempts; injectable so tests don't
	// wait on the wall clock. Defaults to time.Sleep.
	sleep func(time.Duration)
}

// NewLiteLLMAdapter constructs a live adapter. It does NOT validate that
// required env vars are present — that is the caller's responsibility (see
// providers.go) so wiring code can decide whether to fail fast.
func NewLiteLLMAdapter(cfg LiteLLMConfig) *LiteLLMAdapter {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &LiteLLMAdapter{
		cfg:         cfg,
		client:      &http.Client{Timeout: cfg.Timeout},
		maxAttempts: defaultMaxAttempts,
		backoff:     defaultRetryBackoff,
		sleep:       time.Sleep,
	}
}

// chatRequest is the OpenAI/LiteLLM-compatible chat-completions payload. We pin
// temperature to 0 and force a tool call so the model returns a schema-shaped
// JSON object via tool arguments rather than prose or an empty `{}`; see
// LFXV2-2134. Plain `response_format: json_object` is NOT used here: against this
// Anthropic-via-LiteLLM endpoint it has no schema to satisfy and the model
// trivially returns `{}`. Forced tool calling with required fields is the
// reliable structured-output mechanism for Claude.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Tools       []tool        `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
}

// tool is one OpenAI/LiteLLM-compatible function tool the model may call.
type tool struct {
	Type     string       `json:"type"` // always "function"
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// weeklyBriefToolName is the function the model is forced to call. Its arguments
// are the JSON object we parse into a brief.
const weeklyBriefToolName = "emit_weekly_brief"

// weeklyBriefToolParameters is the JSON-schema for the tool arguments. Required
// fields force the model to populate the brief rather than emitting an empty
// object; minItems biases it toward grounding on at least one claim/source.
const weeklyBriefToolParameters = `{
  "type": "object",
  "properties": {
    "claim_ids": {
      "type": "array",
      "items": {"type": "string"},
      "minItems": 1,
      "description": "IDs of the supplied claims this brief grounds on (at least one)."
    },
    "source_refs": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {"type": {"type": "string"}, "id": {"type": "string"}},
        "required": ["type", "id"]
      },
      "minItems": 1,
      "description": "Evidence references backing the brief (at least one)."
    },
    "brief_text": {
      "type": "string",
      "description": "The human-readable brief: two paragraphs separated by a blank line."
    }
  },
  "required": ["claim_ids", "source_refs", "brief_text"]
}`

// weeklyBriefTools is the forced tool set sent on every request.
func weeklyBriefTools() []tool {
	return []tool{{
		Type: "function",
		Function: toolFunction{
			Name:        weeklyBriefToolName,
			Description: "Emit the structured weekly brief. You MUST call this function.",
			Parameters:  json.RawMessage(weeklyBriefToolParameters),
		},
	}}
}

// forceWeeklyBriefTool is the tool_choice value that forces the model to call
// weeklyBriefToolName rather than replying with free-form content.
func forceWeeklyBriefTool() any {
	return map[string]any{
		"type":     "function",
		"function": map[string]any{"name": weeklyBriefToolName},
	}
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

	// Conversation seeded with the system + user prompt. On a retry we append a
	// corrective turn (see below) so the next attempt is nudged back to valid
	// JSON even under deterministic (temperature 0) decoding, where a plain
	// re-send would otherwise reproduce the same malformed reply verbatim.
	messages := []chatMessage{
		{Role: "system", Content: weeklyBriefSystemPrompt},
		{Role: "user", Content: buildPrompt(in)},
	}

	attempts := a.maxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if a.backoff > 0 && a.sleep != nil {
				a.sleep(a.backoff)
			}
			slog.WarnContext(ctx, "litellm adapter: retrying after failed attempt",
				"attempt", attempt, "max_attempts", attempts, "prev_error", lastErr.Error(),
			)
		}

		brief, raw, err := a.attemptGenerate(ctx, messages, len(in.Claims))
		if err == nil {
			return brief, nil
		}
		lastErr = err

		// Only append a corrective turn when the model actually produced content
		// to correct (raw != ""). Transport/HTTP/envelope failures (raw == "")
		// still get a bounded retry for transient resilience, but nudging the
		// model makes no sense there — there is no malformed reply to fix, and
		// the error text (e.g. an HTTP proxy body) must not be fed into the
		// prompt. We feed back a length-capped copy of the reply plus a stable,
		// generic instruction (no interpolated error string).
		if attempt < attempts && raw != "" {
			messages = append(messages,
				chatMessage{Role: "assistant", Content: truncateForPrompt(raw)},
				chatMessage{Role: "user", Content: correctiveNudge},
			)
		}
	}

	return port.WeeklyBrief{}, fmt.Errorf("litellm adapter: generation failed after %d attempts: %w", attempts, lastErr)
}

// correctiveNudge is the stable retry instruction appended after a malformed
// model reply. It deliberately embeds no error detail so untrusted upstream
// text can never reach the prompt (prompt-injection / data-leak guard).
const correctiveNudge = "Your previous response was not a valid weekly-brief JSON object. " +
	"Respond with ONLY the JSON object matching the schema — no prose, no code fences."

// maxPromptFeedbackBytes caps how much of a malformed reply we feed back on a
// retry: enough for the model to self-correct, but far below the 1 MiB response
// bound so a large reply can't balloon the next request.
const maxPromptFeedbackBytes = 2000

// truncateForPrompt returns s capped to maxPromptFeedbackBytes runes, with an
// elision marker when truncated.
func truncateForPrompt(s string) string {
	r := []rune(s)
	if len(r) <= maxPromptFeedbackBytes {
		return s
	}
	return string(r[:maxPromptFeedbackBytes]) + " …[truncated]"
}

// maxErrorBodyBytes caps how much of an upstream non-2xx body we keep in the
// returned error, which is also surfaced in retry logs/telemetry.
const maxErrorBodyBytes = 512

// truncateForError trims surrounding whitespace and caps an upstream error body
// to maxErrorBodyBytes runes so a large proxy/HTML page can't bloat errors/logs.
func truncateForError(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxErrorBodyBytes {
		return s
	}
	return string(r[:maxErrorBodyBytes]) + " …[truncated]"
}

// attemptGenerate performs a single chat-completions call and parses the result.
// It returns the parsed brief on success, plus the raw model content (for retry
// context) and an error describing why this attempt failed.
func (a *LiteLLMAdapter) attemptGenerate(ctx context.Context, messages []chatMessage, claimCount int) (port.WeeklyBrief, string, error) {
	body, err := json.Marshal(chatRequest{
		Model:       a.cfg.Model,
		Messages:    messages,
		Temperature: generationTemperature,
		Tools:       weeklyBriefTools(),
		ToolChoice:  forceWeeklyBriefTool(),
	})
	if err != nil {
		return port.WeeklyBrief{}, "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(a.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return port.WeeklyBrief{}, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)

	slog.DebugContext(ctx, "litellm adapter: sending request",
		"url", url, "model", a.cfg.Model, "claims", claimCount,
	)

	resp, err := a.client.Do(req)
	if err != nil {
		return port.WeeklyBrief{}, "", fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bound the response body read (1 MiB) so a misbehaving upstream or proxy
	// error page can't return an unbounded payload and exhaust memory.
	const maxResponseBytes = 1 << 20
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return port.WeeklyBrief{}, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body may be a large HTML/proxy error page; truncate + trim it so it
		// doesn't bloat errors or logs (this error is also surfaced via prev_error
		// on retry) or expose more than needed.
		return port.WeeklyBrief{}, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateForError(string(respBody)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return port.WeeklyBrief{}, "", fmt.Errorf("unmarshal chat response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return port.WeeklyBrief{}, "", fmt.Errorf("empty choices in response")
	}

	// Prefer the forced tool call's arguments (already a JSON object). Select the
	// call that matches our tool by name with non-empty arguments — never blindly
	// the first call — so an unexpected/extra tool call can't be parsed as the
	// brief. Fall back to message content for replies that ignore the tool
	// directive.
	msg := parsed.Choices[0].Message
	raw := msg.Content
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name == weeklyBriefToolName && strings.TrimSpace(tc.Function.Arguments) != "" {
			raw = tc.Function.Arguments
			break
		}
	}
	content, ok := extractJSONObject(raw)
	if !ok {
		return port.WeeklyBrief{}, raw, fmt.Errorf("model returned non-JSON content")
	}

	var bp briefPayload
	if err := json.Unmarshal([]byte(content), &bp); err != nil {
		return port.WeeklyBrief{}, raw, fmt.Errorf("model returned non-JSON content: %w", err)
	}
	if len(bp.ClaimIDs) == 0 {
		return port.WeeklyBrief{}, raw, fmt.Errorf("model returned 0 claim_ids")
	}
	if len(bp.SourceRefs) == 0 {
		return port.WeeklyBrief{}, raw, fmt.Errorf("model returned 0 source_refs")
	}
	if strings.TrimSpace(bp.BriefText) == "" {
		return port.WeeklyBrief{}, raw, fmt.Errorf("model returned empty brief_text")
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
		return port.WeeklyBrief{}, raw, fmt.Errorf("brief_text must contain at least two paragraphs separated by a blank line")
	}

	return port.WeeklyBrief{
		ClaimIDs:   bp.ClaimIDs,
		SourceRefs: bp.SourceRefs,
		BriefText:  bp.BriefText,
	}, raw, nil
}

// extractJSONObject recovers a JSON object from a model reply that may be
// wrapped in prose and/or Markdown code fences. It scans for the first balanced,
// brace-matched top-level object (honoring strings/escapes so braces inside
// string values do not throw off the count). This transparently handles a bare
// object, leading prose, trailing prose, and ```json fences — the scanner starts
// at the first '{' and stops at its matching '}', so surrounding fences/prose
// (including a ``` that appears inside brief_text) are ignored. Returns the
// candidate JSON and whether one was found.
func extractJSONObject(content string) (string, bool) {
	s := strings.TrimSpace(content)

	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// other chars inside a string are ignored
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
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
