// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookSender posts messages to a Slack Incoming Webhook URL.
// The webhookURL is treated as a bearer credential and is never logged.
type WebhookSender struct {
	client *http.Client
}

// NewWebhookSender creates a WebhookSender with the given HTTP client.
// If client is nil a default client with a 10-second timeout is used.
func NewWebhookSender(client *http.Client) *WebhookSender {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &WebhookSender{client: client}
}

type slackPayload struct {
	Text string `json:"text"`
}

// Send POSTs text to the Slack Incoming Webhook URL.
// A non-2xx response is returned as an error carrying the HTTP status and
// Slack's response body — but never the URL itself.
func (s *WebhookSender) Send(ctx context.Context, webhookURL string, text string) error {
	body, err := json.Marshal(slackPayload{Text: text})
	if err != nil {
		return fmt.Errorf("failed to marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack webhook request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a small excerpt of the body for diagnostics without echoing the URL.
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("slack webhook returned %d: %s", resp.StatusCode, string(excerpt))
	}
	return nil
}
