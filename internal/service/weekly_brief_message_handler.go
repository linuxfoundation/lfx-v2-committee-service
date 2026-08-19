// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

type weeklyBriefMessageHandler struct {
	generator       GroupWeeklyBriefGenerator
	committeeReader CommitteeReader
}

func NewWeeklyBriefMessageHandler(
	generator GroupWeeklyBriefGenerator,
	reader CommitteeReader,
) port.WeeklyBriefGenerateHandler {
	return &weeklyBriefMessageHandler{
		generator:       generator,
		committeeReader: reader,
	}
}

func (h *weeklyBriefMessageHandler) HandleGenerateWeeklyBriefRequested(ctx context.Context, msg port.StreamMessenger) error {
	if h.generator == nil {
		// A nil generator is a static wiring defect. Return nil (ACK) to avoid an
		// endless NAK/retry loop — retrying will not fix a misconfigured wiring.
		slog.ErrorContext(ctx, "weekly brief generator is not configured — dropping generate-requested event")
		return nil
	}

	subject := msg.Subject()
	if subject != constants.GenerateWeeklyBriefRequestedSubject {
		slog.DebugContext(ctx, "stream message subject not relevant for weekly-brief generate — skipping",
			"subject", subject,
		)
		return nil
	}

	var event GenerateWeeklyBriefRequestedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal GenerateWeeklyBriefRequestedEvent — discarding", "error", err)
		return nil
	}
	if event.CommitteeUID == "" {
		slog.WarnContext(ctx, "generate-requested event missing committee_uid — discarding")
		return nil
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")
	slog.DebugContext(ctx, "fulfilling weekly-brief generation",
		"committee_uid", event.CommitteeUID,
		"force", event.Force,
	)

	membersHidden := true
	if h.committeeReader != nil {
		if settings, _, errSettings := h.committeeReader.GetSettings(ctx, event.CommitteeUID); errSettings == nil && settings != nil {
			membersHidden = settings.MemberVisibility != "basic_profile"
		}
	}

	return h.generator.Fulfill(ctx, GroupWeeklyBriefGenerateInput{
		CommitteeUID:  event.CommitteeUID,
		CommitteeName: event.CommitteeName,
		ProjectName:   event.ProjectName,
		Force:         event.Force,
		Now:           event.RequestedAt,
		MembersHidden: membersHidden,
	})
}
