// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	emailsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service/email"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
	"golang.org/x/sync/errgroup"
)

const committeeNotificationTimeout = 5 * time.Second

type committeeNotificationHandler struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	committeePublisher          port.CommitteePublisher
	emailSender                 port.EmailSender
	inviteSender                port.InviteSender
	userReader                  port.UserReader
	linkReader                  port.CommitteeLinkReader
	lfxSelfServeBaseURL         string
	projectReader               port.ProjectReader
}

func NewCommitteeNotificationHandler(
	reader CommitteeReader,
	writerOrch CommitteeWriter,
	publisher port.CommitteePublisher,
	emailSender port.EmailSender,
	inviteSender port.InviteSender,
	userReader port.UserReader,
	linkReader port.CommitteeLinkReader,
	baseURL string,
	projectReader port.ProjectReader,
) port.CommitteeNotificationHandler {
	return &committeeNotificationHandler{
		committeeReader:             reader,
		committeeWriterOrchestrator: writerOrch,
		committeePublisher:          publisher,
		emailSender:                 emailSender,
		inviteSender:                inviteSender,
		userReader:                  userReader,
		linkReader:                  linkReader,
		lfxSelfServeBaseURL:         baseURL,
		projectReader:               projectReader,
	}
}

// HandleCommitteeMemberCreated handles committee_member.created events and notifies
// the newly added member. Users with an LFID (Username present) receive a direct
// notification email. Users without an LFID receive an invite via the invite service.
// Best-effort: send errors are logged, not returned.
func (h *committeeNotificationHandler) HandleCommitteeMemberCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_member.created event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_member.created event has unexpected data shape", "error", err)
		return nil, nil
	}

	// Decode the created-event payload once into the typed wrapper so the
	// request-scoped skip_notification flag is parsed alongside the member. On a
	// malformed payload we fail safe by suppressing the notification rather than
	// defaulting to send.
	var created model.CommitteeMemberCreatedEventData
	if err := json.Unmarshal(raw, &created); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeMemberCreatedEventData from event data — suppressing notification", "error", err)
		return nil, nil
	}
	if created.CommitteeMember == nil {
		slog.WarnContext(ctx, "committee_member.created event missing member payload — suppressing notification")
		return nil, nil
	}
	member := *created.CommitteeMember

	// Request-scoped opt-out: when the member was added with skip_notification set,
	// suppress both the invite and the direct notification email.
	if created.SkipNotification {
		slog.DebugContext(ctx, "skipping member notification — skip_notification flag set",
			"committee_uid", member.CommitteeUID)
		return nil, nil
	}

	if member.Email == "" {
		slog.WarnContext(ctx, "skipping member notification — no email address",
			"committee_uid", member.CommitteeUID, "username", redaction.Redact(member.Username))
		return nil, nil
	}

	recipientName := strings.TrimSpace(member.FirstName + " " + member.LastName)
	if recipientName == "" {
		recipientName = member.Username
	}
	if recipientName == "" {
		recipientName = member.Email
	}

	committeeURL := buildCommitteeURL(h.lfxSelfServeBaseURL, member.CommitteeUID)

	if member.Username == "" {
		// No LFID — route through the invite service so the user must create an
		// account before gaining committee access.
		_ = h.sendMemberInvite(ctx, &member, recipientName, committeeURL)
		return nil, nil
	}

	// LFID present — send a direct notification email.
	if h.emailSender == nil {
		slog.DebugContext(ctx, "email sender not configured — skipping member notification")
		return nil, nil
	}

	subject, html, text, err := emailsvc.RenderCommitteeRoleNotification(emailsvc.CommitteeRoleNotificationData{
		RecipientName: recipientName,
		CommitteeName: member.CommitteeName,
		Role:          "Member",
		CommitteeURL:  committeeURL,
		InviterName:   "A committee administrator",
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to render member notification email template",
			"error", err, "committee_uid", member.CommitteeUID)
		return nil, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	if sendErr := h.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
		To:      member.Email,
		Subject: subject,
		HTML:    html,
		Text:    text,
	}); sendErr != nil {
		slog.WarnContext(ctx, "failed to send member notification email",
			"error", sendErr, "committee_uid", member.CommitteeUID)
	} else {
		slog.InfoContext(ctx, "sent member notification email",
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
			"recipient_email", redaction.RedactEmail(member.Email),
			"username", redaction.Redact(member.Username))
	}

	return nil, nil
}

