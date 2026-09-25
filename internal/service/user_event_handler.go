// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

type userEventHandler struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	userReader                  port.UserReader
}

func NewUserEventHandler(
	reader CommitteeReader,
	writerOrch CommitteeWriter,
	userReader port.UserReader,
) *userEventHandler {
	return &userEventHandler{
		committeeReader:             reader,
		committeeWriterOrchestrator: writerOrch,
		userReader:                  userReader,
	}
}

type V1UserDeletedEvent struct {
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
}

func usernameMatches(deletedUsername, storedUsername string) bool {
	deleted := strings.TrimSpace(deletedUsername)
	stored := strings.TrimSpace(storedUsername)
	if deleted == "" || stored == "" {
		return false
	}
	return strings.EqualFold(deleted, stored)
}

func emailMatches(deletedEmail, storedEmail string) bool {
	deleted := strings.ToLower(strings.TrimSpace(deletedEmail))
	stored := strings.ToLower(strings.TrimSpace(storedEmail))
	if deleted == "" || stored == "" {
		return false
	}
	return deleted == stored
}

func (h *userEventHandler) HandleUserDeleted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event V1UserDeletedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal V1UserDeletedEvent — discarding", "error", err)
		return nil, nil
	}
	if strings.TrimSpace(event.Username) == "" {
		slog.WarnContext(ctx, "user.deleted event missing username — nothing to scrub")
		return nil, nil
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")
	ctx = log.AppendCtx(ctx, slog.String("username", redaction.Redact(event.Username)))

	slog.InfoContext(ctx, "scrubbing deleted user's username from committee data")

	h.scrubUsernameFromMembers(ctx, event.Username, event.Email)
	h.scrubUsernameFromSettings(ctx, event.Username, event.Email)
	return nil, nil
}

func (h *userEventHandler) scrubUsernameFromMembers(ctx context.Context, username, deletedEmail string) {
	if h.committeeReader == nil || h.committeeWriterOrchestrator == nil {
		slog.WarnContext(ctx, "committee reader/writer not configured — skipping member username scrub")
		return
	}

	members, err := h.committeeReader.ListMembersByUsername(ctx, username)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list committee members by username for scrub",
			"error", err, "username", redaction.Redact(username))
		return
	}

	slog.InfoContext(ctx, "scrubbing username from committee members",
		"username", redaction.Redact(username), "count", len(members))

	for _, member := range members {
		const maxRetries = 4
		for attempt := 0; attempt < maxRetries; attempt++ {
			current, revision, err := h.committeeReader.GetMember(ctx, member.CommitteeUID, member.UID)
			if err != nil {
				slog.ErrorContext(ctx, "failed to fetch committee member for username scrub",
					"error", err, "member_uid", member.UID, "committee_uid", member.CommitteeUID)
				break
			}
			if !usernameMatches(username, current.Username) {
				slog.DebugContext(ctx, "committee member username already cleared or reassigned — skipping",
					"member_uid", member.UID)
				break
			}
			if deletedEmail != "" {
				if current.Email == "" {
					slog.DebugContext(ctx, "committee member username matches but entry is email-less — skipping",
						"member_uid", member.UID)
					break
				}
				if !emailMatches(deletedEmail, current.Email) {
					slog.DebugContext(ctx, "committee member username matches but email differs — skipping reuse guard",
						"member_uid", member.UID)
					break
				}
			}

			current.Username = ""
			if _, err := h.committeeWriterOrchestrator.UpdateMember(ctx, current, revision, false, true); err != nil {
				var conflictErr errors.Conflict
				if stderrors.As(err, &conflictErr) && attempt < maxRetries-1 {
					slog.DebugContext(ctx, "revision conflict scrubbing member username — retrying",
						"attempt", attempt+1, "member_uid", member.UID)
					continue
				}
				slog.ErrorContext(ctx, "failed to clear username from committee member",
					"error", err, "member_uid", member.UID, "committee_uid", member.CommitteeUID)
				break
			}
			slog.InfoContext(ctx, "cleared username from committee member",
				"member_uid", member.UID, "committee_uid", member.CommitteeUID,
				"username", redaction.Redact(username))
			break
		}
	}
}

