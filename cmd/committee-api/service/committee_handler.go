// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	committeeapi "github.com/linuxfoundation/lfx-v2-committee-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
)

// MessageHandlerService handles NATS messages using the service layer
type MessageHandlerService struct {
	messageHandler port.MessageHandler
}

// HandleMessage routes NATS messages to appropriate handlers
func (mhs *MessageHandlerService) HandleMessage(ctx context.Context, msg port.TransportMessenger) {
	subject := msg.Subject()
	ctx = log.AppendCtx(ctx, slog.String("subject", subject))

	slog.DebugContext(ctx, "handling NATS message")

	handlers := map[string]func(ctx context.Context, msg port.TransportMessenger) ([]byte, error){
		constants.CommitteeGetNameSubject:              mhs.handleCommitteeGetName,
		constants.CommitteeListMembersSubject:          mhs.handleCommitteeListMembers,
		constants.CommitteeGetProjectSubject:           mhs.handleCommitteeGetProject,
		constants.CommitteeExistsSubject:               mhs.handleCommitteeExists,
		constants.MailingListCommitteeChangedSubject:   mhs.handleMailingListChanged,
		constants.CommitteeUpdatedSubject:              mhs.handleCommitteeUpdated,
		constants.CommitteeMemberCreatedSubject:        mhs.handleCommitteeMemberCreated,
		constants.CommitteeMemberDeletedSubject:        mhs.handleCommitteeMemberDeleted,
		constants.CommitteeSettingsUpdatedSubject:      mhs.handleCommitteeSettingsUpdated,
		inviteapi.InviteServiceAcceptedSubject:         mhs.handleInviteAccepted,
		constants.CommitteeDocumentCreatedSubject:      mhs.handleCommitteeDocumentCreated,
		constants.CommitteeLinkCreatedSubject:          mhs.handleCommitteeLinkCreated,
		constants.CommitteeApplicationSubmittedSubject: mhs.handleCommitteeApplicationSubmitted,
		constants.CommitteeApplicationUpdatedSubject:   mhs.handleCommitteeApplicationUpdated,
		constants.V1SyncHelperUserDeletedSubject:       mhs.handleUserDeleted,
	}

	handler, ok := handlers[subject]
	if !ok {
		slog.WarnContext(ctx, "unknown subject")
		mhs.respondWithError(ctx, msg, "unknown subject")
		return
	}

	response, errHandler := handler(ctx, msg)
	if errHandler != nil {
		slog.ErrorContext(ctx, "error handling message",
			"error", errHandler,
			"subject", subject,
		)
		mhs.respondWithError(ctx, msg, errHandler.Error())
		return
	}

	// Skip respond for fire-and-forget events (no reply address)
	if response == nil {
		return
	}

	errRespond := msg.Respond(response)
	if errRespond != nil {
		slog.ErrorContext(ctx, "error responding to NATS message", "error", errRespond)
		return
	}

	slog.DebugContext(ctx, "responded to NATS message", "response", string(response))
}

func (mhs *MessageHandlerService) handleCommitteeGetName(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeGetAttribute(ctx, msg, "name")
}

func (mhs *MessageHandlerService) handleCommitteeListMembers(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeListMembers(ctx, msg)
}

func (mhs *MessageHandlerService) handleMailingListChanged(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeMailingListChanged(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeUpdated(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeMemberCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeMemberCreated(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeMemberDeleted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeMemberDeleted(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeSettingsUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeSettingsUpdated(ctx, msg)
}

func (mhs *MessageHandlerService) handleInviteAccepted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleInviteAccepted(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeDocumentCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeDocumentCreated(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeLinkCreated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeLinkCreated(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeApplicationSubmitted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeApplicationSubmitted(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeApplicationUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeApplicationUpdated(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeGetProject(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeGetProject(ctx, msg)
}

func (mhs *MessageHandlerService) handleCommitteeExists(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleCommitteeExists(ctx, msg)
}

func (mhs *MessageHandlerService) handleUserDeleted(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	return mhs.messageHandler.HandleUserDeleted(ctx, msg)
}

func (mhs *MessageHandlerService) respondWithError(ctx context.Context, msg port.TransportMessenger, errorMsg string) {
	// CommitteeExistsSubject is documented to always reply with CommitteeExistsResponse
	// (Exists is non-omitempty), so error replies for it must preserve that shape rather
	// than the generic {"error":...} envelope used for other subjects.
	var payload any
	if msg.Subject() == constants.CommitteeExistsSubject {
		payload = committeeapi.CommitteeExistsResponse{Exists: false, Error: errorMsg}
	} else {
		payload = struct {
			Error string `json:"error"`
		}{Error: errorMsg}
	}

	errResponse, err := json.Marshal(payload)
	if err != nil {
		// errorMsg is always a plain string, so this should be unreachable; fall back
		// to a static, always-valid payload rather than risk emitting broken JSON.
		slog.ErrorContext(ctx, "failed to marshal error response", "error", err)
		errResponse = []byte(`{"error":"internal error"}`)
	}
	if err := msg.Respond(errResponse); err != nil {
		slog.ErrorContext(ctx, "failed to send error response", "error", err)
	}
}

// NewMessageHandlerService creates a new message handler service
func NewMessageHandlerService(messageHandler port.MessageHandler) *MessageHandlerService {
	return &MessageHandlerService{
		messageHandler: messageHandler,
	}
}
