// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	texttemplate "text/template"
)

//go:embed templates/committee_document_notification.html templates/committee_document_notification.txt
var committeeDocumentNotificationTemplates embed.FS

var (
	committeeDocumentNotificationHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_document_notification.html").
			ParseFS(committeeDocumentNotificationTemplates, "templates/committee_document_notification.html"),
	)
	committeeDocumentNotificationTextTemplate = texttemplate.Must(
		texttemplate.New("committee_document_notification.txt").
			ParseFS(committeeDocumentNotificationTemplates, "templates/committee_document_notification.txt"),
	)
)

// CommitteeDocumentNotificationData holds the template variables for a document/link upload notification email.
// A single template handles both cases: ItemType is "document" for file uploads and "link" for URL additions;
// ItemURL is non-empty only for links.
type CommitteeDocumentNotificationData struct {
	RecipientName   string
	CommitteeName   string
	CommitteeURL    string
	UploaderName    string
	ItemType        string // "document" or "link"
	ItemName        string
	ItemURL         string // non-empty for links, empty for file documents
	ItemDescription string
}

// RenderCommitteeDocumentNotification renders the subject, HTML body, and plain-text body for an
// email notifying committee members/writers/auditors that a new document or link was added.
func RenderCommitteeDocumentNotification(data CommitteeDocumentNotificationData) (subject, html, text string, err error) {
	subject = sanitizeHeader(data.UploaderName) + " added a " + sanitizeHeader(data.ItemType) + " to " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeDocumentNotificationHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeDocumentNotificationTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}
