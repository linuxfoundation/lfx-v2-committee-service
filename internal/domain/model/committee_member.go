// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"
)

// CommitteeMember represents the complete committee member business entity
type CommitteeMember struct {
	CommitteeMemberBase
}

// CommitteeMemberBase represents the base committee member attributes
type CommitteeMemberBase struct {
	UID          string                      `json:"uid"`
	Username     string                      `json:"username"`
	Email        string                      `json:"email"`
	FirstName    string                      `json:"first_name"`
	LastName     string                      `json:"last_name"`
	JobTitle     string                      `json:"job_title,omitempty"`
	Role         CommitteeMemberRole         `json:"role"`
	AppointedBy  string                      `json:"appointed_by"`
	Status       string                      `json:"status"`
	Voting       CommitteeMemberVotingInfo   `json:"voting"`
	Agency       string                      `json:"agency,omitempty"`
	Country      string                      `json:"country,omitempty"`
	Organization CommitteeMemberOrganization `json:"organization"`
	CreatedAt    time.Time                   `json:"created_at"`
	UpdatedAt    time.Time                   `json:"updated_at"`
}

// Role represents committee role information
type CommitteeMemberRole struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// VotingInfo represents voting information for the committee member
type CommitteeMemberVotingInfo struct {
	Status    string `json:"status"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// Organization represents organization information for the committee member
type CommitteeMemberOrganization struct {
	Name    string `json:"name"`
	Website string `json:"website,omitempty"`
}

// BuildIndexKey generates a SHA-256 hash for use as a NATS KV key.
// This is necessary because the original input may contain special characters,
// exceed length limits, or have inconsistent formatting, and we do not control its content.
// Using a hash ensures a safe, fixed-length, and deterministic key.
func (cm *CommitteeMember) BuildIndexKey(ctx context.Context, committeeUID string) string {
	// Combine committee_uid and member email with a delimiter
	data := fmt.Sprintf("%s|%s", committeeUID, cm.Email)

	hash := sha256.Sum256([]byte(data))

	key := hex.EncodeToString(hash[:])

	slog.DebugContext(ctx, "member index key built",
		"committee_uid", committeeUID,
		"email", cm.Email,
		"key", key,
	)

	return key
}

// Tags generates a consistent set of tags for the committee member.
func (cm *CommitteeMember) Tags(committeeUID string) []string {
	var tags []string

	if cm == nil {
		return nil
	}

	if cm.UID != "" {
		tag := fmt.Sprintf("member_uid:%s", cm.UID)
		tags = append(tags, tag)
	}

	if committeeUID != "" {
		tag := fmt.Sprintf("committee_uid:%s", committeeUID)
		tags = append(tags, tag)
	}

	if cm.Username != "" {
		tag := fmt.Sprintf("username:%s", cm.Username)
		tags = append(tags, tag)
	}

	if cm.Email != "" {
		tag := fmt.Sprintf("email:%s", cm.Email)
		tags = append(tags, tag)
	}

	if cm.Voting.Status != "" {
		tag := fmt.Sprintf("voting_status:%s", cm.Voting.Status)
		tags = append(tags, tag)
	}

	return tags
}