func (h *userEventHandler) scrubUsernameFromSettings(ctx context.Context, username, deletedEmail string) {
	if h.committeeReader == nil || h.committeeWriterOrchestrator == nil {
		slog.WarnContext(ctx, "committee reader/writer not configured — skipping settings username scrub")
		return
	}

	allUIDs, err := h.committeeReader.ListAllUIDs(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list committee UIDs for settings username scrub", "error", err)
		return
	}

	slog.InfoContext(ctx, "scrubbing username from committee settings",
		"username", redaction.Redact(username), "committee_count", len(allUIDs))

	for _, committeeUID := range allUIDs {
		h.scrubUsernameFromOneCommitteeSettings(ctx, committeeUID, username, deletedEmail)
	}
}

func (h *userEventHandler) scrubUsernameFromOneCommitteeSettings(ctx context.Context, committeeUID, username, deletedEmail string) {
	const maxRetries = 4
	for attempt := 0; attempt < maxRetries; attempt++ {
		settings, revision, err := h.committeeReader.GetSettings(ctx, committeeUID)
		if err != nil {
			slog.DebugContext(ctx, "failed to get settings for username scrub — skipping",
				"error", err, "committee_uid", committeeUID)
			return
		}
		if settings == nil {
			return
		}

		changed := false
		for i := range settings.Writers {
			w := &settings.Writers[i]
			if !usernameMatches(username, w.Username) {
				continue
			}
			if deletedEmail != "" {
				if w.Email == "" {
					continue
				}
				if !emailMatches(deletedEmail, w.Email) {
					continue
				}
			}
			w.Username = ""
			changed = true
		}
		for i := range settings.Auditors {
			a := &settings.Auditors[i]
			if !usernameMatches(username, a.Username) {
				continue
			}
			if deletedEmail != "" {
				if a.Email == "" {
					continue
				}
				if !emailMatches(deletedEmail, a.Email) {
					continue
				}
			}
			a.Username = ""
			changed = true
		}

		if !changed {
			return
		}

		if _, err := h.committeeWriterOrchestrator.UpdateSettings(ctx, settings, revision, false); err != nil {
			var conflictErr errors.Conflict
			if stderrors.As(err, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(ctx, "revision conflict scrubbing settings username — retrying",
					"attempt", attempt+1, "committee_uid", committeeUID)
				continue
			}
			slog.ErrorContext(ctx, "failed to clear username from committee settings",
				"error", err, "committee_uid", committeeUID)
			return
		}

		slog.InfoContext(ctx, "cleared username from committee settings writers/auditors",
			"committee_uid", committeeUID, "username", redaction.Redact(username))
		return
	}
}

// HandleUserEmailChanged reacts to a user-email change event from the durable user-email-events
// stream. For alternate_email_added events it promotes all email-only committee member seats
// associated with the address to the resolved LFID username via reconcileUsernamesForEmail.
// Other event types are logged and ACKed; the same helper can be wired to them as the
// corresponding teardown semantics are defined. The caller (infrastructure layer) owns ACK/NAK.
func (h *userEventHandler) HandleUserEmailChanged(ctx context.Context, msg port.StreamMessenger) error {
	subject := msg.Subject()

	if subject != constants.UserEmailChangedSubject {
		slog.DebugContext(ctx, "stream message subject not relevant for user-email sync — skipping",
			"subject", subject,
		)
		return nil
	}

	var event model.UserEmailEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.WarnContext(ctx, "user-email event has malformed payload — discarding",
			"error", err,
			"subject", subject,
		)
		return nil
	}

	if event.Email == "" {
		slog.WarnContext(ctx, "user-email event missing email — discarding",
			"subject", subject,
			"event_type", event.Type,
		)
		return nil
	}

	slog.InfoContext(ctx, "received user-email change event",
		"event_type", event.Type,
		"email", redaction.RedactEmail(event.Email),
		"timestamp", event.Timestamp,
	)

	switch event.Type {
	case model.UserEmailEventAlternateEmailAdded:
		return h.reconcileUsernamesForEmail(ctx, event.Email)
	default:
		slog.DebugContext(ctx, "user-email event type not yet handled — skipping",
			"event_type", event.Type,
		)
		return nil
	}
}