// sendMemberInvite sends an invite request for a new committee member who does not
// yet have an LFID. Best-effort: logs failures internally; callers may ignore the returned error.
func (h *committeeNotificationHandler) sendMemberInvite(ctx context.Context, member *model.CommitteeMember, recipientName, deepLinkURL string) error {
	if h.inviteSender == nil {
		slog.DebugContext(ctx, "invite sender not configured — skipping member invite",
			"committee_uid", member.CommitteeUID)
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	result, err := h.inviteSender.SendInvite(sendCtx, inviteapi.SendInviteRequest{
		Recipient: &inviteapi.Recipient{
			Email: strings.TrimSpace(member.Email),
			Name:  recipientName,
		},
		Inviter: &inviteapi.Inviter{
			Name: "A committee administrator",
		},
		Resource: &inviteapi.Resource{
			UID:  member.CommitteeUID,
			Name: member.CommitteeName,
			Type: "group",
		},
		Role:      string(inviteapi.InviteRoleMember),
		ReturnURL: deepLinkURL,
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to send member invite request",
			"error", err, "committee_uid", member.CommitteeUID)
		return err
	}

	slog.InfoContext(ctx, "sent member invite request",
		"committee_uid", member.CommitteeUID,
		"member_uid", member.UID,
		"recipient_email", redaction.RedactEmail(member.Email),
		"invite_uid", result.InviteUID)
	return nil
}

// HandleCommitteeSettingsUpdated handles committee_settings.updated events and notifies
// Writers or Auditors newly added in the updated settings. Users with an LFID (Username
// present) receive a direct notification email. Users without an LFID receive an invite
// via the invite service. Best-effort: send errors are logged, not returned.
func (h *committeeNotificationHandler) HandleCommitteeSettingsUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_settings.updated event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_settings.updated event has unexpected data shape", "error", err)
		return nil, nil
	}

	var data model.CommitteeSettingsUpdateEventData
	if err := json.Unmarshal(raw, &data); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeSettingsUpdateEventData from event", "error", err)
		return nil, nil
	}

	// Classify every user that appears in old or new settings into one of:
	// added (newly appeared), updated (role-set changed), removed (fully gone).
	changes := classifyCommitteeUsers(data.OldSettings, data.Settings)
	if len(changes) == 0 {
		slog.DebugContext(ctx, "no writer/auditor changes — skipping settings notification",
			"committee_uid", data.CommitteeUID)
		return nil, nil
	}

	committeeURL := buildCommitteeURL(h.lfxSelfServeBaseURL, data.CommitteeUID)

	resolveCtx, resolveCancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	inviterName := h.resolveDisplayName(resolveCtx, data.UpdatedBy)
	resolveCancel()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, c := range changes {
		g.Go(func() error {
			u, kind, oldRoles, newRoles := c.user, c.kind, c.oldRoles, c.newRoles

			// Removed non-LF users get no notification.
			if kind == roleChangeKindRemoved && u.Username == "" {
				slog.DebugContext(gctx, "skipping removal notification — non-LF user",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			if u.Email == "" {
				slog.WarnContext(gctx, "skipping settings notification — user has no email address",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			recipientName := u.Name
			if recipientName == "" {
				recipientName = u.Username
			}
			if recipientName == "" {
				recipientName = u.Email
			}

			// Skip "added" notification if this user was previously an invited (email-only) entry in
			// the old settings — they're being promoted from non-LFID to LFID via invite acceptance,
			// not freshly added. They already received the invite email; a second email would be
			// confusing and redundant.
			if kind == roleChangeKindAdded && u.Email != "" && wasInvitedInOldSettings(u.Email, data.OldSettings) {
				slog.DebugContext(gctx, "skipping notification — email was already present in old settings",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			if u.Username == "" {
				// No LFID — added/updated paths go through the invite service; removed was handled above.
				if h.inviteSender == nil {
					slog.DebugContext(gctx, "invite sender not configured — skipping settings invite",
						"committee_uid", data.CommitteeUID)
					return nil
				}
				// Skip re-invite when effective access is unchanged (e.g. gaining Auditor on top of Writer).
				if kind == roleChangeKindUpdated && effectiveRoleUnchanged(oldRoles, newRoles) {
					slog.DebugContext(gctx, "skipping non-LF invite — effective role unchanged",
						"committee_uid", data.CommitteeUID)
					return nil
				}
				// Use the highest new role for the invite (Writer > Auditor for access level).
				inviteRole := mapRoleToInviteRole(highestRole(newRoles))
				inviteCtx, inviteCancel := context.WithTimeout(gctx, committeeNotificationTimeout)
				result, inviteErr := h.inviteSender.SendInvite(inviteCtx, inviteapi.SendInviteRequest{
					Recipient: &inviteapi.Recipient{
						Email: strings.TrimSpace(u.Email),
						Name:  recipientName,
					},
					Inviter: &inviteapi.Inviter{
						Name: inviterName,
					},
					Resource: &inviteapi.Resource{
						UID:  data.CommitteeUID,
						Name: data.CommitteeName,
						Type: "group",
					},
					Role:      inviteRole,
					ReturnURL: committeeURL,
				})
				inviteCancel()
				if inviteErr != nil {
					slog.WarnContext(gctx, "failed to send settings invite request",
						"error", inviteErr, "committee_uid", data.CommitteeUID)
					return nil
				}

				slog.InfoContext(gctx, "sent settings invite request",
					"committee_uid", data.CommitteeUID,
					"recipient_email", redaction.RedactEmail(u.Email),
					"invite_uid", result.InviteUID,
					"kind", kind)

				return nil
			}

			// LFID present — send a direct notification email.
			if h.emailSender == nil {
				slog.DebugContext(gctx, "email sender not configured — skipping settings notification",
					"committee_uid", data.CommitteeUID)
				return nil
			}

			var emailSubject, emailHTML, emailText string
			var renderErr error

			switch kind {
			case roleChangeKindAdded:
				// Newly added — use the original "added you as a <role>" email.
				// newRoles may contain multiple roles; display only the highest-privilege one.
				roleDisplay := emailsvc.CommitteeRoleDisplayName(highestRole(newRoles))
				emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeRoleNotification(emailsvc.CommitteeRoleNotificationData{
					RecipientName: recipientName,
					CommitteeName: data.CommitteeName,
					Role:          roleDisplay,
					CommitteeURL:  committeeURL,
					InviterName:   inviterName,
				})
			case roleChangeKindUpdated:
				// Skip email when effective display role is unchanged (e.g. Auditor gained on top of Writer).
				if effectiveRoleUnchanged(oldRoles, newRoles) {
					slog.DebugContext(gctx, "skipping role-updated email — effective role unchanged",
						"committee_uid", data.CommitteeUID)
					return nil
				}
				emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeRoleUpdated(emailsvc.CommitteeRoleUpdatedData{
					RecipientName: recipientName,
					CommitteeName: data.CommitteeName,
					OldRoles:      oldRoles,
					NewRoles:      newRoles,
					CommitteeURL:  committeeURL,
					InviterName:   inviterName,
				})
			case roleChangeKindRemoved:
				emailSubject, emailHTML, emailText, renderErr = emailsvc.RenderCommitteeRoleRemoved(emailsvc.CommitteeRoleRemovedData{
					RecipientName: recipientName,
					CommitteeName: data.CommitteeName,
					OldRoles:      oldRoles,
					InviterName:   inviterName,
				})
			}

			if renderErr != nil {
				slog.WarnContext(gctx, "failed to render settings notification email",
					"error", renderErr, "committee_uid", data.CommitteeUID, "kind", kind)
				return nil
			}

			sendCtx, cancel := context.WithTimeout(gctx, committeeNotificationTimeout)
			defer cancel()
			if sendErr := h.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      u.Email,
				Subject: emailSubject,
				HTML:    emailHTML,
				Text:    emailText,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send settings notification email",
					"error", sendErr, "committee_uid", data.CommitteeUID, "kind", kind)
			} else {
				slog.InfoContext(gctx, "sent settings notification email",
					"committee_uid", data.CommitteeUID,
					"recipient_email", redaction.RedactEmail(u.Email),
					"username", redaction.Redact(u.Username),
					"kind", kind)
			}
			return nil
		})
	}
	_ = g.Wait()

	return nil, nil
}

// HandleInviteAccepted processes an invite acceptance event published by the invite service.
// It scans all committees for email-only records matching the recipient email and enriches
// them with the accepted user's LFID (username set). Writers, Auditors, and Members are all
// enriched regardless of which invite role triggered acceptance.
//
// TODO(LFXV2-2238): replace the full-scan with an email → [committee_uid] index lookup so we avoid
// loading every committee's settings and listing all members on each acceptance event.
func (h *committeeNotificationHandler) HandleInviteAccepted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event inviteapi.InviteServiceAcceptedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal invite_accepted event", "error", err)
		return nil, nil
	}

	if event.UID == "" || event.AcceptedBy == "" || strings.TrimSpace(event.Recipient.Email) == "" {
		slog.WarnContext(ctx, "invite_accepted event missing required fields — discarding",
			"invite_uid", event.UID, "accepted_by", redaction.Redact(event.AcceptedBy),
			"recipient_email", redaction.RedactEmail(event.Recipient.Email))
		return nil, nil
	}

	if h.committeeWriterOrchestrator == nil {
		slog.WarnContext(ctx, "committee writer orchestrator not available — cannot persist invite enrichment",
			"invite_uid", event.UID)
		return nil, nil
	}

	// NATS event handlers have no inbound HTTP request and therefore no JWT in ctx.
	// Inject a service-identity bearer so UpdateSettings' downstream calls (FGA, indexer)
	// carry a recognized auth token. The header is propagated into context by
	// internal/middleware/authorization.go (no allow-listing — it copies whatever header
	// is present; trust is enforced by the downstream FGA/indexer services).
	writeCtx := context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	normalizedEmail := strings.ToLower(strings.TrimSpace(event.Recipient.Email))

	// Publish FGA invitee tuples upfront — before the full committee scan — so the user can
	// see their pending invites in the UI immediately after LFID acceptance. The UI can then
	// call AcceptInvite on the now-visible invite without any backend polling.
	// Guard: skip when FGA publishing is disabled (committeePublisher == nil).
	// TODO(LFXV2-2238): replace ListAllInvites with an email→committee index so this avoids a full scan.
	if h.committeePublisher != nil {
		invitesByCommittee := h.fetchInvitesByEmail(ctx, normalizedEmail)
		for committeeUID := range invitesByCommittee {
			h.publishInviteeFGAForCommittee(ctx, inviteAcceptedEnrichment{
				committeeUID:       committeeUID,
				username:           event.AcceptedBy,
				invitesByCommittee: invitesByCommittee,
			})
		}
	}

	// Full scan (LFXV2-2238): per committee, load settings and list members to reconcile email-only records.
	allUIDs, listErr := h.committeeReader.ListAllUIDs(ctx)
	if listErr != nil {
		slog.WarnContext(ctx, "failed to list committee UIDs for invite reconciliation",
			"error", listErr, "invite_uid", event.UID)
		return nil, nil
	}

	// Resolve the invitee's name once — avoids one round-trip per committee in the full scan.
	// Precedence: auth-service user_metadata.read (meta.Name, or GivenName+FamilyName) → Recipient.Name from payload.
	resolveCtx, resolveCancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	firstName, lastName, fullName := h.resolveInvitedName(resolveCtx, event.AcceptedBy, event.Recipient.Name)
	resolveCancel()

	var g errgroup.Group
	g.SetLimit(10)
	for _, committeeUID := range allUIDs {
		g.Go(func() error {
			h.enrichInvitedUserInCommittee(ctx, inviteAcceptedEnrichment{
				writeCtx:        writeCtx,
				committeeUID:    committeeUID,
				normalizedEmail: normalizedEmail,
				username:        event.AcceptedBy,
				inviteUID:       event.UID,
				firstName:       firstName,
				lastName:        lastName,
				fullName:        fullName,
			})
			return nil
		})
	}
	_ = g.Wait()

	return nil, nil
}

// inviteAcceptedEnrichment holds the per-event context threaded through the invite-accepted
// enrichment chain. committeeUID is set per-iteration of the full-committee scan.
// invitesByCommittee is keyed by committeeUID and contains only invites for the accepting email,
// pre-fetched once before the loop to avoid repeated full-bucket scans.
type inviteAcceptedEnrichment struct {
	writeCtx           context.Context
	committeeUID       string
	normalizedEmail    string
	username           string
	inviteUID          string
	firstName          string
	lastName           string
	fullName           string
	invitesByCommittee map[string][]*model.CommitteeInvite
}

// enrichInvitedUserInCommittee enriches every email-only Writers, Auditors, and Members record
// for e.normalizedEmail in the given committee. Invite role is ignored — acceptance always
// reconciles all resource data for the recipient email. FGA invitee tuples are published
// upfront in HandleInviteAccepted before this scan runs, so they are not repeated here.
func (h *committeeNotificationHandler) enrichInvitedUserInCommittee(ctx context.Context, e inviteAcceptedEnrichment) {
	h.enrichInvitedUserInCommitteeSettings(ctx, e)
	h.enrichInvitedUserInCommitteeMembers(ctx, e)
}

// fetchInvitesByEmail calls ListAllInvites once and returns a map of committeeUID → invites
// for invites whose InviteeEmail matches normalizedEmail. When the storage layer returns a
// partial result alongside a non-nil error (e.g. some individual reads failed), the successfully
// loaded invites are still used — only a total failure (nil slice) causes an early return.
func (h *committeeNotificationHandler) fetchInvitesByEmail(ctx context.Context, normalizedEmail string) map[string][]*model.CommitteeInvite {
	all, err := h.committeeReader.ListAllInvites(ctx)
	if err != nil {
		slog.WarnContext(ctx, "partial or total failure listing committee invites for FGA invitee grant",
			"error", err)
		if len(all) == 0 {
			return nil
		}
	}
	result := make(map[string][]*model.CommitteeInvite)
	for _, invite := range all {
		if invite == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(invite.InviteeEmail)) != normalizedEmail {
			continue
		}
		result[invite.CommitteeUID] = append(result[invite.CommitteeUID], invite)
	}
	return result
}

