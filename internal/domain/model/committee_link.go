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

// Tags generates a consistent set of tags for the committee link.
// IMPORTANT: If you modify this method, please update the Committee Tags documentation in the README.md
// to ensure consumers understand how to use these tags for searching.
func (l *CommitteeLink) Tags() []string {
	if l == nil {
		return nil
	}

	var tags []string

	if l.UID != "" {
		// without prefix
		tags = append(tags, l.UID)
		// with prefix
		tags = append(tags, fmt.Sprintf("committee_link_uid:%s", l.UID))
	}

	if l.CommitteeUID != "" {
		tags = append(tags, fmt.Sprintf("committee_uid:%s", l.CommitteeUID))
	}

	if l.FolderUID != nil && *l.FolderUID != "" {
		tags = append(tags, fmt.Sprintf("folder_uid:%s", *l.FolderUID))
	}

	return tags
}

// CommitteeLinkFolder represents an organizational folder for committee links.
type CommitteeLinkFolder struct {
	UID           string    `json:"uid"`
	CommitteeUID  string    `json:"committee_uid"`
	Name          string    `json:"name"`
	CreatedByUID  string    `json:"created_by_uid,omitempty"`
	CreatedByName string    `json:"created_by_name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Tags generates a consistent set of tags for the committee link folder.
// IMPORTANT: If you modify this method, please update the Committee Tags documentation in the README.md
// to ensure consumers understand how to use these tags for searching.
func (f *CommitteeLinkFolder) Tags() []string {
	if f == nil {
		return nil
	}

	var tags []string

	if f.UID != "" {
		// without prefix
		tags = append(tags, f.UID)
		// with prefix
		tags = append(tags, fmt.Sprintf("committee_link_folder_uid:%s", f.UID))
	}

	if f.CommitteeUID != "" {
		tags = append(tags, fmt.Sprintf("committee_uid:%s", f.CommitteeUID))
	}

	return tags
}

// BuildIndexKey returns a SHA-256 hash of committee_uid|name for uniqueness enforcement.
func (f *CommitteeLinkFolder) BuildIndexKey(_ context.Context) string {
	data := fmt.Sprintf("%s|%s", f.CommitteeUID, f.Name)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
