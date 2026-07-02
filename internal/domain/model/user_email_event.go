// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import "time"

// UserEmailEventType identifies the kind of identity change that triggered a user-email event.
type UserEmailEventType string

const (
	// UserEmailEventAlternateEmailAdded is emitted when an alternate email is added to a user account.
	UserEmailEventAlternateEmailAdded UserEmailEventType = "alternate_email_added"
	// UserEmailEventAlternateEmailRemoved is emitted when an alternate email is removed from a user account.
	UserEmailEventAlternateEmailRemoved UserEmailEventType = "alternate_email_removed"
	// UserEmailEventLFIDUserCreated is emitted when a new LFID (LF Identity) account is created.
	UserEmailEventLFIDUserCreated UserEmailEventType = "lfid_user_created"
	// UserEmailEventLFIDUserDeleted is emitted when an LFID account is deleted.
	UserEmailEventLFIDUserDeleted UserEmailEventType = "lfid_user_deleted"
)

// UserEmailEvent is the inbound payload published by lfx-v1-sync-helper and other identity
// producers on the lfx.user-email.changed subject. It signals that the committee service
// should re-resolve the username for any committee member seats associated with Email.
type UserEmailEvent struct {
	Type      UserEmailEventType `json:"type"`
	Email     string             `json:"email"`
	Timestamp time.Time          `json:"timestamp"`
}