// publishInviteeFGAForCommittee publishes an FGA update_access message for every committee_invite
// for the accepting email in the given committee, regardless of invite status. Uses the pre-fetched
// invitesByCommittee map to avoid per-committee full-bucket scans.
func (h *committeeNotificationHandler) publishInviteeFGAForCommittee(ctx context.Context, e inviteAcceptedEnrichment) {
	if h.committeePublisher == nil {
		return
	}
	for _, invite := range e.invitesByCommittee[e.committeeUID] {
		if invite == nil {
			continue
		}
		msg := fgatypes.GenericFGAMessage{
			ObjectType: "committee_invite",
			Operation:  "update_access",
			Data: fgatypes.GenericAccessData{
				UID: invite.UID,
				References: map[string][]string{
					"committee": {invite.CommitteeUID},
				},
				Relations: map[string][]string{
					"invitee": {e.username},
				},
			},
		}
		if pubErr := h.committeePublisher.UpdateAccess(ctx, msg); pubErr != nil {
			slog.WarnContext(ctx, "failed to publish FGA invitee grant for committee invite",
				"invite_uid", invite.UID, "committee_uid", e.committeeUID,
				"username", redaction.Redact(e.username), "error", pubErr)
		} else {
			slog.DebugContext(ctx, "published FGA invitee grant for committee invite",
				"invite_uid", invite.UID, "committee_uid", e.committeeUID,
				"username", redaction.Redact(e.username))
		}
	}
}

