// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
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
// Returns a typed error from pkg/errors; raw Slack response details are
// logged server-side and never forwarded to callers.
func (s *WebhookSender) Send(ctx context.Context, webhookURL string, text string) error {
	parsed, err := url.Parse(webhookURL)
	if err != nil || parsed.Scheme != "https" || !allowedHosts[parsed.Hostname()] {
		return errors.NewUnexpected("slack webhook URL failed allowlist check", nil)
	}

	body, err := json.Marshal(slackPayload{Text: text})
	if err != nil {
		return errors.NewUnexpected("failed to marshal slack payload", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return errors.NewUnexpected("failed to build slack request", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// *url.Error embeds the full URL — strip it before wrapping.
		var urlErr *url.Error
		if stderrors.As(err, &urlErr) {
			return errors.NewServiceUnavailable("slack webhook unreachable", urlErr.Err)
		}
		return errors.NewServiceUnavailable("slack webhook request failed", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		// Log details server-side; return a stable typed error to callers.
		slog.WarnContext(ctx, "slack webhook returned non-2xx",
			"status", resp.StatusCode,
			"response_excerpt", string(excerpt),
		)
		return errors.NewServiceUnavailable("slack webhook returned a non-2xx response", nil)
	}
	return nil
}
