// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type committeeMailingListHandler struct {
	committeeReader    CommitteeReader
	committeeWriter    port.CommitteeWriter
	committeePublisher port.CommitteePublisher
}

func NewCommitteeMailingListHandler(
	reader CommitteeReader,
	writer port.CommitteeWriter,
	publisher port.CommitteePublisher,
) port.CommitteeMailingListHandler {
	return &committeeMailingListHandler{
		committeeReader:    reader,
		committeeWriter:    writer,
		committeePublisher: publisher,
	}
}

func (h *committeeMailingListHandler) HandleCommitteeMailingListChanged(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var event model.CommitteeMailingListChangedEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		return nil, errors.NewValidation("failed to unmarshal CommitteeMailingListChangedEvent", err)
	}

	if event.CommitteeUID == "" {
		slog.WarnContext(ctx, "CommitteeMailingListChangedEvent received with empty committee_uid — discarding")
		return nil, nil
	}

	slog.InfoContext(ctx, "processing committee mailing list change",
		"committee_uid", event.CommitteeUID,
		"has_mailing_list", event.HasMailingList,
	)

	committee, changed, err := h.committeeWriter.UpdateHasMailingList(ctx, event.CommitteeUID, event.HasMailingList)
	if err != nil {
		return nil, err
	}
	if !changed {
		slog.DebugContext(ctx, "has_mailing_list already matches — skipping re-index",
			"committee_uid", event.CommitteeUID,
			"has_mailing_list", event.HasMailingList,
		)
		return nil, nil
	}

	fullCommittee := &model.Committee{CommitteeBase: *committee}
	if settings, _, errSettings := h.committeeReader.GetSettings(ctx, event.CommitteeUID); errSettings == nil {
		fullCommittee.CommitteeSettings = settings
	}

	indexerMsg, err := buildIndexerMessage(ctx, model.ActionUpdated, committee, fullCommittee.Tags())
	if err != nil {
		return nil, err
	}
	indexerMsg.IndexingConfig = buildCommitteeIndexingConfig(fullCommittee)

	if err := h.committeePublisher.Indexer(ctx, constants.IndexCommitteeSubject, indexerMsg, false); err != nil {
		return nil, err
	}

	return nil, nil
}
