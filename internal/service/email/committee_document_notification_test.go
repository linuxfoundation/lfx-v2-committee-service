// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderCommitteeDocumentNotification(t *testing.T) {
	baseData := CommitteeDocumentNotificationData{
		RecipientName: "Alice Smith",
		CommitteeName: "TSC Committee",
		CommitteeURL:  "https://app.dev.lfx.dev/project/groups/committee-1",
		UploaderName:  "Bob Jones",
	}

	t.Run("file document — subject, html, text all rendered", func(t *testing.T) {
		data := baseData
		data.DocumentType = "file"
		data.DocumentName = "Q1 Report"
		data.FileName = "q1-report.pdf"

		subject, html, text, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "Bob Jones")
		assert.Contains(t, subject, "document")
		assert.Contains(t, subject, "TSC Committee")

		assert.True(t, strings.Contains(html, "<html"), "expected HTML output")
		assert.Contains(t, html, "Alice Smith")
		assert.Contains(t, html, "Bob Jones")
		assert.Contains(t, html, "TSC Committee")
		assert.Contains(t, html, "Q1 Report")
		assert.Contains(t, html, "q1-report.pdf")
		assert.Contains(t, html, "https://app.dev.lfx.dev/project/groups/committee-1")
		assert.NotContains(t, html, "URL:")

		assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
		assert.Contains(t, text, "Alice Smith")
		assert.Contains(t, text, "Q1 Report")
		assert.Contains(t, text, "q1-report.pdf")
		assert.NotContains(t, text, "URL:")
	})

	t.Run("link — URL row shown, File row absent", func(t *testing.T) {
		data := baseData
		data.DocumentType = "link"
		data.DocumentName = "LFX Homepage"
		data.URL = "https://lfx.linuxfoundation.org"

		subject, html, text, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "link")
		assert.Contains(t, html, "https://lfx.linuxfoundation.org")
		assert.Contains(t, html, "URL:")
		assert.NotContains(t, html, "File:")
		assert.Contains(t, text, "URL: https://lfx.linuxfoundation.org")
		assert.NotContains(t, text, "File:")
	})

	t.Run("with folder name — folder row shown", func(t *testing.T) {
		data := baseData
		data.DocumentType = "file"
		data.DocumentName = "Minutes"
		data.FileName = "minutes.pdf"
		data.FolderName = "Meeting Notes"

		_, html, text, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, html, "Meeting Notes")
		assert.Contains(t, html, "Folder:")
		assert.Contains(t, text, "Folder: Meeting Notes")
	})

	t.Run("without folder name — folder row absent", func(t *testing.T) {
		data := baseData
		data.DocumentType = "file"
		data.DocumentName = "Spec"
		data.FileName = "spec.pdf"

		_, html, text, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.NotContains(t, html, "Folder:")
		assert.NotContains(t, text, "Folder:")
	})

	t.Run("no uploader name — generic subject and body", func(t *testing.T) {
		data := baseData
		data.UploaderName = ""
		data.DocumentType = "file"
		data.DocumentName = "Spec"
		data.FileName = "spec.pdf"

		subject, html, text, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "A new document was added to")
		assert.Contains(t, subject, "TSC Committee")
		assert.Contains(t, html, "A new document has been added")
		assert.Contains(t, text, "A new document has been added")
	})

	t.Run("html title reflects document type for file", func(t *testing.T) {
		data := baseData
		data.DocumentType = "file"
		data.DocumentName = "Spec"
		data.FileName = "spec.pdf"

		_, html, _, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, html, "<title>New document added to TSC Committee</title>")
	})

	t.Run("html title reflects document type for link", func(t *testing.T) {
		data := baseData
		data.DocumentType = "link"
		data.DocumentName = "Homepage"
		data.URL = "https://example.com"

		_, html, _, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, html, "<title>New link added to TSC Committee</title>")
	})

	t.Run("footer mentions all roles", func(t *testing.T) {
		data := baseData
		data.DocumentType = "file"
		data.DocumentName = "Spec"
		data.FileName = "spec.pdf"

		_, _, text, err := RenderCommitteeDocumentNotification(data)
		require.NoError(t, err)

		assert.Contains(t, text, "member, writer, or auditor")
	})
}
