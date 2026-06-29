// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	texttemplate "text/template"
)

//go:embed templates/committee_application_submitted.html templates/committee_application_submitted.txt
//go:embed templates/committee_application_accepted.html templates/committee_application_accepted.txt
//go:embed templates/committee_application_rejected.html templates/committee_application_rejected.txt
var committeeApplicationNotificationTemplates embed.FS

var (
	committeeApplicationSubmittedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_application_submitted.html").
			ParseFS(committeeApplicationNotificationTemplates, "templates/committee_application_submitted.html"),
	)
	committeeApplicationSubmittedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_application_submitted.txt").
			ParseFS(committeeApplicationNotificationTemplates, "templates/committee_application_submitted.txt"),
	)

	committeeApplicationAcceptedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_application_accepted.html").
			ParseFS(committeeApplicationNotificationTemplates, "templates/committee_application_accepted.html"),
	)
	committeeApplicationAcceptedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_application_accepted.txt").
			ParseFS(committeeApplicationNotificationTemplates, "templates/committee_application_accepted.txt"),
	)

	committeeApplicationRejectedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_application_rejected.html").
			ParseFS(committeeApplicationNotificationTemplates, "templates/committee_application_rejected.html"),
	)
	committeeApplicationRejectedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_application_rejected.txt").
			ParseFS(committeeApplicationNotificationTemplates, "templates/committee_application_rejected.txt"),
	)
)

// CommitteeApplicationSubmittedData holds template variables for the writer notification email
// sent when a new application is submitted to a committee.
type CommitteeApplicationSubmittedData struct {
	RecipientName  string
	CommitteeName  string
	CommitteeURL   string
	ApplicantEmail string
	Message        string // applicant's optional cover message
}

// RenderCommitteeApplicationSubmitted renders the subject, HTML body, and plain-text body for
// the email sent to committee writers when a new application is submitted.
func RenderCommitteeApplicationSubmitted(data CommitteeApplicationSubmittedData) (subject, html, text string, err error) {
	subject = "New application to " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeApplicationSubmittedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeApplicationSubmittedTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// CommitteeApplicationAcceptedData holds template variables for the applicant notification email
// sent when their application is approved.
type CommitteeApplicationAcceptedData struct {
	RecipientName string
	CommitteeName string
	CommitteeURL  string
}

// RenderCommitteeApplicationAccepted renders the subject, HTML body, and plain-text body for
// the email sent to the applicant when their application is approved.
func RenderCommitteeApplicationAccepted(data CommitteeApplicationAcceptedData) (subject, html, text string, err error) {
	subject = "You've been accepted to " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeApplicationAcceptedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeApplicationAcceptedTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// CommitteeApplicationRejectedData holds template variables for the applicant notification email
// sent when their application is rejected.
type CommitteeApplicationRejectedData struct {
	RecipientName string
	CommitteeName string
	ReviewerNotes string // optional message from the reviewer
}

// RenderCommitteeApplicationRejected renders the subject, HTML body, and plain-text body for
// the email sent to the applicant when their application is rejected.
func RenderCommitteeApplicationRejected(data CommitteeApplicationRejectedData) (subject, html, text string, err error) {
	subject = "Update on your application to " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeApplicationRejectedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeApplicationRejectedTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}