// enrichInvitedUserInCommitteeSettings enriches all email-only Writers and Auditors matching
// e.normalizedEmail in the given committee's settings with the accepted LFID and display name.
// It retries on revision conflicts.
func (h *committeeNotificationHandler) enrichInvitedUserInCommitteeSettings(ctx context.Context, e inviteAcceptedEnrichment) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		settings, revision, err := h.committeeReader.GetSettings(ctx, e.committeeUID)
		if err != nil {
			slog.WarnContext(ctx, "failed to get settings for invite enrichment",
				"error", err, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
			return
		}

		enriched := false
		for i := range settings.Writers {
			if enrichCommitteeUserIfEmailOnly(&settings.Writers[i], e.normalizedEmail, e.username, e.fullName) {
				enriched = true
			}
		}
		for i := range settings.Auditors {
			if enrichCommitteeUserIfEmailOnly(&settings.Auditors[i], e.normalizedEmail, e.username, e.fullName) {
				enriched = true
			}
		}

		if !enriched {
			return
		}

		if _, writeErr := h.committeeWriterOrchestrator.UpdateSettings(e.writeCtx, settings, revision, false); writeErr != nil {
			var conflictErr errors.Conflict
			if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(ctx, "revision conflict enriching invite — retrying",
					"attempt", attempt+1, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
				continue
			}
			slog.ErrorContext(ctx, "failed to update settings after invite acceptance",
				"error", writeErr, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
			return
		}

		slog.DebugContext(ctx, "invite accepted — enriched email-only Writers/Auditors with LFID",
			"committee_uid", e.committeeUID, "invite_uid", e.inviteUID, "username", redaction.Redact(e.username))
		return
	}
}

// enrichInvitedUserInCommitteeMembers enriches all email-only committee members matching
// e.normalizedEmail in the given committee with the accepted LFID and name.
func (h *committeeNotificationHandler) enrichInvitedUserInCommitteeMembers(ctx context.Context, e inviteAcceptedEnrichment) {
	members, err := h.committeeReader.ListMembersByCommittee(ctx, e.committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to list members for invite enrichment",
			"error", err, "committee_uid", e.committeeUID, "invite_uid", e.inviteUID)
		return
	}

	for _, member := range members {
		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != e.normalizedEmail {
			continue
		}
		h.enrichInvitedCommitteeMember(ctx, e, member.UID)
	}
}

// enrichInvitedCommitteeMember enriches a single email-only member with the accepted LFID and name.
// Revision-conflict retries are handled here, not in enrichInvitedUserInCommitteeMembers.
// memberUID identifies the specific member within e.committeeUID.
func (h *committeeNotificationHandler) enrichInvitedCommitteeMember(ctx context.Context, e inviteAcceptedEnrichment, memberUID string) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		member, revision, err := h.committeeReader.GetMember(ctx, e.committeeUID, memberUID)
		if err != nil {
			slog.WarnContext(ctx, "failed to get member for invite enrichment",
				"error", err, "committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID)
			return
		}

		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != e.normalizedEmail {
			return
		}

		member.Username = e.username
		// Only fill name fields that are not already set — preserves any name stored at invite creation.
		if member.FirstName == "" && e.firstName != "" {
			member.FirstName = e.firstName
		}
		if member.LastName == "" && e.lastName != "" {
			member.LastName = e.lastName
		}

		updated, writeErr := h.committeeWriterOrchestrator.UpdateMember(e.writeCtx, member, revision, false, true)
		if writeErr != nil {
			var conflictErr errors.Conflict
			if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(ctx, "revision conflict enriching member — retrying",
					"attempt", attempt+1, "committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID)
				continue
			}
			slog.ErrorContext(ctx, "failed to update member after invite acceptance",
				"error", writeErr, "committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID)
			return
		}

		persistedUsername := e.username
		if updated != nil {
			persistedUsername = updated.Username
		}
		slog.DebugContext(ctx, "invite accepted — enriched email-only member with LFID",
			"committee_uid", e.committeeUID, "member_uid", memberUID, "invite_uid", e.inviteUID,
			"username", redaction.Redact(persistedUsername))
		return
	}
}

// enrichCommitteeUserIfEmailOnly sets username (and name when not already set) on a settings
// user when the entry is email-only and matches normalizedEmail. Returns true when the user
// was enriched.
func enrichCommitteeUserIfEmailOnly(user *model.CommitteeUser, normalizedEmail, username, fullName string) bool {
	if user.Username != "" || strings.ToLower(strings.TrimSpace(user.Email)) != normalizedEmail {
		return false
	}
	user.Username = username
	// Only fill the name when the record has none — preserves any name stored at invite creation.
	if user.Name == "" && fullName != "" {
		user.Name = fullName
	}
	return true
}

