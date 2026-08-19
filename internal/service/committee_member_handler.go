// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
)

type committeeMemberHandler struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	committeeWriter             port.CommitteeWriter
	committeePublisher          port.CommitteePublisher
}

func NewCommitteeMemberHandler(
	reader CommitteeReader,
	writerOrch CommitteeWriter,
	writer port.CommitteeWriter,
	publisher port.CommitteePublisher,
) port.CommitteeMemberHandler {
	return &committeeMemberHandler{
		committeeReader:             reader,
		committeeWriterOrchestrator: writerOrch,
		committeeWriter:             writer,
		committeePublisher:          publisher,
	}
}

func (h *committeeMemberHandler) HandleCommitteeListMembers(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	uid := string(msg.Data())

	ctx = log.AppendCtx(ctx, slog.String("committee_uid", uid))
	slog.DebugContext(ctx, "executing list committee members use case")

	if _, err := uuid.Parse(uid); err != nil {
		return nil, errors.NewValidation("invalid committee UID in list_members request", err)
	}

	_, _, err := h.committeeReader.GetBase(ctx, uid)
	if err != nil {
		return nil, err
	}

	members, err := h.committeeReader.ListMembersByCommittee(ctx, uid)
	if err != nil {
		return nil, err
	}

	membersJSON, err := json.Marshal(members)
	if err != nil {
		return nil, errors.NewUnexpected("failed to marshal committee members", err)
	}

	slog.DebugContext(ctx, "committee members retrieved successfully", "member_count", len(members))

	return membersJSON, nil
}

func (h *committeeMemberHandler) HandleCommitteeUpdated(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	if h.committeeWriterOrchestrator == nil {
		return nil, errors.NewValidation("committee writer orchestrator is required for handling committee_updated events")
	}

	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		return nil, errors.NewValidation("failed to unmarshal CommitteeEvent in committee_updated handler", err)
	}

	rawData, errMarshal := json.Marshal(event.Data)
	if errMarshal != nil {
		slog.WarnContext(ctx, "CommitteeUpdated event has unexpected data shape — discarding", "error", errMarshal)
		return nil, nil
	}
	var data model.CommitteeUpdateEventData
	if err := json.Unmarshal(rawData, &data); err != nil {
		slog.WarnContext(ctx, "CommitteeUpdated event data cannot be decoded — discarding", "error", err)
		return nil, nil
	}

	if !data.RequiresMemberSync() {
		slog.DebugContext(ctx, "no denormalized fields changed — skipping member sync",
			"committee_uid", data.CommitteeUID)
		return nil, nil
	}

	if data.CommitteeUID == "" {
		slog.WarnContext(ctx, "CommitteeUpdated event missing committee_uid — discarding")
		return nil, nil
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	slog.InfoContext(ctx, "denormalized fields changed — syncing members",
		"committee_uid", data.CommitteeUID)

	members, err := h.committeeReader.ListMembersByCommittee(ctx, data.CommitteeUID)
	if err != nil {
		return nil, err
	}

	var syncErrors []error

	for _, member := range members {
		if !member.NeedsSyncWith(data.Committee) {
			slog.DebugContext(ctx, "member already up to date — skipping",
				"member_uid", member.UID, "committee_uid", data.CommitteeUID)
			continue
		}

		member.CommitteeName = data.Committee.Name
		member.CommitteeCategory = data.Committee.Category
		member.ProjectUID = data.Committee.ProjectUID
		member.ProjectSlug = data.Committee.ProjectSlug

		revision, errRev := h.committeeReader.GetMemberRevision(ctx, member.UID)
		if errRev != nil {
			slog.ErrorContext(ctx, "failed to get member revision during sync",
				"member_uid", member.UID, "committee_uid", data.CommitteeUID, "error", errRev)
			syncErrors = append(syncErrors, errRev)
			continue
		}

		if _, errUpdate := h.committeeWriterOrchestrator.UpdateMember(ctx, member, revision, false, false); errUpdate != nil {
			slog.ErrorContext(ctx, "failed to update member during sync",
				"member_uid", member.UID, "committee_uid", data.CommitteeUID, "error", errUpdate)
			syncErrors = append(syncErrors, errUpdate)
		}
	}

	slog.InfoContext(ctx, "member sync completed",
		"committee_uid", data.CommitteeUID,
		"members_processed", len(members),
		"failures", len(syncErrors))

	if len(syncErrors) > 0 {
		return nil, stderrors.Join(syncErrors...)
	}

	return nil, nil
}

func (h *committeeMemberHandler) HandleCommitteeTotalMembersSync(ctx context.Context, msg port.StreamMessenger) error {
	if h.committeeWriter == nil || h.committeeReader == nil || h.committeePublisher == nil {
		return errors.NewValidation("committee writer, reader, and publisher are required for handling total_members sync events")
	}

	subject := msg.Subject()

	if subject != constants.CommitteeMemberCreatedSubject && subject != constants.CommitteeMemberDeletedSubject {
		slog.DebugContext(ctx, "stream message subject not relevant for total_members sync — skipping",
			"subject", subject,
		)
		return nil
	}

	var event model.CommitteeEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		return errors.NewValidation("failed to unmarshal CommitteeEvent in total_members_sync handler", err)
	}

	rawData, err := json.Marshal(event.Data)
	if err != nil {
		slog.WarnContext(ctx, "total_members sync event has unexpected data shape — discarding", "error", err)
		return nil
	}

	var member model.CommitteeMember
	if err := json.Unmarshal(rawData, &member); err != nil {
		slog.WarnContext(ctx, "total_members sync event data cannot be decoded — discarding", "error", err)
		return nil
	}

	if member.CommitteeUID == "" {
		slog.WarnContext(ctx, "total_members sync event missing committee_uid — discarding")
		return nil
	}

	committeeUID := member.CommitteeUID

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")
	ctx = log.AppendCtx(ctx, slog.String("committee_uid", committeeUID))

	slog.DebugContext(ctx, "starting total_members sync", "subject", subject)

	members, err := h.committeeReader.ListMembersByCommittee(ctx, committeeUID)
	if err != nil {
		return err
	}
	actualCount := len(members)

	committee, changed, err := h.committeeWriter.UpdateTotalMembers(ctx, committeeUID, actualCount)
	if err != nil {
		return err
	}
	if !changed {
		slog.DebugContext(ctx, "total_members already correct — re-indexing in case of a prior incomplete delivery", "total_members", actualCount)
		base, _, err := h.committeeReader.GetBase(ctx, committeeUID)
		if err != nil {
			return err
		}
		committee = base
	} else {
		slog.DebugContext(ctx, "updated total_members counter", "total_members", actualCount)
	}

	fullCommittee := &model.Committee{CommitteeBase: *committee}
	if settings, _, errSettings := h.committeeReader.GetSettings(ctx, committeeUID); errSettings == nil {
		fullCommittee.CommitteeSettings = settings
	}

	indexerMsg, err := buildIndexerMessage(ctx, model.ActionUpdated, committee, fullCommittee.Tags())
	if err != nil {
		return err
	}
	indexerMsg.IndexingConfig = buildCommitteeIndexingConfig(fullCommittee)

	if err := h.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, indexerMsg, false); err != nil {
		return err
	}

	return nil
}
