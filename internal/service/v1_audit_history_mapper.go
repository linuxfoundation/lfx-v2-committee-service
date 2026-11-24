// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type v1AuditRow struct {
	ID                string          `json:"id"`
	RecordID          string          `json:"record_id"`
	SubRecordID       string          `json:"sub_record_id"`
	RecordType        string          `json:"record_type"`
	Action            string          `json:"action"`
	CreatedAt         time.Time       `json:"created_at"`
	CreatedBy         string          `json:"created_by"`
	CreatedByUsername string          `json:"created_by_username"`
	CreatedByName     json.RawMessage `json:"created_by_name"`
	CreatedByEmail    json.RawMessage `json:"created_by_email"`
	CreatedByAvatar   json.RawMessage `json:"created_by_avatar"`
	UserSource        json.RawMessage `json:"user_source"`
	OldRecord         json.RawMessage `json:"old_record"`
	NewRecord         json.RawMessage `json:"new_record"`
	ChangedRecord     json.RawMessage `json:"changed_record"`
	canonical         []byte
}

func decodeV1AuditRow(data []byte) (*v1AuditRow, error) {
	var row v1AuditRow
	if !strictV1AuditJSON(data) || !v1AuditKnownKeys(data, "id", "record_id", "sub_record_id", "record_type", "action", "created_at", "created_by",
		"created_by_username", "created_by_name", "created_by_email", "created_by_avatar", "user_source", "old_record", "new_record", "changed_record") ||
		decodeV1AuditJSON(data, &row) != nil {
		return nil, errs.NewValidation("invalid source audit JSON")
	}
	id, err := uuid.Parse(row.ID)
	if err != nil || id == uuid.Nil || row.RecordID == "" || len(row.RecordID) > 256 || len(row.SubRecordID) > 256 || row.CreatedAt.IsZero() {
		return nil, errs.NewValidation("invalid source audit metadata")
	}
	row.ID = id.String()
	row.CreatedAt = row.CreatedAt.UTC()
	// Canonicalize complete source JSON for bounded in-run duplicate detection.
	// The hash is never emitted and raw source/profile fields never enter plans.
	var canonical map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&canonical) != nil {
		return nil, errs.NewValidation("invalid source audit object")
	}
	canonical["id"], canonical["created_at"] = row.ID, row.CreatedAt.Format(time.RFC3339Nano)
	row.canonical, _ = json.Marshal(canonical)
	return &row, nil
}

func planV1AuditRow(row *v1AuditRow, mappings *V1AuditMappings) ([]model.V1AuditPlanEvent, string, string) {
	actions := map[string]model.MessageAction{"INSERT": model.ActionCreated, "UPDATE": model.ActionUpdated, "DELETE": model.ActionDeleted}
	action, ok := actions[row.Action]
	if !ok {
		return nil, "invalid", "unsupported_source_action"
	}
	var resources []model.ResourceType
	switch row.RecordType {
	case "COMMITTEE":
		resources = []model.ResourceType{model.ResourceCommittee, model.ResourceCommitteeSettings}
	case "COMMITTEEMEMBER":
		if row.SubRecordID == "" {
			return nil, "invalid", "missing_member_record_id"
		}
		resources = []model.ResourceType{model.ResourceCommitteeMember}
	default:
		return nil, "skipped", "unsupported_source_record_type"
	}
	before, beforeOK := v1AuditImage(row.OldRecord)
	after, afterOK := v1AuditImage(row.NewRecord)
	if !beforeOK || !afterOK ||
		(action == model.ActionCreated && (len(before) != 0 || len(after) == 0)) ||
		(action == model.ActionUpdated && (len(before) == 0 || len(after) == 0)) ||
		(action == model.ActionDeleted && (len(before) == 0 || len(after) != 0)) {
		return nil, "invalid", "invalid_source_snapshot_sides"
	}
	if before != nil && after != nil &&
		(v1AuditString(before, "project_name__c") != v1AuditString(after, "project_name__c") ||
			v1AuditString(before, "collaboration_name__c") != v1AuditString(after, "collaboration_name__c")) {
		return nil, "skipped", "ownership_move_requires_review"
	}
	events := []model.V1AuditPlanEvent{}
	for _, resource := range resources {
		beforeFields, errBefore := model.V1AuditFields(resource, before)
		afterFields, errAfter := model.V1AuditFields(resource, after)
		if errBefore != nil || errAfter != nil {
			return nil, "invalid", "invalid_or_sensitive_snapshot_field"
		}
		beforeJSON, _ := json.Marshal(beforeFields)
		afterJSON, _ := json.Marshal(afterFields)
		if (len(beforeFields) == 0 && len(afterFields) == 0) || (action == model.ActionUpdated && bytes.Equal(beforeJSON, afterJSON)) {
			continue
		}
		var oldSnapshot, newSnapshot *model.V1AuditSnapshot
		if len(before) > 0 {
			var status, reason string
			oldSnapshot, status, reason = v1AuditSnapshot(row, resource, before, beforeFields, mappings)
			if oldSnapshot == nil {
				return nil, status, reason
			}
		}
		if len(after) > 0 {
			var status, reason string
			newSnapshot, status, reason = v1AuditSnapshot(row, resource, after, afterFields, mappings)
			if newSnapshot == nil {
				return nil, status, reason
			}
		}
		// The revision is the source occurrence time in microseconds: source-owned,
		// stable across retries and ordered in time. Imports never touch live
		// revision lineage, so it only has to keep this meaning.
		micros := row.CreatedAt.UnixMicro()
		if micros < 1 {
			return nil, "invalid", "historical_time_before_epoch"
		}
		revision := uint64(micros)
		current := newSnapshot
		if current == nil {
			current = oldSnapshot
		}
		event := model.V1AuditPlanEvent{
			EventID:      "v1-" + v1AuditHash([]byte(mappings.Source+"\x00platform.committee_audit_log\x00"+row.ID+"\x00"+string(resource))),
			ResourceType: resource, ResourceUID: current.UID, Action: action,
			OccurredAt: row.CreatedAt, ResourceRevision: revision, ActorRef: v1AuditActor(row, mappings),
			HistoryCheckObject: "committee:" + current.CommitteeUID, HistoryCheckRelation: "auditor",
			Schema: model.V1AuditSchema(resource), Before: oldSnapshot, After: newSnapshot,
		}
		if oldSnapshot != nil {
			event.ScopeBefore = []string{"project:" + oldSnapshot.ProjectUID, "committee:" + oldSnapshot.CommitteeUID}
		}
		if newSnapshot != nil {
			event.ScopeAfter = []string{"project:" + newSnapshot.ProjectUID, "committee:" + newSnapshot.CommitteeUID}
		}
		if _, err := json.Marshal(event); err != nil {
			return nil, "invalid", "historical_draft_not_encodable"
		}
		built, err := BuildV1AuditImportEvent(event)
		if errors.Is(err, errV1AuditImportTooLarge) {
			return nil, "invalid", "historical_draft_exceeds_size_limit"
		}
		if err != nil {
			return nil, "invalid", "indexer_import_rejected"
		}
		event.Frozen = built.Event
		events = append(events, event)
	}
	if len(events) == 0 {
		return nil, "skipped", "no_supported_historical_change"
	}
	return events, "importable", ""
}

