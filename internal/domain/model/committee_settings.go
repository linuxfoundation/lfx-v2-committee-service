// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"fmt"
	"time"

	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
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

// IndexingConfig returns the indexer access-control configuration for a committee_settings document.
// Settings are always indexed as non-public regardless of the parent committee's visibility,
// because they carry writer/auditor emails and are gated by the auditor relation at the API layer.
// tags should be the parent committee's Tags() output so search facets stay consistent.
func (s *CommitteeSettings) IndexingConfig(tags []string) *indexerTypes.IndexingConfig {
	notPublic := false
	return &indexerTypes.IndexingConfig{
		ObjectID:             s.UID,
		AccessCheckObject:    fmt.Sprintf("committee_settings:%s", s.UID),
		AccessCheckRelation:  "auditor",
		HistoryCheckObject:   fmt.Sprintf("committee_settings:%s", s.UID),
		HistoryCheckRelation: "auditor",
		Tags:                 tags,
		Public:               &notPublic,
	}
}

// CommitteeSettings represents sensitive committee settings
type CommitteeSettings struct {
	UID                   string          `json:"uid"`
	BusinessEmailRequired bool            `json:"business_email_required"`
	ShowMeetingAttendees  bool            `json:"show_meeting_attendees"`
	MemberVisibility      string          `json:"member_visibility"`
	ChatWebhookURL        *string         `json:"chat_webhook_url,omitempty"`
	HasChatWebhook        bool            `json:"has_chat_webhook"`
	LastReviewedAt        *string         `json:"last_reviewed_at,omitempty"`
	LastReviewedBy        *string         `json:"last_reviewed_by,omitempty"`
	Writers               []CommitteeUser `json:"writers"`
	Auditors              []CommitteeUser `json:"auditors"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}
