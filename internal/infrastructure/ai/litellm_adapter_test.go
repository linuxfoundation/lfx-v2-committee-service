// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// roundTripFunc adapts a function to http.RoundTripper so tests can serve canned
// chat-completions responses without any network I/O.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newTestAdapter builds a LiteLLMAdapter whose HTTP client is backed by the
// given round tripper and whose retry backoff is disabled (no wall-clock waits).
// The round tripper receives the decoded request body for each attempt and
// returns the assistant `content` to embed in a well-formed chat response.
func newTestAdapter(t *testing.T, reply func(attempt int, req chatRequest) string) *LiteLLMAdapter {
	t.Helper()
	a := NewLiteLLMAdapter(LiteLLMConfig{
		BaseURL: "https://litellm.test.invalid",
		APIKey:  "test-key",
		Model:   "claude-sonnet-4-6",
	})
	a.backoff = 0
	a.sleep = func(time.Duration) {}

	attempt := 0
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt++
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var cr chatRequest
		if err := json.Unmarshal(bodyBytes, &cr); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		content := reply(attempt, cr)
		respJSON := mustChatResponseJSON(t, content)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(respJSON)),
			Header:     make(http.Header),
		}, nil
	})}
	return a
}

// mustChatResponseJSON renders a minimal valid chat-completions response whose
// first choice carries the given assistant content.
func mustChatResponseJSON(t *testing.T, content string) string {
	t.Helper()
	payload := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{"content": content},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal chat response: %v", err)
	}
	return string(b)
}

// validBriefJSON is a schema-valid weekly-brief JSON object the adapter accepts.
const validBriefJSON = `{
  "claim_ids": ["meeting-m-301"],
  "source_refs": [{"type": "meeting", "id": "m-301"}],
  "brief_text": "Working Group Beta held its weekly sync this period.\n\nNo other activity was recorded, so this brief is intentionally concise."
}`

func liveTestInput() port.WeeklyBriefInput {
	return port.WeeklyBriefInput{
		CommitteeID:   "c-low-data",
		CommitteeName: "Working Group Beta",
		ProjectName:   "LFX Demo Project",
		PeriodStart:   "2026-05-10T00:00:00Z",
		PeriodEnd:     "2026-05-16T23:59:59Z",
		Claims: []port.ClaimEvidence{
			{ID: "meeting-m-301", Summary: "Weekly sync", Sources: []port.SourceRef{{Type: "meeting", ID: "m-301"}}},
		},
	}
}

// TestLiteLLMAdapter_RequestUsesStructuredOutput asserts the outgoing request
// pins temperature 0 and forces the weekly-brief tool call — the load-bearing
// fix for the flaky prose / empty-`{}` reply (LFXV2-2134).
func TestLiteLLMAdapter_RequestUsesStructuredOutput(t *testing.T) {
	t.Parallel()
	var seen chatRequest
	a := newTestAdapter(t, func(_ int, req chatRequest) string {
		seen = req
		return validBriefJSON
	})

	if _, err := a.GenerateWeeklyBrief(context.Background(), liveTestInput()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen.Temperature != 0 {
		t.Errorf("temperature: got %v, want 0", seen.Temperature)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Function.Name != weeklyBriefToolName {
		t.Fatalf("tools: got %+v, want one %q function", seen.Tools, weeklyBriefToolName)
	}
	if seen.Tools[0].Type != "function" {
		t.Errorf("tool type: got %q, want function", seen.Tools[0].Type)
	}
	// tool_choice must force the function so the model can't reply with prose.
	tc, ok := seen.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice: got %T, want forced-function object", seen.ToolChoice)
	}
	fn, _ := tc["function"].(map[string]any)
	if tc["type"] != "function" || fn["name"] != weeklyBriefToolName {
		t.Errorf("tool_choice: got %+v, want forced %q", tc, weeklyBriefToolName)
	}
}

// TestLiteLLMAdapter_ToolCallArgumentsRecovers asserts the adapter reads the
// structured brief from the forced tool call's arguments (the live path), not
// just from message content.
func TestLiteLLMAdapter_ToolCallArgumentsRecovers(t *testing.T) {
	t.Parallel()
	a := NewLiteLLMAdapter(LiteLLMConfig{BaseURL: "https://litellm.test.invalid", APIKey: "k", Model: "m"})
	a.backoff = 0
	a.sleep = func(time.Duration) {}
	a.client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mustChatResponseWithToolCall(t, validBriefJSON))),
			Header:     make(http.Header),
		}, nil
	})}

	brief, err := a.GenerateWeeklyBrief(context.Background(), liveTestInput())
	if err != nil {
		t.Fatalf("expected tool-call arguments to recover, got error: %v", err)
	}
	if strings.TrimSpace(brief.BriefText) == "" {
		t.Fatalf("brief_text must be non-empty")
	}
	if len(brief.ClaimIDs) == 0 {
		t.Fatalf("claim_ids must be populated from tool arguments")
	}
}

