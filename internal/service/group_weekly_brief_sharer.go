// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// shareableStates are the brief states that carry saved brief_text worth sending.
var shareableStates = map[model.GroupWeeklyBriefState]bool{
	model.GroupWeeklyBriefStateGenerated: true,
	model.GroupWeeklyBriefStateEdited:    true,
	model.GroupWeeklyBriefStateApproved:  true,
}

// GroupWeeklyBriefShareInput carries the caller-supplied parameters for a
// share-to-chat request.
type GroupWeeklyBriefShareInput struct {
	CommitteeUID string
	// Revision is the brief's optimistic-concurrency token, echoed from GET
	// /current. A mismatch returns errors.RevisionMismatch (409).
	Revision uint64
	// Now is used to compute the current weekly window. Zero → time.Now().UTC().
	Now time.Time
}

// GroupWeeklyBriefDataSharer is the use-case façade for share-to-chat.
type GroupWeeklyBriefDataSharer interface {
	// ShareToChat posts the current weekly brief to the committee's configured
	// Slack Incoming Webhook URL.
	//
	// Preconditions (checked in order):
	//   - committee_uid is non-empty                     → errors.Validation
	//   - brief exists for the current window            → errors.NotFound
	//   - brief state is generated, edited, or approved  → errors.Validation
	//   - revision matches the stored brief              → errors.RevisionMismatch
	//   - chat_webhook_url is configured in settings     → errors.NoChatWebhook
	ShareToChat(ctx context.Context, in GroupWeeklyBriefShareInput) error
}

type groupWeeklyBriefSharerOrchestrator struct {
	briefReader    port.GroupWeeklyBriefReader
	settingsReader CommitteeReader
	slackSender    port.SlackSender
}

// GroupWeeklyBriefSharerOption configures the sharer orchestrator.
type GroupWeeklyBriefSharerOption func(*groupWeeklyBriefSharerOrchestrator)

// WithGroupWeeklyBriefReaderForSharer injects the brief reader.
func WithGroupWeeklyBriefReaderForSharer(r port.GroupWeeklyBriefReader) GroupWeeklyBriefSharerOption {
	return func(o *groupWeeklyBriefSharerOrchestrator) { o.briefReader = r }
}

// WithCommitteeSettingsReaderForSharer injects the settings reader used to
// retrieve the stored chat_webhook_url.
func WithCommitteeSettingsReaderForSharer(r CommitteeReader) GroupWeeklyBriefSharerOption {
	return func(o *groupWeeklyBriefSharerOrchestrator) { o.settingsReader = r }
}

// WithSlackSenderForSharer injects the Slack webhook sender.
func WithSlackSenderForSharer(s port.SlackSender) GroupWeeklyBriefSharerOption {
	return func(o *groupWeeklyBriefSharerOrchestrator) { o.slackSender = s }
}

// NewGroupWeeklyBriefSharerOrchestrator builds a GroupWeeklyBriefDataSharer.
func NewGroupWeeklyBriefSharerOrchestrator(opts ...GroupWeeklyBriefSharerOption) GroupWeeklyBriefDataSharer {
	o := &groupWeeklyBriefSharerOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.briefReader == nil {
		panic("group weekly brief reader is required")
	}
	if o.settingsReader == nil {
		panic("committee settings reader is required")
	}
	if o.slackSender == nil {
		panic("slack sender is required")
	}
	return o
}

func (o *groupWeeklyBriefSharerOrchestrator) ShareToChat(ctx context.Context, in GroupWeeklyBriefShareInput) error {
	if in.CommitteeUID == "" {
		return errors.NewValidation("committee_uid is required")
	}
	if in.Revision == 0 {
		return errors.NewValidation("revision must be >= 1")
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}

	// Load the brief for the current window.
	start, end := model.WeeklyWindow(in.Now)
	window := model.GroupWeeklyBrief{WindowStart: start, WindowEnd: end}
	brief, _, err := o.briefReader.GetGroupWeeklyBriefForWindow(ctx, in.CommitteeUID, window)
	if err != nil {
		return err
	}
	if brief == nil {
		return errors.NewNotFound("no weekly brief exists for the current window")
	}

	// Validate state before the revision check so the error is actionable.
	if !shareableStates[brief.State] {
		return errors.NewValidation("brief is not in a shareable state (must be generated, edited, or approved)")
	}

	if brief.Revision != in.Revision {
		return errors.NewRevisionMismatch(brief.Revision)
	}

	// Read committee settings to get the stored webhook URL.
	// The URL is treated as a bearer credential — never log it.
	settings, _, err := o.settingsReader.GetSettings(ctx, in.CommitteeUID)
	if err != nil {
		return err
	}
	if settings == nil || settings.ChatWebhookURL == nil || *settings.ChatWebhookURL == "" {
		return errors.NewNoChatWebhook()
	}

	return o.slackSender.Send(ctx, *settings.ChatWebhookURL, brief.BriefText)
}
