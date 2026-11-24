// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	indexerconstants "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// V1AuditImportPublisher is the indexer publisher namespace for historical v1
// imports. It is separate from the live committee-service publisher and is only
// approved for the historical_import origin.
const V1AuditImportPublisher = "committee-v1-import"

// v1AuditAuthorizationAllowance reserves room in the 1 MiB wire limit for the
// dispatch credential, which is attached only when an event is sent.
const v1AuditAuthorizationAllowance = 64 << 10

// v1AuditMaxFrozenBytes bounds one frozen import event without its credential.
const v1AuditMaxFrozenBytes = indexertypes.MaxAuditEnvelopeBytes - v1AuditAuthorizationAllowance

// errV1AuditImportTooLarge marks a frozen event over v1AuditMaxFrozenBytes, so
// the planner can report the size limit rather than a generic rejection.
var errV1AuditImportTooLarge = errors.New("historical event exceeds the indexer size limit")

// V1AuditImportEvent is one frozen, validated historical_import event: the
// indexer version 2 envelope without transport headers, and the resource type
// (the lfx.index.<type> subject) it must be sent under. It carries personal audit
// data and no credential.
type V1AuditImportEvent struct {
	ObjectType string          `json:"object_type"`
	Event      json.RawMessage `json:"event"`
}

// BuildV1AuditImportEvent freezes a planned historical event as an indexer
// version 2 historical_import message. Every value comes from the source plan;
// nothing is generated, so rebuilding the same plan gives identical bytes and
// therefore the same indexer fingerprint. Data and tags are not stored by the
// indexer for imports, but they are part of that fingerprint, so they are fixed.
func BuildV1AuditImportEvent(event model.V1AuditPlanEvent) (*V1AuditImportEvent, error) {
	attribution, err := indexertypes.NewAuditAttribution(indexertypes.AuditActor{Ref: event.ActorRef}, nil)
	if err != nil {
		return nil, errs.NewValidation("historical actor is not a valid indexer actor reference")
	}
	var before, after json.RawMessage
	if event.Before != nil {
		if before, err = json.Marshal(event.Before); err != nil {
			return nil, errs.NewValidation("historical snapshot is not encodable")
		}
	}
	if event.After != nil {
		if after, err = json.Marshal(event.After); err != nil {
			return nil, errs.NewValidation("historical snapshot is not encodable")
		}
	}
	if len(before)+len(after) > v1AuditMaxFrozenBytes {
		return nil, errs.NewValidation("historical event exceeds the indexer size limit", errV1AuditImportTooLarge)
	}
	content, err := indexertypes.NewAuditSnapshots(event.Schema, before, after)
	if err != nil {
		return nil, errs.NewValidation("historical snapshots are not valid indexer content")
	}

	// Access mirrors the live index documents: committee and member documents are
	// visible to committee viewers, settings only to committee auditors.
	accessRelation := "viewer"
	if event.ResourceType == model.ResourceCommitteeSettings {
		accessRelation = "auditor"
	}
	var data any = map[string]any{"uid": event.ResourceUID}
	if event.Action == model.ActionDeleted {
		data = event.ResourceUID
	}
	message, err := indexertypes.NewMessageV2(string(event.ResourceType), indexertypes.MessageV2Options{
		Action: indexerconstants.MessageAction(event.Action),
		Data:   data,
		IndexingConfig: &indexertypes.IndexingConfig{
			ObjectID:             event.ResourceUID,
			AccessCheckObject:    event.HistoryCheckObject,
			AccessCheckRelation:  accessRelation,
			HistoryCheckObject:   event.HistoryCheckObject,
			HistoryCheckRelation: event.HistoryCheckRelation,
		},
		Audit: indexertypes.AuditEvent{
			EventID:          event.EventID,
			Publisher:        V1AuditImportPublisher,
			OccurredAt:       event.OccurredAt,
			ResourceRevision: event.ResourceRevision,
			Origin:           indexertypes.AuditOriginHistoricalImport,
			Attribution:      attribution,
			Scope:            indexertypes.AuditScope{Before: event.ScopeBefore, After: event.ScopeAfter},
			Content:          content,
		},
	}, ValidateV1AuditImportContent)
	if err != nil {
		return nil, errs.NewValidation("historical event is not a valid indexer import message")
	}
	frozen, err := message.MarshalJSON()
	if err != nil {
		return nil, errs.NewValidation("historical event is not encodable")
	}
	if len(frozen) > v1AuditMaxFrozenBytes {
		return nil, errs.NewValidation("historical event exceeds the indexer size limit", errV1AuditImportTooLarge)
	}
	return &V1AuditImportEvent{ObjectType: string(event.ResourceType), Event: frozen}, nil
}