// mustChatResponseWithToolCall renders a chat response whose first choice carries
// a single forced tool call with the given JSON arguments.
func mustChatResponseWithToolCall(t *testing.T, arguments string) string {
	t.Helper()
	payload := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": "",
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"name":      weeklyBriefToolName,
								"arguments": arguments,
							},
						},
					},
				},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal tool-call response: %v", err)
	}
	return string(b)
}

// TestLiteLLMAdapter_FencedJSONRecovers asserts a reply wrapped in a ```json
// code fence is parsed into a non-empty brief.
func TestLiteLLMAdapter_FencedJSONRecovers(t *testing.T) {
	t.Parallel()
	a := newTestAdapter(t, func(_ int, _ chatRequest) string {
		return "```json\n" + validBriefJSON + "\n```"
	})

	brief, err := a.GenerateWeeklyBrief(context.Background(), liveTestInput())
	if err != nil {
		t.Fatalf("expected fenced JSON to recover, got error: %v", err)
	}
	if strings.TrimSpace(brief.BriefText) == "" {
		t.Fatalf("brief_text must be non-empty")
	}
}

// TestLiteLLMAdapter_ProseWrappedJSONRecovers reproduces the LFXV2-2134 failure
// shape (a reply that begins with prose, e.g. 'I'), and asserts the embedded
// JSON object is extracted into a non-empty brief on the first attempt.
func TestLiteLLMAdapter_ProseWrappedJSONRecovers(t *testing.T) {
	t.Parallel()
	attempts := 0
	a := newTestAdapter(t, func(n int, _ chatRequest) string {
		attempts = n
		return "I'll summarize the week below.\n\n" + validBriefJSON + "\n\nLet me know if you need changes."
	})

	brief, err := a.GenerateWeeklyBrief(context.Background(), liveTestInput())
	if err != nil {
		t.Fatalf("expected prose-wrapped JSON to recover, got error: %v", err)
	}
	if strings.TrimSpace(brief.BriefText) == "" {
		t.Fatalf("brief_text must be non-empty")
	}
	if attempts != 1 {
		t.Errorf("prose-wrapped JSON should recover on first attempt, took %d", attempts)
	}
}

// TestLiteLLMAdapter_RetryRecovers asserts the bounded retry recovers when the
// first attempt is pure prose and the second is valid JSON, and that the retry
// carries a corrective user message.
func TestLiteLLMAdapter_RetryRecovers(t *testing.T) {
	t.Parallel()
	var secondReq chatRequest
	a := newTestAdapter(t, func(attempt int, req chatRequest) string {
		if attempt == 1 {
			return "I cannot produce JSON right now, here is a prose summary instead."
		}
		secondReq = req
		return validBriefJSON
	})

	brief, err := a.GenerateWeeklyBrief(context.Background(), liveTestInput())
	if err != nil {
		t.Fatalf("expected retry to recover, got error: %v", err)
	}
	if strings.TrimSpace(brief.BriefText) == "" {
		t.Fatalf("brief_text must be non-empty")
	}
	// The retry must include the failed assistant turn + a corrective nudge.
	last := secondReq.Messages[len(secondReq.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "ONLY the JSON object") {
		t.Errorf("retry missing corrective message; last message = %+v", last)
	}
}

// TestLiteLLMAdapter_PureProseExhaustsRetries asserts that when every attempt is
// non-JSON, the adapter surfaces a precise error after exhausting retries — the
// release gate stays meaningful rather than masking a real failure.
func TestLiteLLMAdapter_PureProseExhaustsRetries(t *testing.T) {
	t.Parallel()
	attempts := 0
	a := newTestAdapter(t, func(n int, _ chatRequest) string {
		attempts = n
		return "I'm unable to comply and will only ever return prose."
	})

	_, err := a.GenerateWeeklyBrief(context.Background(), liveTestInput())
	if err == nil {
		t.Fatalf("expected error after retries are exhausted")
	}
	if attempts != defaultMaxAttempts {
		t.Errorf("expected %d attempts, got %d", defaultMaxAttempts, attempts)
	}
	if !strings.Contains(err.Error(), "non-JSON") {
		t.Errorf("error should describe the non-JSON cause, got: %v", err)
	}
}

// TestExtractJSONObject covers the extraction fallback directly.
func TestExtractJSONObject(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"bare", `{"a":1}`, `{"a":1}`, true},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`, true},
		{"fenced-bare", "```\n{\"a\":1}\n```", `{"a":1}`, true},
		{"prose-prefix", `Sure! {"a":1}`, `{"a":1}`, true},
		{"prose-suffix", `{"a":1} hope that helps`, `{"a":1}`, true},
		{"nested-and-string-braces", `prefix {"a":{"b":"}{"},"c":2} suffix`, `{"a":{"b":"}{"},"c":2}`, true},
		{"no-object", `I cannot help with that.`, "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractJSONObject(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("extractJSONObject(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}
