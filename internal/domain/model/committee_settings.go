// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"time"
)

// CommitteeUser represents a user stored in the writers or auditors lists.
type CommitteeUser struct {
	Avatar   string `json:"avatar,omitempty"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
}

// GetWriters returns the Writers slice, returning nil when the receiver is nil.
func (s *CommitteeSettings) GetWriters() []CommitteeUser {
	if s == nil {
		return nil
	}
	return s.Writers
}

// GetAuditors returns the Auditors slice, returning nil when the receiver is nil.
func (s *CommitteeSettings) GetAuditors() []CommitteeUser {
	if s == nil {
		return nil
	}
	return s.Auditors
}

// CommitteeSettings represents sensitive committee settings
type CommitteeSettings struct {
	UID                   string          `json:"uid"`
	BusinessEmailRequired bool            `json:"business_email_required"`
	ShowMeetingAttendees  bool            `json:"show_meeting_attendees"`
	MemberVisibility      string          `json:"member_visibility"`
	LastReviewedAt        *string         `json:"last_reviewed_at,omitempty"`
	LastReviewedBy        *string         `json:"last_reviewed_by,omitempty"`
	Writers               []CommitteeUser `json:"writers"`
	Auditors              []CommitteeUser `json:"auditors"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}
