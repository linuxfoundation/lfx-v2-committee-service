// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// CommitteeApplication represents a committee application business entity
type CommitteeApplication struct {
	UID           string    `json:"uid"`
	CommitteeUID  string    `json:"committee_uid"`
	ApplicantUID  string    `json:"applicant_uid"`
	Message       string    `json:"message"`
	Status        string    `json:"status"`
	ReviewerNotes string    `json:"reviewer_notes"`
	CreatedAt     time.Time `json:"created_at"`
}

// BuildIndexKey generates a SHA-256 hash for use as a NATS KV key.
// The hash is generated from the committee UID and the applicant UID (i.e., committee_uid + applicant_uid).
// This enforces uniqueness for committee applications within a committee.
// This is necessary because the original input may contain special characters,
// exceed length limits, or have inconsistent formatting, and we do not control its content.
// Using a hash ensures a safe, fixed-length, and deterministic key.
func (ca *CommitteeApplication) BuildIndexKey(ctx context.Context) string {

	committee := strings.TrimSpace(strings.ToLower(ca.CommitteeUID))
	applicant := strings.TrimSpace(strings.ToLower(ca.ApplicantUID))
	// Combine normalized values with a delimiter
	data := fmt.Sprintf("%s|%s", committee, applicant)

	hash := sha256.Sum256([]byte(data))

	key := hex.EncodeToString(hash[:])

	slog.DebugContext(ctx, "application index key built",
		"committee_uid", ca.CommitteeUID,
		"applicant_uid", ca.ApplicantUID,
		"key", key,
	)

	return key
}

// Tags generates a consistent set of tags for the committee application.
// IMPORTANT: If you modify this method, please update the Committee Tags documentation in the README.md
// to ensure consumers understand how to use these tags for searching.
func (ca *CommitteeApplication) Tags() []string {
	var tags []string

	if ca == nil {
		return nil
	}

	if ca.UID != "" {
		// without prefix
		tags = append(tags, ca.UID)
		// with prefix
		tag := fmt.Sprintf("committee_application_uid:%s", ca.UID)
		tags = append(tags, tag)
	}

	if ca.CommitteeUID != "" {
		tag := fmt.Sprintf("committee_uid:%s", ca.CommitteeUID)
		tags = append(tags, tag)
	}

	if ca.ApplicantUID != "" {
		tag := fmt.Sprintf("applicant_uid:%s", ca.ApplicantUID)
		tags = append(tags, tag)
	}

	if ca.Status != "" {
		tag := fmt.Sprintf("status:%s", ca.Status)
		tags = append(tags, tag)
	}

	return tags
}
