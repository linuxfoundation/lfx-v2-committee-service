// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
)

type inviteSender struct {
	client *NATSClient
}

// SendInvite publishes a send-invite request to the invite service for a user
// who does not yet have an LFID. Fire-and-forget async publish.
func (s *inviteSender) SendInvite(ctx context.Context, req inviteapi.SendInviteRequest) error {
	if s.client == nil || s.client.conn == nil {
		return errors.NewServiceUnavailable("invite sender is not configured", nil)
	}

	if err := ctx.Err(); err != nil {
		return errors.NewUnexpected("context cancelled before publishing invite", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return errors.NewUnexpected("failed to marshal invite request", err)
	}

	if err := s.client.conn.Publish(inviteapi.SendInviteSubject, data); err != nil {
		return errors.NewServiceUnavailable("failed to publish invite request", err)
	}

	return nil
}

// NewInviteSender creates a NATS-backed InviteSender.
func NewInviteSender(client *NATSClient) port.InviteSender {
	return &inviteSender{client: client}
}