// resolveInvitedName returns (firstName, lastName, fullName) for the accepted invitee.
// Precedence: auth-service UserMetadataByPrincipal result (when it supplies any name) →
// payload Recipient.Name fallback. The payload is also used when metadata is non-nil but
// carries no name fields. All lookups are best-effort: a transport error logs a warning
// and triggers the fallback so the caller is never blocked.
func (h *committeeNotificationHandler) resolveInvitedName(ctx context.Context, principal, payloadName string) (firstName, lastName, fullName string) {
	if h.userReader != nil && principal != "" {
		meta, err := h.userReader.UserMetadataByPrincipal(ctx, principal)
		if err != nil {
			slog.WarnContext(ctx, "user metadata lookup failed during invite acceptance — falling back to payload name",
				"principal", redaction.Redact(principal), "error", err)
		} else if meta != nil {
			firstName = meta.GivenName
			lastName = meta.FamilyName
			fullName = meta.Name
			if fullName == "" {
				fullName = strings.TrimSpace(meta.GivenName + " " + meta.FamilyName)
			}
			if firstName == "" && lastName == "" && fullName != "" {
				firstName, lastName = splitName(fullName)
			}
			// Only return metadata-derived names when metadata actually supplied
			// at least one — a non-nil record with all empty fields falls through
			// to the payload fallback below.
			if firstName != "" || lastName != "" || fullName != "" {
				return firstName, lastName, fullName
			}
		}
	}

	// Metadata unavailable (lookup failed or returned nil) — fall back to payload.
	firstName, lastName = splitName(payloadName)
	fullName = strings.TrimSpace(payloadName)
	return firstName, lastName, fullName
}

// splitName splits a combined display name on the first space, returning (first, rest).
// Leading/trailing whitespace is trimmed from both parts.
func splitName(name string) (first, last string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}
	parts := strings.SplitN(name, " ", 2)
	first = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		last = strings.TrimSpace(parts[1])
	}
	return first, last
}

// resolveDisplayName looks up the display name for the given principal via the user reader.
// Returns "A committee administrator" if the lookup fails or the metadata has no name.
func (h *committeeNotificationHandler) resolveDisplayName(ctx context.Context, principal string) string {
	if principal != "" && h.userReader != nil {
		meta, err := h.userReader.UserMetadataByPrincipal(ctx, principal)
		if err != nil {
			slog.WarnContext(ctx, "failed to look up inviter display name — using default",
				"error", err)
		} else if meta != nil {
			if meta.Name != "" {
				return meta.Name
			}
			if full := strings.TrimSpace(meta.GivenName + " " + meta.FamilyName); full != "" {
				return full
			}
		}
	}
	return "A committee administrator"
}

// HandleCommitteeMemberDeleted sends a removal notification email when an LF committee
// member is deleted. Non-LF members (Username == "") receive nothing. Best-effort: send
// errors are logged, not returned.
func (h *committeeNotificationHandler) HandleCommitteeMemberDeleted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_member.deleted event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_member.deleted event has unexpected data shape", "error", err)
		return nil, nil
	}

	// Decode the deleted-event payload into the typed wrapper so the
	// request-scoped skip_notification flag is parsed alongside the member. On a
	// malformed payload we fail safe by suppressing the notification rather than
	// defaulting to send.
	var deleted model.CommitteeMemberDeletedEventData
	if err := json.Unmarshal(raw, &deleted); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeMemberDeletedEventData from committee_member.deleted event — suppressing notification", "error", err)
		return nil, nil
	}
	if deleted.CommitteeMember == nil {
		slog.WarnContext(ctx, "committee_member.deleted event missing member payload — suppressing notification")
		return nil, nil
	}
	if deleted.SkipNotification {
		slog.DebugContext(ctx, "skipping member-deleted notification — skip_notification flag set",
			"committee_uid", deleted.CommitteeUID)
		return nil, nil
	}
	member := *deleted.CommitteeMember

	if member.Username == "" {
		slog.DebugContext(ctx, "skipping member-deleted notification — non-LF user",
			"committee_uid", member.CommitteeUID)
		return nil, nil
	}

	if member.Email == "" {
		slog.WarnContext(ctx, "skipping member-deleted notification — no email address",
			"committee_uid", member.CommitteeUID, "username", redaction.Redact(member.Username))
		return nil, nil
	}

	if h.emailSender == nil {
		slog.DebugContext(ctx, "email sender not configured — skipping member-deleted notification")
		return nil, nil
	}

	recipientName := strings.TrimSpace(member.FirstName + " " + member.LastName)
	if recipientName == "" {
		recipientName = member.Username
	}
	if recipientName == "" {
		recipientName = member.Email
	}

	var oldRoleNames []string
	if member.Role.Name != "" {
		oldRoleNames = []string{member.Role.Name}
	}
	subject, html, text, renderErr := emailsvc.RenderCommitteeRoleRemoved(emailsvc.CommitteeRoleRemovedData{
		RecipientName: recipientName,
		CommitteeName: member.CommitteeName,
		OldRoles:      oldRoleNames,
		InviterName:   "A committee administrator",
	})
	if renderErr != nil {
		slog.WarnContext(ctx, "failed to render member-deleted notification email",
			"error", renderErr, "committee_uid", member.CommitteeUID)
		return nil, nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	if sendErr := h.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
		To:      member.Email,
		Subject: subject,
		HTML:    html,
		Text:    text,
	}); sendErr != nil {
		slog.WarnContext(ctx, "failed to send member-deleted notification email",
			"error", sendErr, "committee_uid", member.CommitteeUID)
	} else {
		slog.InfoContext(ctx, "sent member-deleted notification email",
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
			"recipient_email", redaction.RedactEmail(member.Email),
			"username", redaction.Redact(member.Username))
	}

	return nil, nil
}

// committeeContentItem is a unified representation of a file document or link for notification purposes.
type committeeContentItem struct {
	committeeUID      string
	documentType      string // "file" or "link"
	documentName      string // user-given display name
	fileName          string // actual file name, set for documentType "file"
	url               string // set for documentType "link"
	folderName        string // optional folder context
	createdByUsername string // LFID of the uploader/creator
}

// HandleCommitteeDocumentCreated handles committee_document.created events and notifies all
// LFID members, writers, and auditors of the committee. Best-effort: errors are logged, not returned.
func (h *committeeNotificationHandler) HandleCommitteeDocumentCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_document.created event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_document.created event has unexpected data shape", "error", err)
		return nil, nil
	}

	var doc model.CommitteeDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeDocument from event data", "error", err)
		return nil, nil
	}

	if doc.CommitteeUID == "" {
		slog.WarnContext(ctx, "committee_document.created event missing committee_uid — discarding")
		return nil, nil
	}

	item := committeeContentItem{
		committeeUID:      doc.CommitteeUID,
		documentType:      "file",
		documentName:      doc.Name,
		fileName:          doc.FileName,
		folderName:        h.resolveFolderName(ctx, doc.CommitteeUID, doc.FolderUID),
		createdByUsername: model.AuditCreatorUsername(doc.CreatedBy),
	}

	h.handleContentCreated(ctx, item)
	return nil, nil
}

