// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	emailsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service/email"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"golang.org/x/sync/errgroup"
)

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
func (m *messageHandlerOrchestrator) HandleCommitteeDocumentCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
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
		folderName:        m.resolveFolderName(ctx, doc.CommitteeUID, doc.FolderUID),
		createdByUsername: doc.UploadedByUsername,
	}

	m.handleContentCreated(ctx, item)
	return nil, nil
}

// HandleCommitteeLinkCreated handles committee_link.created events and notifies all
// LFID members, writers, and auditors of the committee. Best-effort: errors are logged, not returned.
func (m *messageHandlerOrchestrator) HandleCommitteeLinkCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
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
		folderName:        m.resolveFolderName(ctx, link.CommitteeUID, link.FolderUID),
		createdByUsername: link.CreatedByUsername,
	}

	m.handleContentCreated(ctx, item)
	return nil, nil
}

// handleContentCreated fans out notification emails to all LFID members, writers, and auditors
// of the committee. Best-effort: individual send failures are logged but never abort the batch.
func (m *messageHandlerOrchestrator) handleContentCreated(ctx context.Context, item committeeContentItem) {
	if m.committeeReader == nil || m.emailSender == nil {
		slog.DebugContext(ctx, "committee reader or email sender not configured — skipping content notification",
			"committee_uid", item.committeeUID)
		return
	}

	committee, _, err := m.committeeReader.GetBase(ctx, item.committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load committee for content notification",
			"error", err, "committee_uid", item.committeeUID)
		return
	}

	recipients := m.collectCommitteeRecipients(ctx, item.committeeUID)
	if len(recipients) == 0 {
		slog.DebugContext(ctx, "no LFID recipients for content notification", "committee_uid", item.committeeUID)
		return
	}

	// Only resolve the uploader display name when a principal is present; otherwise
	// leave it empty so the template renders the generic "A new X was added" message.
	var uploaderName string
	if item.createdByUsername != "" {
		uploaderName = m.resolveDisplayNameWithTimeout(ctx, item.createdByUsername)
	}
	committeeURL := buildCommitteeURL(m.lfxSelfServeBaseURL, item.committeeUID)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, r := range recipients {
		recipient := r
		g.Go(func() error {
			email := recipient.email
			if email == "" && m.userReader != nil && recipient.username != "" {
				lookupCtx, cancel := context.WithTimeout(gctx, committeeNotificationTimeout)
				emails, lookupErr := m.userReader.EmailsByPrincipal(lookupCtx, recipient.username)
				cancel()
				if lookupErr == nil && emails != nil && emails.PrimaryEmail != "" {
					email = emails.PrimaryEmail
				}
			}
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
			if sendErr := m.emailSender.SendEmail(sendCtx, emailapi.SendEmailRequest{
				To:      email,
				Subject: emailSubject,
				HTML:    emailHTML,
				Text:    emailText,
			}); sendErr != nil {
				slog.WarnContext(gctx, "failed to send content notification email",
					"error", sendErr, "committee_uid", item.committeeUID)
			} else {
				slog.DebugContext(gctx, "sent content notification email",
					"committee_uid", item.committeeUID, "document_type", item.documentType)
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
func (m *messageHandlerOrchestrator) collectCommitteeRecipients(ctx context.Context, committeeUID string) []notificationRecipient {
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

	members, err := m.committeeReader.ListMembersByCommittee(ctx, committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to list members for content notification",
			"error", err, "committee_uid", committeeUID)
	} else {
		for _, member := range members {
			add(member.Username, member.Email, strings.TrimSpace(member.FirstName+" "+member.LastName))
		}
	}

	settings, _, err := m.committeeReader.GetSettings(ctx, committeeUID)
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
func (m *messageHandlerOrchestrator) resolveDisplayNameWithTimeout(ctx context.Context, principal string) string {
	lookupCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	return m.resolveDisplayName(lookupCtx, principal)
}

// resolveFolderName looks up the display name of a link folder by UID.
// Returns an empty string if the UID is nil/empty, no link reader is configured, or the lookup fails.
func (m *messageHandlerOrchestrator) resolveFolderName(ctx context.Context, committeeUID string, folderUID *string) string {
	if folderUID == nil || *folderUID == "" || m.linkReader == nil {
		return ""
	}
	lookupCtx, cancel := context.WithTimeout(ctx, committeeNotificationTimeout)
	defer cancel()
	folder, _, err := m.linkReader.GetLinkFolder(lookupCtx, committeeUID, *folderUID)
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
