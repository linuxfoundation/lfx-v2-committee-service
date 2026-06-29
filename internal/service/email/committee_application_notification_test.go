// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderCommitteeApplicationSubmitted(t *testing.T) {
	data := CommitteeApplicationSubmittedData{
		RecipientName:  "Writer One",
		CommitteeName:  "TSC Committee",
		CommitteeURL:   "https://lfx.linuxfoundation.org/project/groups/committee-1",
		ApplicantEmail: "applicant@example.com",
		Message:        "I would like to contribute.",
	}

	subject, html, text, err := RenderCommitteeApplicationSubmitted(data)
	require.NoError(t, err)
	assert.Equal(t, "New application to TSC Committee", subject)
	assert.NotEmpty(t, html)
	assert.NotEmpty(t, text)
	assert.Contains(t, html, "TSC Committee")
	assert.Contains(t, html, "applicant@example.com")
	assert.Contains(t, html, "I would like to contribute.")
	assert.Contains(t, text, "TSC Committee")
	assert.Contains(t, text, "applicant@example.com")
	assert.Contains(t, text, "I would like to contribute.")
}

func TestRenderCommitteeApplicationSubmitted_NoMessage(t *testing.T) {
	data := CommitteeApplicationSubmittedData{
		RecipientName:  "Writer One",
		CommitteeName:  "TSC",
		CommitteeURL:   "https://example.com",
		ApplicantEmail: "a@b.com",
	}
	_, html, text, err := RenderCommitteeApplicationSubmitted(data)
	require.NoError(t, err)
	assert.NotEmpty(t, html)
	assert.NotEmpty(t, text)
}

func TestRenderCommitteeApplicationSubmitted_HeaderSanitization(t *testing.T) {
	data := CommitteeApplicationSubmittedData{
		RecipientName:  "Writer",
		CommitteeName:  "TSC\r\nBCC: evil@evil.com",
		CommitteeURL:   "https://example.com",
		ApplicantEmail: "a@b.com",
	}
	subject, _, _, err := RenderCommitteeApplicationSubmitted(data)
	require.NoError(t, err)
	assert.False(t, strings.ContainsAny(subject, "\r\n"), "subject must not contain CR/LF")
}

func TestRenderCommitteeApplicationAccepted(t *testing.T) {
	data := CommitteeApplicationAcceptedData{
		RecipientName: "Applicant",
		CommitteeName: "TSC Committee",
		CommitteeURL:  "https://lfx.linuxfoundation.org/project/groups/committee-1",
	}

	subject, html, text, err := RenderCommitteeApplicationAccepted(data)
	require.NoError(t, err)
	assert.Equal(t, "You've been accepted to TSC Committee", subject)
	assert.NotEmpty(t, html)
	assert.NotEmpty(t, text)
	assert.Contains(t, html, "TSC Committee")
	assert.Contains(t, html, "accepted")
	assert.Contains(t, text, "accepted")
}

func TestRenderCommitteeApplicationAccepted_HeaderSanitization(t *testing.T) {
	data := CommitteeApplicationAcceptedData{
		RecipientName: "Applicant",
		CommitteeName: "TSC\r\nBCC: evil@evil.com",
		CommitteeURL:  "https://example.com",
	}
	subject, _, _, err := RenderCommitteeApplicationAccepted(data)
	require.NoError(t, err)
	assert.False(t, strings.ContainsAny(subject, "\r\n"), "subject must not contain CR/LF")
}

func TestRenderCommitteeApplicationRejected(t *testing.T) {
	data := CommitteeApplicationRejectedData{
		RecipientName: "Applicant",
		CommitteeName: "TSC Committee",
		ReviewerNotes: "Not a fit at this time.",
	}

	subject, html, text, err := RenderCommitteeApplicationRejected(data)
	require.NoError(t, err)
	assert.Equal(t, "Update on your application to TSC Committee", subject)
	assert.NotEmpty(t, html)
	assert.NotEmpty(t, text)
	assert.Contains(t, html, "Not a fit at this time.")
	assert.Contains(t, text, "Not a fit at this time.")
}

func TestRenderCommitteeApplicationRejected_NoReviewerNotes(t *testing.T) {
	data := CommitteeApplicationRejectedData{
		RecipientName: "Applicant",
		CommitteeName: "TSC Committee",
	}
	_, html, text, err := RenderCommitteeApplicationRejected(data)
	require.NoError(t, err)
	assert.NotEmpty(t, html)
	assert.NotEmpty(t, text)
}

func TestRenderCommitteeApplicationRejected_HeaderSanitization(t *testing.T) {
	data := CommitteeApplicationRejectedData{
		RecipientName: "Applicant",
		CommitteeName: "TSC\r\nBCC: evil@evil.com",
	}
	subject, _, _, err := RenderCommitteeApplicationRejected(data)
	require.NoError(t, err)
	assert.False(t, strings.ContainsAny(subject, "\r\n"), "subject must not contain CR/LF")
}