func v1AuditSnapshot(row *v1AuditRow, resource model.ResourceType, image, fields map[string]json.RawMessage, mappings *V1AuditMappings) (*model.V1AuditSnapshot, string, string) {
	sourceID := row.RecordID
	if resource == model.ResourceCommitteeMember {
		sourceID = row.SubRecordID
		if v1AuditString(image, "collaboration_name__c") != row.RecordID {
			return nil, "invalid", "inconsistent_source_committee_id"
		}
	}
	if v1AuditString(image, "sfid") != sourceID {
		return nil, "invalid", "inconsistent_source_resource_id"
	}
	committeeUID := mappings.Committees[row.RecordID]
	if committeeUID == "" {
		return nil, "skipped", "missing_committee_mapping"
	}
	projectID := v1AuditString(image, "project_name__c")
	if projectID == "" {
		return nil, "skipped", "missing_historical_project_scope"
	}
	projectUID := mappings.Projects[projectID]
	if projectUID == "" {
		return nil, "skipped", "missing_project_mapping"
	}
	uid := committeeUID
	if resource == model.ResourceCommitteeMember {
		uid = mappings.Members[sourceID]
		if uid == "" {
			return nil, "skipped", "missing_member_mapping"
		}
	}
	snapshot := &model.V1AuditSnapshot{UID: uid, CommitteeUID: committeeUID, ProjectUID: projectUID, Fields: fields}
	if model.ValidateV1AuditSnapshot(resource, *snapshot) != nil {
		return nil, "invalid", "invalid_historical_snapshot"
	}
	return snapshot, "", ""
}

// Suffixes of the derived legacy actor namespaces: a recorded ID that cannot be
// used as-is is hashed, and an event without a recorded actor is identified by
// its source event.
const (
	v1AuditHashedActorSuffix = "_sha256"
	v1AuditEventActorSuffix  = "_event"
)

func v1AuditActor(row *v1AuditRow, mappings *V1AuditMappings) string {
	if actor, ok := mappings.Actors[row.CreatedBy]; ok && row.CreatedBy != "" {
		return actor
	}
	username := row.CreatedByUsername
	if client, ok := strings.CutSuffix(username, "@clients"); ok && v1AuditOpaqueID(client) {
		return "service:" + client
	}
	if v1AuditOpaqueID(username) {
		return "user:" + username
	}
	// The indexer forbids ':' inside a legacy actor ID, so derived refs use their
	// own source namespaces. Manifest sources may not end in these suffixes (see
	// v1AuditSourceValid), so a derived ref can never equal a recorded one.
	id := row.CreatedBy
	if id == "" {
		return "legacy:" + mappings.Source + v1AuditEventActorSuffix + "/" + v1AuditHash([]byte(mappings.Source+"\x00"+row.ID))
	}
	if !v1AuditOpaqueID(id) || len(id)+len(mappings.Source)+1 > 256 {
		return "legacy:" + mappings.Source + v1AuditHashedActorSuffix + "/" + v1AuditHash([]byte(id))
	}
	return "legacy:" + mappings.Source + "/" + id
}

func v1AuditImage(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true
	}
	var image map[string]json.RawMessage
	if json.Unmarshal(raw, &image) != nil {
		return nil, false
	}
	if len(image) == 0 {
		return nil, true
	}
	return image, true
}

func v1AuditString(image map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(image[key], &value)
	return value
}