// HandleCommitteeLinkCreated handles committee_link.created events and notifies all
// LFID members, writers, and auditors of the committee. Best-effort: errors are logged, not returned.
func (h *committeeNotificationHandler) HandleCommitteeLinkCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal committee_link.created event", "error", err)
		return nil, nil
	}

	raw, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "committee_link.created event has unexpected data shape", "error", err)
		return nil, nil
	}

	var link model.CommitteeLink
	if err := json.Unmarshal(raw, &link); err != nil {
		slog.WarnContext(ctx, "cannot decode CommitteeLink from event data", "error", err)
		return nil, nil
	}

	if link.CommitteeUID == "" {
		slog.WarnContext(ctx, "committee_link.created event missing committee_uid — discarding")
		return nil, nil
	}

	// Only include the URL in the email if it uses a safe http(s) scheme.
	safeURL := ""
	if isSafeURL(link.URL) {
		safeURL = link.URL
	} else if link.URL != "" {
		slog.WarnContext(ctx, "omitting unsafe link URL from notification email",
			"committee_uid", link.CommitteeUID, "link_uid", link.UID)
	}

	item := committeeContentItem{
		committeeUID:      link.CommitteeUID,
		documentType:      "link",
		documentName:      link.Name,
		url:               safeURL,
		folderName:        h.resolveFolderName(ctx, link.CommitteeUID, link.FolderUID),
		createdByUsername: model.AuditCreatorUsername(link.CreatedBy),
	}

	h.handleContentCreated(ctx, item)
	return nil, nil
}

// handleContentCreated fans out notification emails to all LFID members, writers, and auditors
// of the committee. Best-effort: individual send failures are logged but never abort the batch.
func (h *committeeNotificationHandler) handleContentCreated(ctx context.Context, item committeeContentItem) {
	if h.committeeReader == nil || h.emailSender == nil {
		slog.DebugContext(ctx, "committee reader or email sender not configured — skipping content notification",
			"committee_uid", item.committeeUID)
		return
	}

	committee, _, err := h.committeeReader.GetBase(ctx, item.committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load committee for content notification",
			"error", err, "committee_uid", item.committeeUID)
		return
	}

	recipients := h.collectCommitteeRecipients(ctx, item.committeeUID)
	if len(recipients) == 0 {
		slog.DebugContext(ctx, "no LFID recipients for content notification", "committee_uid", item.committeeUID)
		return
	}

	// Only resolve the uploader display name when a principal is present; otherwise
	// leave it empty so the template renders the generic "A new X was added" message.
	var uploaderName string
	if item.createdByUsername != "" {
		uploaderName = h.resolveDisplayNameWithTimeout(ctx, item.createdByUsername)
	}
	committeeURL := buildCommitteeURL(h.lfxSelfServeBaseURL, item.committeeUID)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, r := range recipients {
		recipient := r
		g.Go(func() error {
			email := recipient.email
			if email == "" {
				slog.WarnContext(gctx, "skipping content notification — user has no email address",
					"committee_uid", item.committeeUID,
					"username", redaction.Redact(recipient.username))
				return nil
			}

			recipientName := recipient.name
			if recipientName == "" {
				recipientName = recipient.username
			}
			if recipientName == "" {
				recipientName = email
			}

			emailSubject, emailHTML, emailText, renderErr := emailsvc.RenderCommitteeDocumentNotification(
				emailsvc.CommitteeDocumentNotificationData{
					RecipientName: recipientName,
					CommitteeName: committee.Name,
					CommitteeURL:  committeeURL,
					UploaderName:  uploaderName,
					DocumentType:  item.documentType,
					DocumentName:  item.documentName,
					FileName:      item.fileName,
					URL:           item.url,
					FolderName:    item.folderName,
				},
			)
			if renderErr != nil {
				slog.WarnContext(gctx, "failed to render content notification email",
					"error", renderErr, "committee_uid", item.committeeUID)
				return nil
			}

			sendCtx, cancel := context.WithTimeout(gctx, committeeNotificationTimeout)
			defer cancel()
			if sendErr := h.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      email,
				Subject: emailSubject,
				HTML:    emailHTML,
				Text:    emailText,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send content notification email",
					"error", sendErr, "committee_uid", item.committeeUID)
			} else {
				slog.InfoContext(gctx, "sent content notification email",
					"committee_uid", item.committeeUID,
					"recipient_email", redaction.RedactEmail(email),
					"username", redaction.Redact(recipient.username),
					"document_type", item.documentType)
			}
			return nil
		})
	}
	_ = g.Wait()
}

// notificationRecipient is a minimal recipient record used for fan-out.
type notificationRecipient struct {
	username string
	email    string
	name     string
}

// collectCommitteeRecipients returns a deduplicated list of LFID recipients (members + writers + auditors).
// Users without an LFID (Username == "") are excluded — they cannot receive direct emails at this phase.
// When the same LFID appears in multiple role lists, later records can enrich a missing email or name
// from earlier ones rather than being silently dropped.
// Note: the uploader/creator is intentionally included — the notification is sent uniformly to all
// roles regardless of who uploaded the item, so the document appears in everyone's inbox consistently.
func (h *committeeNotificationHandler) collectCommitteeRecipients(ctx context.Context, committeeUID string) []notificationRecipient {
	seen := make(map[string]int) // username → index in recipients slice
	var recipients []notificationRecipient

	add := func(username, email, name string) {
		if username == "" {
			return
		}
		if idx, ok := seen[username]; ok {
			// Enrich existing entry with richer data from later sources.
			if recipients[idx].email == "" && email != "" {
				recipients[idx].email = email
			}
			if recipients[idx].name == "" && name != "" {
				recipients[idx].name = name
			}
			return
		}
		seen[username] = len(recipients)
		recipients = append(recipients, notificationRecipient{
			username: username,
			email:    email,
			name:     name,
		})
	}

	members, err := h.committeeReader.ListMembersByCommittee(ctx, committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to list members for content notification",
			"error", err, "committee_uid", committeeUID)
	} else {
		for _, member := range members {
			add(member.Username, member.Email, strings.TrimSpace(member.FirstName+" "+member.LastName))
		}
	}

	settings, _, err := h.committeeReader.GetSettings(ctx, committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load settings for content notification",
			"error", err, "committee_uid", committeeUID)
	} else {
		for _, u := range settings.GetWriters() {
			add(u.Username, u.Email, u.Name)
		}
		for _, u := range settings.GetAuditors() {
			add(u.Username, u.Email, u.Name)
		}
	}

	return recipients
}

