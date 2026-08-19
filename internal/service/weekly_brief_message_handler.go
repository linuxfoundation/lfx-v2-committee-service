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
	if generator == nil {
		panic("weeklyBriefMessageHandler: generator is required")
	}
	return &weeklyBriefMessageHandler{
		generator:       generator,
		committeeReader: reader,
	}
}

// newWeeklyBriefHandlerOrNoop returns a real handler when a generator is
// configured, or a no-op handler when the weekly-brief feature is disabled.
// The no-op path is safe because no stream subscription is set up when the
// generator is absent; if a message somehow arrives it is ACKed and discarded.
func newWeeklyBriefHandlerOrNoop(generator GroupWeeklyBriefGenerator, reader CommitteeReader) port.WeeklyBriefGenerateHandler {
	if generator == nil {
		return noopWeeklyBriefHandler{}
	}
	return NewWeeklyBriefMessageHandler(generator, reader)
}

type noopWeeklyBriefHandler struct{}

func (noopWeeklyBriefHandler) HandleGenerateWeeklyBriefRequested(_ context.Context, _ port.StreamMessenger) error {
	return nil
}

func (h *weeklyBriefMessageHandler) HandleGenerateWeeklyBriefRequested(ctx context.Context, msg port.StreamMessenger) error {
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
