// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// CommitteeLink represents a URL reference associated with a committee.
type CommitteeLink struct {
	UID           string    `json:"uid"`
	CommitteeUID  string    `json:"committee_uid"`
	FolderUID     *string   `json:"folder_uid,omitempty"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	Description   string    `json:"description,omitempty"`
	CreatedByUID  string    `json:"created_by_uid,omitempty"`
	CreatedByName string    `json:"created_by_name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// BuildIndexKey returns a SHA-256 hash of committee_uid|uid for use as a storage key.
func (l *CommitteeLink) BuildIndexKey(_ context.Context) string {
	data := fmt.Sprintf("%s|%s", l.CommitteeUID, l.UID)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// CommitteeLinkFolder represents an organizational folder for committee links.
type CommitteeLinkFolder struct {
	UID          string    `json:"uid"`
	CommitteeUID string    `json:"committee_uid"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// BuildIndexKey returns a SHA-256 hash of committee_uid|name for uniqueness enforcement.
func (f *CommitteeLinkFolder) BuildIndexKey(_ context.Context) string {
	data := fmt.Sprintf("%s|%s", f.CommitteeUID, f.Name)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
