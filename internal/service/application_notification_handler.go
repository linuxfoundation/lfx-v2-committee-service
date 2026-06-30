// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	emailsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service/email"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"golang.org/x/sync/errgroup"
)

// HandleCommitteeApplicationSubmitted handles committee_application.submitted events and notifies
// all LFID writers of the committee that a new application is awaiting review.
// Best-effort: individual send failures are logged but never abort the batch.
func (m *messageHandlerOrchestrator) HandleCommitteeApplicationSubmitted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_application.submitted event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_application.submitted event has unexpected data shape", "error", err)
		return nil, nil
	}

	var application model.CommitteeApplication
	if err := json.Unmarshal(raw, &application); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeApplication from event data", "error", err)
		return nil, nil
	}

	if application.CommitteeUID == "" {
		slog.WarnContext(ctx, "committee_application.submitted event missing committee_uid — discarding")
		return nil, nil
	}

	if application.ApplicantEmail == "" {
		slog.WarnContext(ctx, "committee_application.submitted event missing applicant_email — discarding",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	if m.committeeReader == nil || m.emailSender == nil {
		slog.DebugContext(ctx, "committee reader or email sender not configured — skipping application submitted notification",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	committee, _, err := m.committeeReader.GetBase(ctx, application.CommitteeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load committee for application submitted notification",
			"error", err, "committee_uid", application.CommitteeUID)
		return nil, nil
	}

	writers := m.collectCommitteeWriters(ctx, committee)
	if len(writers) == 0 {
		slog.DebugContext(ctx, "no LFID writers to notify for application submitted",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, application.CommitteeUID)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, w := range writers {
		writer := w
		g.Go(func() error {
			recipientName := writer.name
			if recipientName == "" {
				recipientName = writer.username
			}
			if recipientName == "" {
				recipientName = writer.email
			}

			emailSubject, emailHTML, emailText, renderErr := emailsvc.RenderCommitteeApplicationSubmitted(
				emailsvc.CommitteeApplicationSubmittedData{
					RecipientName:  recipientName,
					ProjectName:    committee.ProjectName,
					CommitteeName:  committee.Name,
					CommitteeURL:   committeeURL,
					ApplicantEmail: application.ApplicantEmail,
					Message:        application.Message,
				},
			)
			if renderErr != nil {
				slog.WarnContext(gctx, "failed to render application submitted notification email",
					"error", renderErr, "committee_uid", application.CommitteeUID)
				return nil
			}

			sendCtx, cancel := context.WithTimeout(gctx, committeeNotificationTimeout)
			defer cancel()
			if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      writer.email,
				Subject: emailSubject,
				HTML:    emailHTML,
				Text:    emailText,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send application submitted notification email",
					"error", sendErr, "committee_uid", application.CommitteeUID,
					"username", redaction.Redact(writer.username))
			} else {
				slog.DebugContext(gctx, "sent application submitted notification email",
					"committee_uid", application.CommitteeUID)
			}
			return nil
		})
	}
	_ = g.Wait()

	return nil, nil
}

// HandleCommitteeApplicationUpdated handles committee_application.updated events and notifies
// the applicant of the outcome (approved or rejected). Other status transitions (e.g. a
// reinstated pending) are silently skipped.
// Best-effort: send failures are logged, not returned.
func (m *messageHandlerOrchestrator) HandleCommitteeApplicationUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_application.updated event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_application.updated event has unexpected data shape", "error", err)
		return nil, nil
	}

	var application model.CommitteeApplication
	if err := json.Unmarshal(raw, &application); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeApplication from event data", "error", err)
		return nil, nil
	}

	if application.CommitteeUID == "" {
		slog.WarnContext(ctx, "committee_application.updated event missing committee_uid — discarding")
		return nil, nil
	}

	// Only notify on terminal decisions; skip reinstated pending applications.
	if application.Status != "approved" && application.Status != "rejected" {
		slog.DebugContext(ctx, "committee_application.updated event status is not a decision — skipping notification",
			"committee_uid", application.CommitteeUID, "status", application.Status)
		return nil, nil
	}

	if application.ApplicantEmail == "" {
		slog.WarnContext(ctx, "committee_application.updated event missing applicant_email — skipping notification",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	if m.committeeReader == nil || m.emailSender == nil {
		slog.DebugContext(ctx, "committee reader or email sender not configured — skipping application updated notification",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	committee, _, err := m.committeeReader.GetBase(ctx, application.CommitteeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load committee for application updated notification",
			"error", err, "committee_uid", application.CommitteeUID)
		return nil, nil
	}

	// Default recipient name to the email address — no auth-service round-trip needed.
	recipientName := application.ApplicantEmail

	var emailSubject, emailHTML, emailText string
	var renderErr error

	switch application.Status {
	case "approved":
		committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, application.CommitteeUID)
		emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeApplicationAccepted(
			emailsvc.CommitteeApplicationAcceptedData{
				RecipientName: recipientName,
				ProjectName:   committee.ProjectName,
				CommitteeName: committee.Name,
				CommitteeURL:  committeeURL,
			},
		)
	case "rejected":
		emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeApplicationRejected(
			emailsvc.CommitteeApplicationRejectedData{
				RecipientName: recipientName,
				ProjectName:   committee.ProjectName,
				CommitteeName: committee.Name,
				ReviewerNotes: application.ReviewerNotes,
			},
		)
	}

	if renderErr != nil {
		slog.WarnContext(ctx, "failed to render application updated notification email",
			"error", renderErr, "committee_uid", application.CommitteeUID, "status", application.Status)
		return nil, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
		To:      application.ApplicantEmail,
		Subject: emailSubject,
		HTML:    emailHTML,
		Text:    emailText,
	}); sendErr != nil {
		slog.WarnContext(ctx, "failed to send application updated notification email",
			"error", sendErr, "committee_uid", application.CommitteeUID, "status", application.Status)
	} else {
		slog.DebugContext(ctx, "sent application updated notification email",
			"committee_uid", application.CommitteeUID, "status", application.Status)
	}

	return nil, nil
}

