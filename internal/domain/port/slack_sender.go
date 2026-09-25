// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// SlackSender posts a message to a Slack Incoming Webhook URL.
// Implementations must never log the webhookURL — it is a bearer credential.
type SlackSender interface {
	Send(ctx context.Context, webhookURL string, text string) error
}