// reconcileUsernamesForEmail looks up all email-only committee member seats matching email,
// resolves the LFID username once via the auth service, and promotes each seat via UpdateMember.
// It is called for event types where an email gains a resolvable username (e.g. alternate_email_added).
func (h *userEventHandler) reconcileUsernamesForEmail(ctx context.Context, email string) error {
	if h.committeeReader == nil || h.userReader == nil || h.committeeWriterOrchestrator == nil {
		return errors.NewValidation("committeeReader, userReader, and committeeWriterOrchestrator are required for user-email sync")
	}

	// Cheap index lookup first — avoids a needless auth round-trip when no seats match.
	members, err := h.committeeReader.ListMembersByEmail(ctx, email)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list committee members by email for user-email sync",
			"email", redaction.RedactEmail(email),
			"error", err,
		)
		return err
	}

	var emailOnlySeats []*model.CommitteeMember
	for _, member := range members {
		if member.Username == "" {
			emailOnlySeats = append(emailOnlySeats, member)
		}
	}

	if len(emailOnlySeats) == 0 {
		slog.DebugContext(ctx, "no email-only committee seats found for email — skipping",
			"email", redaction.RedactEmail(email),
		)
		return nil
	}

	username, err := h.userReader.UsernameByEmail(ctx, email)
	if err != nil {
		var notFound errors.NotFound
		if stderrors.As(err, &notFound) {
			// For alternate_email_added the LFID must exist; NotFound means the auth index
			// has not caught up yet. Return the error so JetStream retries rather than
			// ACKing permanently and losing this reconciliation.
			slog.WarnContext(ctx, "email not yet visible to auth lookup — NAKing for JetStream retry",
				"email", redaction.RedactEmail(email),
			)
			return err
		}
		slog.ErrorContext(ctx, "failed to resolve username by email during user-email sync",
			"email", redaction.RedactEmail(email),
			"error", err,
		)
		return err
	}

	if username == "" {
		slog.DebugContext(ctx, "email not resolvable to an LFID yet — leaving seats email-only",
			"email", redaction.RedactEmail(email),
		)
		return nil
	}

	writeCtx := context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	var syncErrors []error
	for _, seat := range emailOnlySeats {
		if promoteErr := h.promoteEmailOnlyMemberUsername(writeCtx, seat.CommitteeUID, seat.UID, username, email); promoteErr != nil {
			syncErrors = append(syncErrors, promoteErr)
		}
	}

	if len(syncErrors) > 0 {
		slog.ErrorContext(ctx, "user-email sync completed with errors",
			"email", redaction.RedactEmail(email),
			"username", redaction.Redact(username),
			"total_seats", len(emailOnlySeats),
			"failed_seats", len(syncErrors),
		)
	}

	return stderrors.Join(syncErrors...)
}

// promoteEmailOnlyMemberUsername sets the username on an email-only committee member seat and
// persists it via UpdateMember. It retries on revision conflicts (up to maxRetries), skips seats
// that are already promoted or whose email no longer matches (index lag), and passes
// skipEnrichment=true so UpdateMember does not re-run the auth lookup.
func (h *userEventHandler) promoteEmailOnlyMemberUsername(writeCtx context.Context, committeeUID, memberUID, username, email string) error {
	const maxRetries = 3
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))

	for attempt := 0; attempt < maxRetries; attempt++ {
		member, revision, err := h.committeeReader.GetMember(writeCtx, committeeUID, memberUID)
		if err != nil {
			slog.WarnContext(writeCtx, "failed to get member for username promotion",
				"error", err,
				"committee_uid", committeeUID,
				"member_uid", memberUID,
			)
			return err
		}

		// Skip if already promoted or if the email no longer matches (index may lag an email change).
		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != normalizedEmail {
			return nil
		}

		member.Username = username

		updated, writeErr := h.committeeWriterOrchestrator.UpdateMember(
			writeCtx,
			member, revision, false, true,
		)
		if writeErr != nil {
			var conflictErr errors.Conflict
			if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(writeCtx, "revision conflict promoting member username — retrying",
					"attempt", attempt+1,
					"committee_uid", committeeUID,
					"member_uid", memberUID,
				)
				continue
			}
			slog.ErrorContext(writeCtx, "failed to promote email-only member to LFID",
				"error", writeErr,
				"committee_uid", committeeUID,
				"member_uid", memberUID,
			)
			return writeErr
		}

		persistedUsername := username
		if updated != nil {
			persistedUsername = updated.Username
		}
		slog.DebugContext(writeCtx, "user-email sync — promoted email-only member to LFID",
			"committee_uid", committeeUID,
			"member_uid", memberUID,
			"username", redaction.Redact(persistedUsername),
		)
		return nil
	}

	// Unreachable: the final iteration always returns (conflict on the last attempt falls through
	// to return writeErr above). Required by the compiler for a bounded for loop.
	return nil
}
