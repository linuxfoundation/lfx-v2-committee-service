// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

//go:embed templates/committee_role_notification.html templates/committee_role_notification.txt
var committeeNotificationTemplates embed.FS

var (
	committeeRoleHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_notification.html").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_notification.html"),
	)
	committeeRoleTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_notification.txt").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_notification.txt"),
	)
)

// CommitteeRoleNotificationData holds the template variables for a committee role notification email.
type CommitteeRoleNotificationData struct {
	RecipientName string
	CommitteeName string
	Role          string
	CommitteeURL  string
	InviterName   string
}

// RenderCommitteeRoleNotification renders the subject, HTML body, and plain-text body
// for a committee role notification email.
func RenderCommitteeRoleNotification(data CommitteeRoleNotificationData) (subject, html, text string, err error) {
	subject = sanitizeHeader(data.InviterName) + " added you as a " + data.Role + " on " + data.CommitteeName

	var htmlBuf bytes.Buffer
	if err = committeeRoleHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeRoleTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// sanitizeHeader strips ASCII control characters (including CR/LF) from a string
// to prevent header injection if the value is ever used directly in an email header.
func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
