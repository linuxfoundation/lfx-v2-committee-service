// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"time"
)

// CommitteeSettings represents sensitive committee settings
type CommitteeSettings struct {
	UID                   string     `json:"uid"`
	BusinessEmailRequired bool       `json:"business_email_required"`
	LastReviewedAt        *time.Time `json:"last_reviewed_at,omitempty"`
	LastReviewedBy        *string    `json:"last_reviewed_by,omitempty"`
	Writers               []string   `json:"writers"`
	Auditors              []string   `json:"auditors"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