// RestoreV1AuditImportEvent re-validates a frozen import event, for example one
// read back from a private draft file before dispatch.
func RestoreV1AuditImportEvent(event V1AuditImportEvent) (*indexertypes.MessageV2, error) {
	if model.V1AuditSchema(model.ResourceType(event.ObjectType)) == "" {
		return nil, errs.NewValidation("unsupported historical import resource type")
	}
	message, err := indexertypes.RestoreMessageV2(event.Event, event.ObjectType, ValidateV1AuditImportContent)
	if err != nil {
		return nil, errs.NewValidation("frozen historical event is not a valid indexer import message")
	}
	return message, nil
}

// ValidateV1AuditImportContent is the closed content validator for the import
// publisher's *.legacy.v1 schemas. It accepts only that publisher, the schema of
// the subject's resource type and snapshots that pass ValidateV1AuditSnapshot.
// It is deterministic and never echoes snapshot values in errors.
func ValidateV1AuditImportContent(publisher, objectType string, content indexertypes.AuditContent) error {
	resource := model.ResourceType(objectType)
	if publisher != V1AuditImportPublisher || content.Schema == "" || content.Schema != model.V1AuditSchema(resource) ||
		content.Representation != indexertypes.AuditRepresentationSnapshots {
		return fmt.Errorf("unapproved historical import content")
	}
	for _, side := range []json.RawMessage{content.Before, content.After} {
		if len(side) == 0 {
			continue
		}
		var snapshot model.V1AuditSnapshot
		if !strictV1AuditJSON(side) || decodeV1AuditJSON(side, &snapshot) != nil {
			return fmt.Errorf("invalid historical import snapshot")
		}
		if err := model.ValidateV1AuditSnapshot(resource, snapshot); err != nil {
			return fmt.Errorf("invalid historical import snapshot")
		}
	}
	return nil
}

// v1AuditPolicyField mirrors one closed field rule of the indexer's audit policy
// manifest (AUDIT_POLICY_FILE).
type v1AuditPolicyField struct {
	Type     string                        `json:"type"`
	Required bool                          `json:"required,omitempty"`
	Nullable bool                          `json:"nullable,omitempty"`
	Fields   map[string]v1AuditPolicyField `json:"fields,omitempty"`
}

// V1AuditImportPolicy returns the indexer audit policy entry that admits this
// service's historical imports: the import publisher bound to its machine
// principal, approved only for historical_import, with the closed *.legacy.v1
// snapshot schemas. Operators review it and merge it into the indexer's policy
// manifest; it carries no credential.
func V1AuditImportPolicy(principal string) ([]byte, error) {
	client, ok := strings.CutSuffix(principal, "@clients")
	if !ok || client == "" || !v1AuditOpaqueID(client) {
		return nil, errs.NewValidation("the import principal must be a machine client ending in @clients")
	}
	resources := make(map[string]any, 3)
	for _, resource := range []model.ResourceType{model.ResourceCommittee, model.ResourceCommitteeSettings, model.ResourceCommitteeMember} {
		fields := make(map[string]v1AuditPolicyField)
		for name, kind := range model.V1AuditFieldTypes(resource) {
			fields[name] = v1AuditPolicyField{Type: kind, Nullable: true}
		}
		resources[string(resource)] = map[string]any{
			"origins": []string{string(indexertypes.AuditOriginHistoricalImport)},
			"schemas": map[string]any{model.V1AuditSchema(resource): map[string]any{"fields": map[string]v1AuditPolicyField{
				"uid":           {Type: "string", Required: true},
				"committee_uid": {Type: "string", Required: true},
				"project_uid":   {Type: "string", Required: true},
				"fields":        {Type: "object", Required: true, Fields: fields},
			}}},
		}
	}
	policy := map[string]any{"publishers": []any{map[string]any{
		"publisher":         V1AuditImportPublisher,
		"principal":         principal,
		"can_attest_actors": true,
		"resources":         resources,
	}}}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(policy); err != nil {
		return nil, errs.NewUnexpected("cannot encode the indexer import policy", err)
	}
	return out.Bytes(), nil
}
