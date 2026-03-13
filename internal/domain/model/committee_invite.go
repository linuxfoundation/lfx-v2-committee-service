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

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

// CommitteeInvite represents a committee invitation business entity
type CommitteeInvite struct {
	UID          string    `json:"uid"`
	CommitteeUID string    `json:"committee_uid"`
	InviteeEmail string    `json:"invitee_email"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// BuildIndexKey generates a SHA-256 hash for use as a NATS KV key.
// The hash is generated from the committee UID and the invitee's email (i.e., committee_uid + invitee_email).
// This enforces uniqueness for committee invites within a committee.
// This is necessary because the original input may contain special characters,
// exceed length limits, or have inconsistent formatting, and we do not control its content.
// Using a hash ensures a safe, fixed-length, and deterministic key.
func (ci *CommitteeInvite) BuildIndexKey(ctx context.Context) string {

	committee := strings.TrimSpace(strings.ToLower(ci.CommitteeUID))
	email := strings.TrimSpace(strings.ToLower(ci.InviteeEmail))
	// Combine normalized values with a delimiter
	data := fmt.Sprintf("%s|%s", committee, email)

	hash := sha256.Sum256([]byte(data))

	key := hex.EncodeToString(hash[:])

	slog.DebugContext(ctx, "invite index key built",
		"committee_uid", ci.CommitteeUID,
		"invitee_email", redaction.RedactEmail(ci.InviteeEmail),
		"key", key,
	)

	return key
}

// Tags generates a consistent set of tags for the committee invite.
// IMPORTANT: If you modify this method, please update the Committee Tags documentation in the README.md
// to ensure consumers understand how to use these tags for searching.
func (ci *CommitteeInvite) Tags() []string {
	var tags []string

	if ci == nil {
		return nil
	}

	if ci.UID != "" {
		// without prefix
		tags = append(tags, ci.UID)
		// with prefix
		tag := fmt.Sprintf("committee_invite_uid:%s", ci.UID)
		tags = append(tags, tag)
	}

	if ci.CommitteeUID != "" {
		tag := fmt.Sprintf("committee_uid:%s", ci.CommitteeUID)
		tags = append(tags, tag)
	}

	if ci.InviteeEmail != "" {
		email := strings.TrimSpace(strings.ToLower(ci.InviteeEmail))
		tag := fmt.Sprintf("invitee_email:%s", email)
		tags = append(tags, tag)
	}

	if ci.Status != "" {
		tag := fmt.Sprintf("status:%s", ci.Status)
		tags = append(tags, tag)
	}

	return tags
}
