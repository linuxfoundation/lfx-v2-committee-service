// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service/email"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"golang.org/x/sync/errgroup"
)

const committeeEmailSendTimeout = 5 * time.Second

// HandleCommitteeMemberCreated handles committee_member.created events and sends
// a notification email to the newly added member. Errors from individual sends
// are logged but never returned — the handler is best-effort.
func (m *messageHandlerOrchestrator) HandleCommitteeMemberCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "committee_subscriber: failed to unmarshal committee_member.created event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_subscriber: committee_member.created event has unexpected data shape", "error", err)
		return nil, nil
	}

	var member model.CommitteeMember
	if err := json.Unmarshal(raw, &member); err != nil {
		slog.WarnContext(ctx, "committee_subscriber: cannot decode CommitteeMember from event data", "error", err)
		return nil, nil
	}

	if member.Email == "" {
		slog.WarnContext(ctx, "committee_subscriber: skipping notification — member has no email address",
			"committee_uid", member.CommitteeUID, "username", member.Username)
		return nil, nil
	}

	if m.emailSender == nil {
		slog.WarnContext(ctx, "committee_subscriber: email sender not configured — skipping notification")
		return nil, nil
	}

	recipientName := strings.TrimSpace(member.FirstName + " " + member.LastName)
	if recipientName == "" {
		recipientName = member.Username
	}
	if recipientName == "" {
		recipientName = member.Email
	}

	inviterName := "A committee administrator"

	roleDisplay := member.Role.Name
	if roleDisplay == "" {
		roleDisplay = "Member"
	}

	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, member.ProjectSlug)

	subject, html, text, err := email.RenderCommitteeRoleNotification(email.CommitteeRoleNotificationData{
		RecipientName: recipientName,
		CommitteeName: member.CommitteeName,
		Role:          roleDisplay,
		CommitteeURL:  committeeURL,
		InviterName:   inviterName,
	})
	if err != nil {
		slog.WarnContext(ctx, "committee_subscriber: failed to render email template",
			"error", err, "committee_uid", member.CommitteeUID)
		return nil, nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)
	g.Go(func() error {
		sendCtx, cancel := context.WithTimeout(gctx, committeeEmailSendTimeout)
		defer cancel()
		sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
			To:      member.Email,
			Subject: subject,
			HTML:    html,
			Text:    text,
		})
		if sendErr != nil {
			slog.WarnContext(gctx, "committee_subscriber: failed to send member notification email",
				"error", sendErr, "committee_uid", member.CommitteeUID, "to", member.Email)
		} else {
			slog.DebugContext(gctx, "committee_subscriber: sent member notification email",
				"committee_uid", member.CommitteeUID, "to", member.Email)
		}
		return nil
	})
	_ = g.Wait()

	return nil, nil
}

// buildCommitteeURL returns the deep link to the committee's project page.
// Uses the project slug when available; falls back to the generic projects overview.
func buildCommitteeURL(baseURL, projectSlug string) string {
	base := strings.TrimRight(baseURL, "/")
	if projectSlug != "" {
		return base + "/projects/" + projectSlug + "/committees"
	}
	return base + "/projects/overview"
}
