// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import "github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"

// SanitizeCommitteeLinkForPublish copies a link without viewer-inaccessible audit emails.
func SanitizeCommitteeLinkForPublish(link model.CommitteeLink) model.CommitteeLink {
	link.CreatedBy = publicAuditUser(link.CreatedBy)
	link.UpdatedBy = publicAuditUser(link.UpdatedBy)
	return link
}

// SanitizeCommitteeLinkFolderForPublish copies a folder without viewer-inaccessible audit emails.
func SanitizeCommitteeLinkFolderForPublish(folder model.CommitteeLinkFolder) model.CommitteeLinkFolder {
	folder.CreatedBy = publicAuditUser(folder.CreatedBy)
	folder.UpdatedBy = publicAuditUser(folder.UpdatedBy)
	return folder
}

// SanitizeCommitteeDocumentForPublish copies a document without viewer-inaccessible audit emails.
func SanitizeCommitteeDocumentForPublish(doc model.CommitteeDocument) model.CommitteeDocument {
	doc.CreatedBy = publicAuditUser(doc.CreatedBy)
	doc.UpdatedBy = publicAuditUser(doc.UpdatedBy)
	return doc
}

func publicAuditUser(user *model.CommitteeUser) *model.CommitteeUser {
	user = model.CloneCommitteeUser(user)
	if user != nil {
		user.Email = ""
	}
	return user
}
