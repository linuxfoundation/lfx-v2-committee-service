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
			OldRoles:      []string{"Writer"},
			NewRoles:      []string{"Auditor"},
			CommitteeURL:  "https://app.dev.lfx.dev/projects/demo-project/committees",
			InviterName:   "A committee administrator",
		}

		subject, html, text, err := RenderCommitteeRoleUpdated(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "TSC Committee")
		assert.Contains(t, subject, "A committee administrator")

		assert.Contains(t, html, "Bob")
		assert.Contains(t, html, "TSC Committee")
		assert.Contains(t, html, "Manage") // Writer → Manage
		assert.Contains(t, html, "View")   // Auditor → View
		assert.Contains(t, html, "https://app.dev.lfx.dev/projects/demo-project/committees")
		assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

		assert.Contains(t, text, "Bob")
		assert.Contains(t, text, "TSC Committee")
		assert.Contains(t, text, "Manage")
		assert.Contains(t, text, "View")
		assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
	})

	t.Run("two roles collapses to Manage", func(t *testing.T) {
		data := CommitteeRoleUpdatedData{
			RecipientName: "Carol",
			CommitteeName: "TSC Committee",
			OldRoles:      []string{"Auditor"},
			NewRoles:      []string{"Auditor", "Writer"},
			CommitteeURL:  "https://app.dev.lfx.dev/projects/demo-project/committees",
			InviterName:   "A committee administrator",
		}

		subject, html, text, err := RenderCommitteeRoleUpdated(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "TSC Committee")
		// Old: Auditor → View; New: Auditor+Writer → collapsed to Manage
		assert.Contains(t, html, "View")
		assert.Contains(t, html, "Manage")
		assert.Contains(t, text, "View")
		assert.Contains(t, text, "Manage")
	})
}

func TestRenderCommitteeRoleRemoved(t *testing.T) {
	t.Run("with previous role", func(t *testing.T) {
		data := CommitteeRoleRemovedData{
			RecipientName: "Dave",
			CommitteeName: "TSC Committee",
			OldRoles:      []string{"Writer"},
			InviterName:   "A committee administrator",
		}

		subject, html, text, err := RenderCommitteeRoleRemoved(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "TSC Committee")
		assert.Contains(t, subject, "A committee administrator")

		assert.Contains(t, html, "Dave")
		assert.Contains(t, html, "TSC Committee")
		assert.Contains(t, html, "A committee administrator")
		assert.Contains(t, html, "Manage") // Writer → Manage
		assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

		assert.Contains(t, text, "Dave")
		assert.Contains(t, text, "TSC Committee")
		assert.Contains(t, text, "Manage")
		assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
	})

	t.Run("without previous role", func(t *testing.T) {
		data := CommitteeRoleRemovedData{
			RecipientName: "Eve",
			CommitteeName: "TSC Committee",
			InviterName:   "A committee administrator",
		}

		subject, html, text, err := RenderCommitteeRoleRemoved(data)
		require.NoError(t, err)

		assert.Contains(t, subject, "TSC Committee")
		assert.Contains(t, html, "Eve")
		assert.NotContains(t, html, "previous role")
		assert.NotContains(t, text, "previous role")
	})
}

func TestRenderCommitteeRoleNotification(t *testing.T) {
	data := CommitteeRoleNotificationData{
		RecipientName: "Alice",
		CommitteeName: "TSC Committee",
		Role:          "Manage",
		CommitteeURL:  "https://app.dev.lfx.dev/projects/demo-project/committees",
		InviterName:   "A committee administrator",
	}

	subject, html, text, err := RenderCommitteeRoleNotification(data)
	require.NoError(t, err)

	assert.Contains(t, subject, "Manage")
	assert.Contains(t, subject, "TSC Committee")
	assert.Contains(t, subject, "A committee administrator")

	assert.Contains(t, html, "Alice")
	assert.Contains(t, html, "TSC Committee")
	assert.Contains(t, html, "Manage")
	assert.Contains(t, html, "https://app.dev.lfx.dev/projects/demo-project/committees")
	assert.Contains(t, html, "A committee administrator")
	assert.True(t, strings.Contains(html, "<html"), "expected HTML output")

	assert.Contains(t, text, "Alice")
	assert.Contains(t, text, "TSC Committee")
	assert.Contains(t, text, "Manage")
	assert.Contains(t, text, "https://app.dev.lfx.dev/projects/demo-project/committees")
	assert.Contains(t, text, "A committee administrator")
	assert.False(t, strings.Contains(text, "<html"), "expected plain text output")
}

