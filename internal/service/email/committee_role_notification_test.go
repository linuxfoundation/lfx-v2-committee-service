// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderCommitteeRoleUpdated(t *testing.T) {
	t.Run("single role", func(t *testing.T) {
		data := CommitteeRoleUpdatedData{
			RecipientName: "Bob",
			CommitteeName: "TSC Committee",
			CurrentRoles:  []string{"Auditor"},
			CommitteeURL:  "https://app.dev.lfx.dev/projects/demo-project/committees",
			InviterName:   "A committee administrator",
		}

		subject, html, text, err := RenderCommitteeRoleUpdated(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "TSC Committee")
		assert.Contains(t, subject, "A committee administrator")

		assert.Contains(t, html, "Bob")
		assert.Contains(t, html, "TSC Committee")
		assert.Contains(t, html, "Auditor")
		assert.Contains(t, html, "https://app.dev.lfx.dev/projects/demo-project/committees")
		assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

		assert.Contains(t, text, "Bob")
		assert.Contains(t, text, "TSC Committee")
		assert.Contains(t, text, "Auditor")
		assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
	})

	t.Run("two roles", func(t *testing.T) {
		data := CommitteeRoleUpdatedData{
			RecipientName: "Carol",
			CommitteeName: "TSC Committee",
			CurrentRoles:  []string{"Auditor", "Writer"},
			CommitteeURL:  "https://app.dev.lfx.dev/projects/demo-project/committees",
			InviterName:   "A committee administrator",
		}

		subject, html, text, err := RenderCommitteeRoleUpdated(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "TSC Committee")
		assert.Contains(t, html, "Auditor")
		assert.Contains(t, html, "Writer")
		assert.Contains(t, text, "Auditor")
		assert.Contains(t, text, "Writer")
	})
}

func TestRenderCommitteeRoleRemoved(t *testing.T) {
	data := CommitteeRoleRemovedData{
		RecipientName: "Dave",
		CommitteeName: "TSC Committee",
		InviterName:   "A committee administrator",
	}

	subject, html, text, err := RenderCommitteeRoleRemoved(data)
	require.NoError(t, err)

	assert.Contains(t, subject, "TSC Committee")
	assert.Contains(t, subject, "A committee administrator")

	assert.Contains(t, html, "Dave")
	assert.Contains(t, html, "TSC Committee")
	assert.Contains(t, html, "A committee administrator")
	assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

	assert.Contains(t, text, "Dave")
	assert.Contains(t, text, "TSC Committee")
	assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
}

func TestRenderCommitteeRoleNotification(t *testing.T) {
	data := CommitteeRoleNotificationData{
		RecipientName: "Alice",
		CommitteeName: "TSC Committee",
		Role:          "Writer",
		CommitteeURL:  "https://app.dev.lfx.dev/projects/demo-project/committees",
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
	assert.Contains(t, html, "https://app.dev.lfx.dev/projects/demo-project/committees")
	assert.Contains(t, html, "A committee administrator")
	assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

	assert.Contains(t, text, "Alice")
	assert.Contains(t, text, "TSC Committee")
	assert.Contains(t, text, "Writer")
	assert.Contains(t, text, "https://app.dev.lfx.dev/projects/demo-project/committees")
	assert.Contains(t, text, "A committee administrator")
	assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
}
