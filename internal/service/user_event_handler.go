// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

type userEventHandler struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
}

func NewUserEventHandler(
	reader CommitteeReader,
	writerOrch CommitteeWriter,
) port.UserEventHandler {
	return &userEventHandler{
		committeeReader:             reader,
		committeeWriterOrchestrator: writerOrch,
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
