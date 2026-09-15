// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	committeeapi "github.com/linuxfoundation/lfx-v2-committee-service/pkg/api"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
)

type committeeAttributeHandler struct {
	committeeReader CommitteeReader
}

func NewCommitteeAttributeHandler(reader CommitteeReader) port.CommitteeAttributeHandler {
	return &committeeAttributeHandler{
		committeeReader: reader,
	}
}

func (h *committeeAttributeHandler) HandleCommitteeGetAttribute(ctx context.Context, msg port.TransportMessenger, attribute string) ([]byte, error) {
	uid := string(msg.Data())

	ctx = log.AppendCtx(ctx, slog.String("committee_uid", uid))
	ctx = log.AppendCtx(ctx, slog.String("attribute", attribute))
	slog.DebugContext(ctx, "committee get attribute request")

	_, err := uuid.Parse(uid)
	if err != nil {
		return nil, errs.NewValidation(fmt.Sprintf("invalid committee UID %q in get_attribute request", uid), err)
	}

	committee, _, err := h.committeeReader.GetBase(ctx, uid)
	if err != nil {
		return nil, err
	}

	value, ok := fields.LookupByTag(committee, "json", attribute)
	if !ok {
		return nil, errs.NewNotFound(fmt.Sprintf("attribute %s not found in committee %s", attribute, uid))
	}

	strValue, ok := value.(string)
	if !ok {
		return nil, errs.NewValidation(fmt.Sprintf("attribute %s value is not a string", attribute))
	}

	return []byte(strValue), nil
}

func (h *committeeAttributeHandler) HandleCommitteeGetProject(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var req committeeapi.GetCommitteeProjectRequest
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal get_project request", "error", err)
		return nil, errs.NewValidation("invalid get_project request payload")
	}

	slog.DebugContext(ctx, "committee get project request", "committee_uid", req.CommitteeUID)

	if _, err := uuid.Parse(req.CommitteeUID); err != nil {
		slog.ErrorContext(ctx, "invalid committee UID in get_project request", "error", err, "committee_uid", req.CommitteeUID)
		return nil, errs.NewValidation("invalid committee UID", err)
	}

	committee, _, err := h.committeeReader.GetBase(ctx, req.CommitteeUID)
	if err != nil {
		var nf errs.NotFound
		if stderrors.As(err, &nf) {
			slog.DebugContext(ctx, "committee not found for get_project request", "committee_uid", req.CommitteeUID)
			return json.Marshal(committeeapi.GetCommitteeProjectResponse{Error: "not found"})
		}
		slog.ErrorContext(ctx, "failed to get committee base for get_project request",
			"error", err,
			"committee_uid", req.CommitteeUID,
		)
		return nil, err
	}

	slog.DebugContext(ctx, "committee get project response",
		"committee_uid", req.CommitteeUID,
		"project_uid", committee.ProjectUID,
	)

	return json.Marshal(committeeapi.GetCommitteeProjectResponse{ProjectUID: committee.ProjectUID})
}

func (h *committeeAttributeHandler) HandleCommitteeExists(ctx context.Context, msg port.TransportMessenger) ([]byte, error) {
	var req committeeapi.CommitteeExistsRequest
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal exists request", "error", err)
		return nil, errs.NewValidation("invalid exists request payload")
	}

	ctx = log.AppendCtx(ctx, slog.String("project_uid", req.ProjectUID))
	ctx = log.AppendCtx(ctx, slog.String("name", req.Name))
	slog.DebugContext(ctx, "committee exists request")

	if _, err := uuid.Parse(req.ProjectUID); err != nil {
		slog.ErrorContext(ctx, "invalid project UID in exists request", "error", err, "project_uid", req.ProjectUID)
		return nil, errs.NewValidation("invalid project UID", err)
	}

	if req.Name == "" {
		return nil, errs.NewValidation("name is required in exists request")
	}

	committeeUID, err := h.committeeReader.FindUIDByProjectAndName(ctx, req.ProjectUID, req.Name)
	if err != nil {
		var nf errs.NotFound
		if stderrors.As(err, &nf) {
			slog.DebugContext(ctx, "no committee found for exists request")
			return json.Marshal(committeeapi.CommitteeExistsResponse{Exists: false})
		}
		slog.ErrorContext(ctx, "failed to look up committee by project and name for exists request",
			"error", err,
			"project_uid", req.ProjectUID,
		)
		return nil, err
	}

	slog.DebugContext(ctx, "committee exists response", "committee_uid", committeeUID)

	return json.Marshal(committeeapi.CommitteeExistsResponse{Exists: true, CommitteeUID: committeeUID})
}
