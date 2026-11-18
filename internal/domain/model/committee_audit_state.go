// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// validateCommitteeAuditState compares the closed after-image with shared current
// projection fields. Only documented redactions and parent-derived settings scope
// differ; UID-only deletion events deliberately have no after-image to compare.
func validateCommitteeAuditState(after json.RawMessage, data any) error {
	if len(after) == 0 {
		return nil
	}
	var actual map[string]any
	if err := json.Unmarshal(after, &actual); err != nil {
		return errs.NewValidation("invalid committee audit after-image")
	}
	var projected any
	var err error
	switch value := data.(type) {
	case CommitteeBase:
		projected, err = SnapshotCommittee(value)
		if charter, ok := actual["charter"].(map[string]any); ok {
			if editor, ok := charter["updated_by"].(map[string]any); ok {
				delete(editor, "email")
			}
		}
	case CommitteeSettings:
		var snapshot CommitteeSettingsAuditSnapshot
		err = copyAuditSnapshot(value, &snapshot)
		snapshot.ProjectUID, _ = actual["project_uid"].(string)
		projected = snapshot
	case CommitteeMember:
		projected, err = SnapshotCommitteeMember(value)
	default:
		return errs.NewValidation("a typed committee audit projection is required")
	}
	if err != nil {
		return err
	}
	expected, err := json.Marshal(projected)
	if err != nil {
		return errs.NewValidation("invalid committee audit projection state")
	}
	canonical, err := json.Marshal(actual)
	if err != nil || !sameAuditJSONObject(canonical, expected) {
		return errs.NewValidation("committee audit after-image and projection state differ")
	}
	return nil
}
