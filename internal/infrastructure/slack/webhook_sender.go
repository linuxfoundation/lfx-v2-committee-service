// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// allowedHosts is the set of hosts permitted as Slack Incoming Webhook targets.
// Restricting to hooks.slack.com prevents SSRF via an attacker-controlled URL.
var allowedHosts = map[string]bool{
	"hooks.slack.com": true,
}

// WebhookSender posts messages to a Slack Incoming Webhook URL.
// The webhookURL is treated as a bearer credential and is never logged.
type WebhookSender struct {
	client *http.Client
}

// NewWebhookSender creates a WebhookSender with the given HTTP client.
// If client is nil a default client with a 10-second timeout and redirect
// blocking is used.
func NewWebhookSender(client *http.Client) *WebhookSender {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			// Block redirects: a redirect from an approved host to an
			// unapproved one would bypass the host allowlist check.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &WebhookSender{client: client}
}

type slackPayload struct {
	Text string `json:"text"`
}

// Send POSTs text to the Slack Incoming Webhook URL.
// Returns an error if the host is not in the approved allowlist, or if the
// request fails. Errors never contain the webhook URL.
func (s *WebhookSender) Send(ctx context.Context, webhookURL string, text string) error {
	parsed, err := url.Parse(webhookURL)
	if err != nil || !allowedHosts[parsed.Hostname()] {
		return fmt.Errorf("slack webhook host is not in the approved allowlist")
	}

	body, err := json.Marshal(slackPayload{Text: text})
	if err != nil {
		return fmt.Errorf("failed to marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		// NewRequestWithContext errors do not include the URL but sanitize anyway.
		return fmt.Errorf("failed to build slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// *url.Error embeds the full URL in its string — strip it.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("slack webhook request failed: %w", urlErr.Err)
		}
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