func TestCommitteeRoleCapabilities(t *testing.T) {
	t.Run("Manage has 5 capabilities", func(t *testing.T) {
		items := committeeRoleCapabilities("Manage")
		assert.Len(t, items, 5)
		assert.Contains(t, items, "Update committee settings")
		assert.Contains(t, items, "Schedule a survey for a committee")
	})
	t.Run("View has 6 capabilities", func(t *testing.T) {
		items := committeeRoleCapabilities("View")
		assert.Len(t, items, 6)
		assert.Contains(t, items, "View committee details")
		assert.Contains(t, items, "Download committee documents")
	})
	t.Run("unknown role returns nil", func(t *testing.T) {
		assert.Nil(t, committeeRoleCapabilities("Member"))
		assert.Nil(t, committeeRoleCapabilities(""))
	})
}

func TestRenderCommitteeRoleNotification_CapabilitiesInEmail(t *testing.T) {
	data := CommitteeRoleNotificationData{
		RecipientName: "Alice",
		CommitteeName: "TSC Committee",
		Role:          "Manage",
		CommitteeURL:  "https://example.com",
		InviterName:   "Admin",
	}
	_, html, text, err := RenderCommitteeRoleNotification(data)
	require.NoError(t, err)
	assert.Contains(t, html, "With the <strong>Manage</strong> role, you can:")
	assert.Contains(t, html, "Update committee settings")
	assert.Contains(t, html, "Schedule a survey for a committee")
	assert.Contains(t, text, "With the Manage role, you can:")
	assert.Contains(t, text, "- Update committee settings")
}

func TestRenderCommitteeRoleUpdated_CapabilitiesInEmail(t *testing.T) {
	data := CommitteeRoleUpdatedData{
		RecipientName: "Bob",
		CommitteeName: "TSC Committee",
		OldRoles:      []string{"Writer"},
		NewRoles:      []string{"Auditor"},
		CommitteeURL:  "https://example.com",
		InviterName:   "Admin",
	}
	_, html, text, err := RenderCommitteeRoleUpdated(data)
	require.NoError(t, err)
	assert.Contains(t, html, "With the <strong>View</strong> role, you can:")
	assert.Contains(t, html, "View committee details")
	assert.Contains(t, text, "With the View role, you can:")
	assert.Contains(t, text, "- View committee details")
	// Old role capabilities should not appear
	assert.NotContains(t, html, "Update committee settings")
}

func TestCommitteeRolesForDisplay(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"writer only", []string{"Writer"}, []string{"Manage"}},
		{"auditor only", []string{"Auditor"}, []string{"View"}},
		{"writer and auditor collapses", []string{"Writer", "Auditor"}, []string{"Manage"}},
		{"auditor and writer collapses", []string{"Auditor", "Writer"}, []string{"Manage"}},
		{"empty", []string{}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CommitteeRolesForDisplay(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestJoinCommitteeRoles(t *testing.T) {
	assert.Equal(t, "", JoinCommitteeRoles([]string{}))
	assert.Equal(t, "Manage", JoinCommitteeRoles([]string{"Manage"}))
	assert.Equal(t, "Manage and View", JoinCommitteeRoles([]string{"Manage", "View"}))
	assert.Equal(t, "A, B, and C", JoinCommitteeRoles([]string{"A", "B", "C"}))
}
