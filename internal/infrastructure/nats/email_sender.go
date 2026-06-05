// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

type emailSender struct {
	client *NATSClient
}

// SendEmail sends an email via the email service using NATS request/reply with trace context propagation.
func (e *emailSender) SendEmail(ctx context.Context, req emailapi.SendEmailRequest) error {
	if e.client == nil || e.client.conn == nil {
		return errors.NewServiceUnavailable("email sender is not configured", nil)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return errors.NewUnexpected("failed to marshal email request", err)
	}

	msg, err := e.client.requestWithSpan(ctx, emailapi.SendEmailSubject, data)
	if err != nil {
		return errors.NewServiceUnavailable("email service unavailable", err)
	}

	if len(msg.Data) == 0 {
		return nil
	}

	var errResp emailapi.SendEmailErrorResponse
	if jsonErr := json.Unmarshal(msg.Data, &errResp); jsonErr == nil && errResp.Error != "" {
		return errors.NewUnexpected("email service error: "+errResp.Error, nil)
	}

	return nil
}

// NewEmailSender creates a NATS-backed EmailSender.
func NewEmailSender(client *NATSClient) port.EmailSender {
	return &emailSender{client: client}
}
