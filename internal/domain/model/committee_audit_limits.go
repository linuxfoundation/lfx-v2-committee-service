// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"strings"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexconstants "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// CommitteeAuditMaxAuthorizationBytes includes the Bearer prefix. Capture reserves
// this same bound that the refreshed credential source and dispatcher enforce.
const CommitteeAuditMaxAuthorizationBytes = 64 * 1024

// CommitteeAuditMaxResourceBytes caps each closed snapshot/current projection,
// leaving room for both snapshots, projection, actor evidence and dispatch auth.
// It applies at adoption and audited source writes, not the legacy API contract.
const CommitteeAuditMaxResourceBytes = 192 * 1024

func validateCommitteeAuditResourceSize(value any) error {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > CommitteeAuditMaxResourceBytes {
		return errs.NewValidation("committee audit resource exceeds the update-safe source budget; repair oversized source data before adoption")
	}
	return nil
}

func checkedCommitteeAuditProjectionSize(value any) (any, error) {
	if err := validateCommitteeAuditResourceSize(value); err != nil {
		return nil, err
	}
	return value, nil
}

// validateCommitteeAuditUpdateBudget rejects adoption/creation that would fit
// only without a before snapshot. It uses the actual tags, attribution and config
// as well as both snapshots and the maximum dispatch authorization allowance.
func validateCommitteeAuditUpdateBudget(m CommitteeAuditMutation, after json.RawMessage, data any) error {
	if m.Action != ActionCreated {
		return nil
	}
	schema, err := committeeAuditSchema(m.ObjectType)
	if err != nil {
		return err
	}
	content, err := indexertypes.NewAuditSnapshots(schema, after, after)
	if err != nil {
		return err
	}
	message, err := indexertypes.NewMessageV2(string(m.ObjectType), indexertypes.MessageV2Options{
		Action: indexconstants.ActionUpdated, Data: data, Tags: m.Tags, IndexingConfig: m.IndexingConfig,
		Audit: indexertypes.AuditEvent{Publisher: CommitteeAuditPublisher, EventID: m.EventID, OccurredAt: m.OccurredAt,
			ResourceRevision: m.Revision, Origin: indexertypes.AuditOriginMutation, Attribution: m.Attribution,
			Scope: indexertypes.AuditScope{Before: m.Scope.After, After: m.Scope.After}, Content: content},
	}, ValidateCommitteeAuditContent)
	if err != nil {
		return errs.NewValidation("committee source cannot fit a complete future audit update; repair before adoption")
	}
	return PreflightCommitteeAuditMessage(message, m.ObjectType)
}

// PreflightCommitteeAuditMessage validates a frozen event without writing state.
// The maximum-size safe placeholder reserves dispatch authentication overhead;
// no actual relay credential is needed or stored by source capture or preview.
func PreflightCommitteeAuditMessage(message *indexertypes.MessageV2, kind ResourceType) error {
	if message == nil {
		return errs.NewValidation("a frozen committee audit event is required")
	}
	authorization := "Bearer " + strings.Repeat("a", CommitteeAuditMaxAuthorizationBytes-len("Bearer "))
	envelope, err := message.Envelope(authorization, ValidateCommitteeAuditContent)
	if err != nil {
		return errs.NewValidation("committee audit event exceeds dispatch limits or is invalid")
	}
	if err := ValidateCommitteeAuditEnvelope(envelope, kind); err != nil {
		return errs.NewValidation("invalid committee audit context")
	}
	return nil
}