// resolveDisplayNameWithTimeout wraps resolveDisplayName with a bounded context.
func (h *committeeNotificationHandler) resolveDisplayNameWithTimeout(ctx context.Context, principal string) string {
	lookupCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	return h.resolveDisplayName(lookupCtx, principal)
}

// resolveFolderName looks up the display name of a link folder by UID.
// Returns an empty string if the UID is nil/empty, no link reader is configured, or the lookup fails.
func (h *committeeNotificationHandler) resolveFolderName(ctx context.Context, committeeUID string, folderUID *string) string {
	if folderUID == nil || *folderUID == "" || h.linkReader == nil {
		return ""
	}
	lookupCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	folder, _, err := h.linkReader.GetLinkFolder(lookupCtx, committeeUID, *folderUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to resolve folder name for content notification",
			"error", err, "committee_uid", committeeUID, "folder_uid", *folderUID)
		return ""
	}
	return folder.Name
}

// isSafeURL reports whether rawURL uses an http or https scheme.
// Used to prevent unsafe URL schemes (e.g. javascript:) from appearing as clickable links in emails.
func isSafeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

// HandleCommitteeApplicationSubmitted handles committee_application.submitted events and notifies
// all LFID writers of the committee that a new application is awaiting review.
// Best-effort: individual send failures are logged but never abort the batch.
func (h *committeeNotificationHandler) HandleCommitteeApplicationSubmitted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
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

	if h.committeeReader == nil || h.emailSender == nil {
		slog.DebugContext(ctx, "committee reader or email sender not configured — skipping application submitted notification",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	committee, _, err := h.committeeReader.GetBase(ctx, application.CommitteeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load committee for application submitted notification",
			"error", err, "committee_uid", application.CommitteeUID)
		return nil, nil
	}

	writers := h.collectCommitteeWriters(ctx, committee)
	if len(writers) == 0 {
		slog.DebugContext(ctx, "no LFID writers to notify for application submitted",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	committeeURL := buildCommitteeURL(h.lfxSelfServeBaseURL, application.CommitteeUID)

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
			if sendErr := h.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      writer.email,
				Subject: emailSubject,
				HTML:    emailHTML,
				Text:    emailText,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send application submitted notification email",
					"error", sendErr, "committee_uid", application.CommitteeUID,
					"username", redaction.Redact(writer.username))
			} else {
				slog.InfoContext(gctx, "sent application submitted notification email",
					"committee_uid", application.CommitteeUID,
					"application_uid", application.UID,
					"recipient_email", redaction.RedactEmail(writer.email),
					"username", redaction.Redact(writer.username))
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
func (h *committeeNotificationHandler) HandleCommitteeApplicationUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
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

	if h.committeeReader == nil || h.emailSender == nil {
		slog.DebugContext(ctx, "committee reader or email sender not configured — skipping application updated notification",
			"committee_uid", application.CommitteeUID)
		return nil, nil
	}

	committee, _, err := h.committeeReader.GetBase(ctx, application.CommitteeUID)
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
		committeeURL := buildCommitteeURL(h.lfxSelfServeBaseURL, application.CommitteeUID)
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
	if sendErr := h.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
		To:      application.ApplicantEmail,
		Subject: emailSubject,
		HTML:    emailHTML,
		Text:    emailText,
	}); sendErr != nil {
		slog.WarnContext(ctx, "failed to send application updated notification email",
			"error", sendErr, "committee_uid", application.CommitteeUID, "status", application.Status)
	} else {
		slog.InfoContext(ctx, "sent application updated notification email",
			"committee_uid", application.CommitteeUID,
			"application_uid", application.UID,
			"recipient_email", redaction.RedactEmail(application.ApplicantEmail),
			"status", application.Status)
	}

	return nil, nil
}