// collectCommitteeWriters returns a deduplicated list of LFID writers (Username and Email both set)
// for the given committee. If no eligible writers are found on the committee itself, it falls back
// to the project-level settings writers (keyed by committee.ProjectUID) so that someone always has
// visibility into new applications even when the committee has no direct writers configured.
// Non-LFID writers (Username == "") are excluded — they cannot receive direct emails.
func (m *messageHandlerOrchestrator) collectCommitteeWriters(ctx context.Context, committee *model.CommitteeBase) []notificationRecipient {
	seen := make(map[string]struct{})
	var writers []notificationRecipient

	addWriters := func(uid string) {
		settings, _, err := m.committeeReader.GetSettings(ctx, uid)
		if err != nil {
			slog.WarnContext(ctx, "failed to load settings for application submitted notification",
				"error", err, "committee_uid", uid)
			return
		}
		for _, u := range settings.GetWriters() {
			if u.Username == "" || u.Email == "" {
				continue
			}
			if _, ok := seen[u.Username]; ok {
				continue
			}
			seen[u.Username] = struct{}{}
			writers = append(writers, notificationRecipient{
				username: u.Username,
				email:    u.Email,
				name:     u.Name,
			})
		}
	}

	addWriters(committee.UID)

	// Fall back to project writers when the committee has no eligible writers of its own.
	if len(writers) == 0 && committee.ProjectUID != "" && committee.ProjectUID != committee.UID {
		slog.DebugContext(ctx, "no committee writers found — falling back to project writers",
			"committee_uid", committee.UID, "project_uid", committee.ProjectUID)
		addWriters(committee.ProjectUID)
	}

	return writers
}
