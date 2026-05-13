// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderCommitteeRoleNotification(t *testing.T) {
	data := CommitteeRoleNotificationData{
		RecipientName: "Alice",
		CommitteeName: "TSC Committee",
		Role:          "Writer",
		CommitteeURL:  "https://dev.app.lfx.dev/projects/demo-project/committees",
		InviterName:   "A committee administrator",
	}

	subject, html, text, err := RenderCommitteeRoleNotification(data)
	require.NoError(t, err)

	assert.Contains(t, subject, "Writer")
	assert.Contains(t, subject, "TSC Committee")
	assert.Contains(t, subject, "A committee administrator")

	assert.Contains(t, html, "Alice")
	assert.Contains(t, html, "TSC Committee")
	assert.Contains(t, html, "Writer")
	assert.Contains(t, html, "https://dev.app.lfx.dev/projects/demo-project/committees")
	assert.Contains(t, html, "A committee administrator")
	assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

	assert.Contains(t, text, "Alice")
	assert.Contains(t, text, "TSC Committee")
	assert.Contains(t, text, "Writer")
	assert.Contains(t, text, "https://dev.app.lfx.dev/projects/demo-project/committees")
	assert.Contains(t, text, "A committee administrator")
	assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
}
