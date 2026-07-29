// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// CommitteeLink represents a URL reference associated with a committee.
type CommitteeLink struct {
	UID          string         `json:"uid"`
	CommitteeUID string         `json:"committee_uid"`
	FolderUID    *string        `json:"folder_uid,omitempty"`
	Name         string         `json:"name"`
	URL          string         `json:"url"`
	Description  string         `json:"description,omitempty"`
	CreatedBy    *CommitteeUser `json:"created_by,omitempty"`
	UpdatedBy    *CommitteeUser `json:"updated_by,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type committeeLinkJSON struct {
	UID               string         `json:"uid"`
	CommitteeUID      string         `json:"committee_uid"`
	FolderUID         *string        `json:"folder_uid,omitempty"`
	Name              string         `json:"name"`
	URL               string         `json:"url"`
	Description       string         `json:"description,omitempty"`
	CreatedBy         *CommitteeUser `json:"created_by,omitempty"`
	UpdatedBy         *CommitteeUser `json:"updated_by,omitempty"`
	CreatedByUsername string         `json:"created_by_username,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func (l *CommitteeLink) UnmarshalJSON(data []byte) error {
	var raw committeeLinkJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.UID = raw.UID
	l.CommitteeUID = raw.CommitteeUID
	l.FolderUID = raw.FolderUID
	l.Name = raw.Name
	l.URL = raw.URL
	l.Description = raw.Description
	l.CreatedBy = raw.CreatedBy
	l.UpdatedBy = raw.UpdatedBy
	l.CreatedAt = raw.CreatedAt
	l.UpdatedAt = raw.UpdatedAt
	l.CreatedBy, l.UpdatedBy = NormalizeLegacyAuditUsers(l.CreatedBy, l.UpdatedBy, raw.CreatedByUsername, "")
	return nil
}

func (l *CommitteeLink) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	return json.Marshal(&committeeLinkJSON{
		UID:          l.UID,
		CommitteeUID: l.CommitteeUID,
		FolderUID:    l.FolderUID,
		Name:         l.Name,
		URL:          l.URL,
		Description:  l.Description,
		CreatedBy:    l.CreatedBy,
		UpdatedBy:    l.UpdatedBy,
		CreatedAt:    l.CreatedAt,
		UpdatedAt:    l.UpdatedAt,
	})
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

	if username := AuditCreatorUsername(l.CreatedBy); username != "" {
		tags = append(tags, fmt.Sprintf("uploaded_by:%s", username))
	}

	return tags
}

// CommitteeLinkFolder represents an organizational folder for committee links.
type CommitteeLinkFolder struct {
	UID          string         `json:"uid"`
	CommitteeUID string         `json:"committee_uid"`
	Name         string         `json:"name"`
	CreatedBy    *CommitteeUser `json:"created_by,omitempty"`
	UpdatedBy    *CommitteeUser `json:"updated_by,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type committeeLinkFolderJSON struct {
	UID               string         `json:"uid"`
	CommitteeUID      string         `json:"committee_uid"`
	Name              string         `json:"name"`
	CreatedBy         *CommitteeUser `json:"created_by,omitempty"`
	UpdatedBy         *CommitteeUser `json:"updated_by,omitempty"`
	CreatedByUsername string         `json:"created_by_username,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func (f *CommitteeLinkFolder) UnmarshalJSON(data []byte) error {
	var raw committeeLinkFolderJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.UID = raw.UID
	f.CommitteeUID = raw.CommitteeUID
	f.Name = raw.Name
	f.CreatedBy = raw.CreatedBy
	f.UpdatedBy = raw.UpdatedBy
	f.CreatedAt = raw.CreatedAt
	f.UpdatedAt = raw.UpdatedAt
	f.CreatedBy, f.UpdatedBy = NormalizeLegacyAuditUsers(f.CreatedBy, f.UpdatedBy, raw.CreatedByUsername, "")
	return nil
}

func (f *CommitteeLinkFolder) MarshalJSON() ([]byte, error) {
	if f == nil {
		return []byte("null"), nil
	}
	return json.Marshal(&committeeLinkFolderJSON{
		UID:          f.UID,
		CommitteeUID: f.CommitteeUID,
		Name:         f.Name,
		CreatedBy:    f.CreatedBy,
		UpdatedBy:    f.UpdatedBy,
		CreatedAt:    f.CreatedAt,
		UpdatedAt:    f.UpdatedAt,
	})
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

	if username := AuditCreatorUsername(f.CreatedBy); username != "" {
		tags = append(tags, fmt.Sprintf("uploaded_by:%s", username))
	}

	return tags
}

// BuildIndexKey returns a SHA-256 hash of committee_uid|name for uniqueness enforcement.
func (f *CommitteeLinkFolder) BuildIndexKey(_ context.Context) string {
	data := fmt.Sprintf("%s|%s", f.CommitteeUID, f.Name)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