// collectCommitteeWriters returns a deduplicated list of LFID writers (Username and Email both set).
// Non-LFID writers (Username == "") are excluded — they cannot receive direct emails.
// When the committee has no eligible writers, falls back to the owning project's writers
// via a NATS request to the project service (lfx.projects-api.get_writers).
func (h *committeeNotificationHandler) collectCommitteeWriters(ctx context.Context, committee *model.CommitteeBase) []notificationRecipient {
	seen := make(map[string]struct{})
	var writers []notificationRecipient

	addUsers := func(users []model.CommitteeUser) {
		for _, u := range users {
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

	settings, _, err := h.committeeReader.GetSettings(ctx, committee.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load settings for application submitted notification",
			"error", err, "committee_uid", committee.UID)
	} else {
		addUsers(settings.GetWriters())
	}

	if len(writers) == 0 && h.projectReader != nil && committee.ProjectUID != "" && committee.ProjectUID != committee.UID {
		slog.DebugContext(ctx, "no committee writers found — falling back to project writers",
			"committee_uid", committee.UID, "project_uid", committee.ProjectUID)
		projectWriters, projErr := h.projectReader.Writers(ctx, committee.ProjectUID)
		if projErr != nil {
			slog.WarnContext(ctx, "failed to fetch project writers for fallback",
				"error", projErr, "committee_uid", committee.UID, "project_uid", committee.ProjectUID)
		} else {
			addUsers(projectWriters)
		}
	}

	return writers
}

// roleChangeKind describes what changed for a user between old and new settings.
type roleChangeKind string

const (
	roleChangeKindAdded   roleChangeKind = "added"
	roleChangeKindUpdated roleChangeKind = "updated"
	roleChangeKindRemoved roleChangeKind = "removed"
)

// committeeUserRoleChange describes a single user whose role-set changed.
type committeeUserRoleChange struct {
	user     model.CommitteeUser
	kind     roleChangeKind
	oldRoles []string // sorted; empty when kind == added
	newRoles []string // sorted; empty when kind == removed
}

// classifyCommitteeUsers compares old and new settings and returns one entry per user
// whose role-set changed. Role-sets are compared as sorted slices so the caller can
// render current roles in alphabetical order.
func classifyCommitteeUsers(old, new *model.CommitteeSettings) []committeeUserRoleChange {
	buildRoleSet := func(s *model.CommitteeSettings) map[string]map[string]model.CommitteeUser {
		out := make(map[string]map[string]model.CommitteeUser)
		if s == nil {
			return out
		}
		for _, u := range s.GetWriters() {
			if key := committeeUserKey(u); key != "" {
				if out[key] == nil {
					out[key] = make(map[string]model.CommitteeUser)
				}
				out[key]["Writer"] = u
			}
		}
		for _, u := range s.GetAuditors() {
			if key := committeeUserKey(u); key != "" {
				if out[key] == nil {
					out[key] = make(map[string]model.CommitteeUser)
				}
				out[key]["Auditor"] = u
			}
		}
		return out
	}

	oldSet := buildRoleSet(old)
	newSet := buildRoleSet(new)

	// Collect all user keys that appear in either old or new.
	allKeys := make(map[string]bool)
	for k := range oldSet {
		allKeys[k] = true
	}
	for k := range newSet {
		allKeys[k] = true
	}

	sortedKeys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var changes []committeeUserRoleChange
	for _, key := range sortedKeys {
		oldRoles := oldSet[key]
		newRoleMap := newSet[key]

		hadRoles := len(oldRoles) > 0
		hasRoles := len(newRoleMap) > 0

		newRoleSlice := sortedRoles(newRoleMap)

		switch {
		case !hadRoles && hasRoles:
			u := anyUser(newRoleMap)
			changes = append(changes, committeeUserRoleChange{user: u, kind: roleChangeKindAdded, newRoles: newRoleSlice})
		case hadRoles && !hasRoles:
			oldRoleSlice := sortedRoles(oldRoles)
			u := anyUser(oldRoles)
			changes = append(changes, committeeUserRoleChange{user: u, kind: roleChangeKindRemoved, oldRoles: oldRoleSlice})
		case hadRoles && hasRoles:
			oldRoleSlice := sortedRoles(oldRoles)
			if !roleSlicesEqual(oldRoleSlice, newRoleSlice) {
				u := anyUser(newRoleMap)
				changes = append(changes, committeeUserRoleChange{user: u, kind: roleChangeKindUpdated, oldRoles: oldRoleSlice, newRoles: newRoleSlice})
			}
		}
	}
	return changes
}

// sortedRoles returns the role names from a map sorted alphabetically.
func sortedRoles(m map[string]model.CommitteeUser) []string {
	roles := make([]string, 0, len(m))
	for r := range m {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return roles
}

// anyUser returns the CommitteeUser from the first entry of a role map.
// Preference is given to the Writer entry so the caller gets the richer struct
// when both Writer and Auditor are present.
func anyUser(m map[string]model.CommitteeUser) model.CommitteeUser {
	if u, ok := m["Writer"]; ok {
		return u
	}
	for _, u := range m {
		return u
	}
	return model.CommitteeUser{}
}

// roleSlicesEqual returns true when two sorted string slices are identical.
func roleSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// effectiveRoleUnchanged returns true when the user-facing display roles derived from
// oldRoles and newRoles are identical — for example, gaining Auditor while already holding
// Writer produces no visible change (both collapse to ["Manage"]), so no email is needed.
func effectiveRoleUnchanged(oldRoles, newRoles []string) bool {
	oldDisplay := emailsvc.CommitteeRolesForDisplay(oldRoles)
	newDisplay := emailsvc.CommitteeRolesForDisplay(newRoles)
	sort.Strings(oldDisplay)
	sort.Strings(newDisplay)
	return roleSlicesEqual(oldDisplay, newDisplay)
}

// highestRole returns the single highest-privilege role from a slice.
// "Writer" is considered higher than "Auditor" (maps to InviteRoleManage).
// Returns the first element if no known role is found.
func highestRole(roles []string) string {
	for _, r := range roles {
		if strings.EqualFold(r, "Writer") {
			return r
		}
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return ""
}

// mapRoleToInviteRole converts a committee settings role string to the invite
// service's role vocabulary.
//
// Mapping:
//   - Writer → Manage
//   - Auditor → View
//   - All other roles (unknown/future) → Manage (intentional: err on the side
//     of access rather than silently blocking a legitimate user).
func mapRoleToInviteRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "writer":
		return string(inviteapi.InviteRoleManage)
	case "auditor":
		return string(inviteapi.InviteRoleView)
	default:
		// Unknown roles fall through to Manage. Log so any new role added later
		// is immediately visible rather than silently inheriting elevated access.
		slog.Warn("mapRoleToInviteRole: unrecognised role, defaulting to Manage", "role", role)
		return string(inviteapi.InviteRoleManage)
	}
}

// committeeUserKey returns a stable, normalized identity key for a CommitteeUser.
// LFID users are keyed by Username; non-LFID users fall back to a normalized email.
// Returns "" when both fields are empty (user cannot be identified).
func committeeUserKey(u model.CommitteeUser) string {
	if username := strings.TrimSpace(u.Username); username != "" {
		return "username:" + strings.ToLower(username)
	}
	if email := strings.ToLower(strings.TrimSpace(u.Email)); email != "" {
		return "email:" + email
	}
	return ""
}

// diffNewCommitteeUsers returns users in newList that were not in oldList.
// LFID users are matched by Username; non-LFID users are matched by normalized Email.
func diffNewCommitteeUsers(oldList, newList []model.CommitteeUser) []model.CommitteeUser {
	oldKeys := make(map[string]bool, len(oldList))
	for _, u := range oldList {
		if key := committeeUserKey(u); key != "" {
			oldKeys[key] = true
		}
	}
	var added []model.CommitteeUser
	for _, u := range newList {
		key := committeeUserKey(u)
		if key == "" || oldKeys[key] {
			continue
		}
		added = append(added, u)
	}
	return added
}

// wasInvitedInOldSettings returns true if the given email was already present in old settings
// in any form (LFID or email-only). This covers two cases:
//   - LFID promotion: old entry was email-only (invite), new entry has a username.
//   - Username scrub: old entry had both username and email; username was cleared, so
//     classifyCommitteeUsers sees the email-only entry as newly added. Without this guard
//     the scrub would re-invite the deleted user.
func wasInvitedInOldSettings(email string, old *model.CommitteeSettings) bool {
	if old == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	for _, u := range old.GetWriters() {
		if strings.ToLower(strings.TrimSpace(u.Email)) == normalized {
			return true
		}
	}
	for _, u := range old.GetAuditors() {
		if strings.ToLower(strings.TrimSpace(u.Email)) == normalized {
			return true
		}
	}
	return false
}

// buildCommitteeURL returns a deep link directly to the committee page.
func buildCommitteeURL(baseURL, committeeUID string) string {
	return strings.TrimRight(baseURL, "/") + "/project/groups/" + committeeUID
}
