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
//go:embed templates/committee_role_updated.html templates/committee_role_updated.txt
//go:embed templates/committee_role_removed.html templates/committee_role_removed.txt
var committeeNotificationTemplates embed.FS

// joinFuncMap exposes a "join" function to templates that need to render []string.
var joinFuncMap = htmltemplate.FuncMap{
	"join": strings.Join,
}

var textJoinFuncMap = texttemplate.FuncMap{
	"join": strings.Join,
}

var (
	committeeRoleHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_notification.html").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_notification.html"),
	)
	committeeRoleTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_notification.txt").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_notification.txt"),
	)

	committeeRoleUpdatedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_updated.html").
			Funcs(joinFuncMap).
			ParseFS(committeeNotificationTemplates, "templates/committee_role_updated.html"),
	)
	committeeRoleUpdatedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_updated.txt").
			Funcs(textJoinFuncMap).
			ParseFS(committeeNotificationTemplates, "templates/committee_role_updated.txt"),
	)

	committeeRoleRemovedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_removed.html").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_removed.html"),
	)
	committeeRoleRemovedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_removed.txt").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_removed.txt"),
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

// CommitteeRoleUpdatedData holds the template variables for a committee role-updated email.
type CommitteeRoleUpdatedData struct {
	RecipientName string
	CommitteeName string
	CurrentRoles  []string
	CommitteeURL  string
	InviterName   string
}

// RenderCommitteeRoleUpdated renders the subject, HTML body, and plain-text body for an
// email notifying an LF user that their role set on a committee changed (gain, swap, or
// partial loss while still holding at least one role).
func RenderCommitteeRoleUpdated(data CommitteeRoleUpdatedData) (subject, html, text string, err error) {
	subject = sanitizeHeader(data.InviterName) + " updated your role on " + data.CommitteeName

	var htmlBuf bytes.Buffer
	if err = committeeRoleUpdatedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeRoleUpdatedTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// CommitteeRoleRemovedData holds the template variables for a committee removal email.
type CommitteeRoleRemovedData struct {
	RecipientName string
	CommitteeName string
	InviterName   string
}

// RenderCommitteeRoleRemoved renders the subject, HTML body, and plain-text body for an
// email notifying an LF user that they were fully removed from a committee (settings or member).
func RenderCommitteeRoleRemoved(data CommitteeRoleRemovedData) (subject, html, text string, err error) {
	subject = sanitizeHeader(data.InviterName) + " removed you from " + data.CommitteeName

	var htmlBuf bytes.Buffer
	if err = committeeRoleRemovedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeRoleRemovedTextTemplate.Execute(&textBuf, data); err != nil {
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
