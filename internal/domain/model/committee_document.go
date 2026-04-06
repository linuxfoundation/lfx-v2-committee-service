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

const (
	// MaxDocumentFileSize is the maximum allowed file size for document uploads (10MB).
	MaxDocumentFileSize = 10 * 1024 * 1024
)

// AllowedDocumentContentTypes is the set of MIME types permitted for document uploads.
var AllowedDocumentContentTypes = map[string]bool{
	"application/pdf": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true, // .docx
	"application/msword": true, // .doc
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true, // .xlsx
	"application/vnd.ms-excel": true, // .xls
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true, // .pptx
	"application/vnd.ms-powerpoint":                                             true, // .ppt
	"text/plain":                                                                true,
	"text/csv":                                                                  true,
	"image/png":                                                                 true,
	"image/jpeg":                                                                true,
	"image/gif":                                                                 true,
	"application/zip":                                                           true,
}

// CommitteeDocument represents a file attachment associated with a committee.
// Metadata is stored in NATS KV; file data is stored in NATS Object Store.
type CommitteeDocument struct {
	UID                string    `json:"uid"`
	CommitteeUID       string    `json:"committee_uid"`
	Name               string    `json:"name"`
	Description        string    `json:"description,omitempty"`
	FileName           string    `json:"file_name"`
	FileSize           int64     `json:"file_size"`
	ContentType        string    `json:"content_type"`
	UploadedByUsername string    `json:"uploaded_by_username,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// BuildIndexKey returns a SHA-256 hash of committeeUID|name for document name uniqueness enforcement.
// NOTE: Name comparison is case-sensitive (consistent with folder pattern). If case-insensitive
// uniqueness is needed in the future, apply strings.ToLower(d.Name) here.
func (d *CommitteeDocument) BuildIndexKey(_ context.Context) string {
	data := fmt.Sprintf("%s|%s", d.CommitteeUID, d.Name)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// Tags generates a consistent set of tags for the committee document.
// IMPORTANT: If you modify this method, please update the Committee Tags documentation in the README.md
// to ensure consumers understand how to use these tags for searching.
func (d *CommitteeDocument) Tags() []string {
	if d == nil {
		return nil
	}

	var tags []string

	if d.UID != "" {
		tags = append(tags, d.UID)
		tags = append(tags, fmt.Sprintf("committee_document_uid:%s", d.UID))
	}

	if d.CommitteeUID != "" {
		tags = append(tags, fmt.Sprintf("committee_uid:%s", d.CommitteeUID))
	}

	if d.ContentType != "" {
		tags = append(tags, fmt.Sprintf("content_type:%s", d.ContentType))
	}

	if d.UploadedByUsername != "" {
		tags = append(tags, fmt.Sprintf("uploaded_by:%s", d.UploadedByUsername))
	}

	return tags
}
