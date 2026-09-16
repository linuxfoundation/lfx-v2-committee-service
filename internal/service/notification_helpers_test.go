// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── highestRole ───────────────────────────────────────────────────────────────

func TestHighestRole_EmptySlice_ReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", highestRole(nil))
	assert.Equal(t, "", highestRole([]string{}))
}

func TestHighestRole_WriterWinsOverAuditor(t *testing.T) {
	assert.Equal(t, "Writer", highestRole([]string{"Auditor", "Writer"}))
}

func TestHighestRole_WriterAlone(t *testing.T) {
	assert.Equal(t, "Writer", highestRole([]string{"Writer"}))
}

func TestHighestRole_CaseInsensitiveWriter(t *testing.T) {
	// The implementation uses strings.EqualFold — verify different cases all match.
	assert.Equal(t, "writer", highestRole([]string{"auditor", "writer"}))
	assert.Equal(t, "WRITER", highestRole([]string{"WRITER"}))
}

func TestHighestRole_NoWriterReturnsFistElement(t *testing.T) {
	// No Writer present → falls back to roles[0].
	assert.Equal(t, "Auditor", highestRole([]string{"Auditor"}))
	assert.Equal(t, "Member", highestRole([]string{"Member", "Auditor"}))
}

// ── mapRoleToInviteRole ───────────────────────────────────────────────────────

func TestMapRoleToInviteRole_Writer_ReturnsManage(t *testing.T) {
	assert.Equal(t, "Manage", mapRoleToInviteRole("Writer"))
}

func TestMapRoleToInviteRole_WriterLowercase_ReturnsManage(t *testing.T) {
	assert.Equal(t, "Manage", mapRoleToInviteRole("writer"))
}

func TestMapRoleToInviteRole_Auditor_ReturnsView(t *testing.T) {
	assert.Equal(t, "View", mapRoleToInviteRole("Auditor"))
}

func TestMapRoleToInviteRole_AuditorLowercase_ReturnsView(t *testing.T) {
	assert.Equal(t, "View", mapRoleToInviteRole("auditor"))
}

func TestMapRoleToInviteRole_UnknownRole_DefaultsToManage(t *testing.T) {
	// Unknown roles err on the side of access; the test also confirms the
	// function doesn't panic on an empty or arbitrary string.
	assert.Equal(t, "Manage", mapRoleToInviteRole(""))
	assert.Equal(t, "Manage", mapRoleToInviteRole("FutureRole"))
}

func TestMapRoleToInviteRole_TrimsWhitespace(t *testing.T) {
	assert.Equal(t, "Manage", mapRoleToInviteRole("  Writer  "))
	assert.Equal(t, "View", mapRoleToInviteRole("  Auditor  "))
}

// ── roleSlicesEqual ───────────────────────────────────────────────────────────

func TestRoleSlicesEqual_BothEmpty(t *testing.T) {
	assert.True(t, roleSlicesEqual(nil, nil))
	assert.True(t, roleSlicesEqual([]string{}, []string{}))
}

func TestRoleSlicesEqual_SameElements(t *testing.T) {
	assert.True(t, roleSlicesEqual([]string{"Manage"}, []string{"Manage"}))
	assert.True(t, roleSlicesEqual([]string{"Manage", "View"}, []string{"Manage", "View"}))
}

func TestRoleSlicesEqual_DifferentLengths(t *testing.T) {
	assert.False(t, roleSlicesEqual([]string{"Manage"}, []string{"Manage", "View"}))
}

func TestRoleSlicesEqual_SameLengthDifferentContent(t *testing.T) {
	assert.False(t, roleSlicesEqual([]string{"Manage"}, []string{"View"}))
}

func TestRoleSlicesEqual_OrderSensitive(t *testing.T) {
	// The function is order-sensitive (callers sort before comparing).
	assert.False(t, roleSlicesEqual([]string{"View", "Manage"}, []string{"Manage", "View"}))
}

// ── sortedRoles ───────────────────────────────────────────────────────────────

func TestSortedRoles_EmptyMap(t *testing.T) {
	assert.Empty(t, sortedRoles(map[string]model.CommitteeUser{}))
}

func TestSortedRoles_SingleEntry(t *testing.T) {
	m := map[string]model.CommitteeUser{"Writer": {Username: "first-last"}}
	assert.Equal(t, []string{"Writer"}, sortedRoles(m))
}

func TestSortedRoles_Deterministic(t *testing.T) {
	// Map iteration is non-deterministic; sortedRoles must always return the
	// same order regardless of how Go happens to iterate the map.
	m := map[string]model.CommitteeUser{
		"Writer":  {Username: "first-last"},
		"Auditor": {Username: "other-user"},
	}
	want := []string{"Auditor", "Writer"}
	for range 20 {
		got := sortedRoles(m)
		require.Equal(t, want, got)
	}
}

// ── effectiveRoleUnchanged ────────────────────────────────────────────────────

func TestEffectiveRoleUnchanged_IdenticalRoles(t *testing.T) {
	assert.True(t, effectiveRoleUnchanged([]string{"Writer"}, []string{"Writer"}))
}

func TestEffectiveRoleUnchanged_WriterPlusAuditor_SameAsWriterAlone(t *testing.T) {
	// CommitteeRolesForDisplay collapses Writer+Auditor → ["Manage"],
	// same as Writer alone → no effective change, no email needed.
	assert.True(t, effectiveRoleUnchanged(
		[]string{"Writer"},
		[]string{"Writer", "Auditor"},
	))
}

func TestEffectiveRoleUnchanged_AuditorToWriter_Changed(t *testing.T) {
	assert.False(t, effectiveRoleUnchanged([]string{"Auditor"}, []string{"Writer"}))
}

func TestEffectiveRoleUnchanged_WriterToAuditor_Changed(t *testing.T) {
	assert.False(t, effectiveRoleUnchanged([]string{"Writer"}, []string{"Auditor"}))
}

func TestEffectiveRoleUnchanged_BothEmpty(t *testing.T) {
	assert.True(t, effectiveRoleUnchanged(nil, nil))
}

// ── enrichCommitteeUserIfEmailOnly ────────────────────────────────────────────

func TestEnrichCommitteeUserIfEmailOnly_AlreadyHasUsername_ReturnsFalse(t *testing.T) {
	user := &model.CommitteeUser{Username: "existing-user", Email: "first.last@example.com"}
	enriched := enrichCommitteeUserIfEmailOnly(user, "first.last@example.com", "new-user", "First Last")
	assert.False(t, enriched)
	assert.Equal(t, "existing-user", user.Username) // unchanged
}

func TestEnrichCommitteeUserIfEmailOnly_EmailMismatch_ReturnsFalse(t *testing.T) {
	user := &model.CommitteeUser{Email: "other@example.com"}
	enriched := enrichCommitteeUserIfEmailOnly(user, "first.last@example.com", "first-last", "First Last")
	assert.False(t, enriched)
	assert.Empty(t, user.Username)
}

func TestEnrichCommitteeUserIfEmailOnly_Match_SetsUsernameAndName(t *testing.T) {
	user := &model.CommitteeUser{Email: "first.last@example.com"}
	enriched := enrichCommitteeUserIfEmailOnly(user, "first.last@example.com", "first-last", "First Last")
	assert.True(t, enriched)
	assert.Equal(t, "first-last", user.Username)
	assert.Equal(t, "First Last", user.Name)
}

func TestEnrichCommitteeUserIfEmailOnly_PreservesExistingName(t *testing.T) {
	// Name set at invite creation is not overwritten.
	user := &model.CommitteeUser{Email: "first.last@example.com", Name: "Existing Name"}
	enriched := enrichCommitteeUserIfEmailOnly(user, "first.last@example.com", "first-last", "New Name")
	assert.True(t, enriched)
	assert.Equal(t, "Existing Name", user.Name)
}

func TestEnrichCommitteeUserIfEmailOnly_EmailCaseInsensitive(t *testing.T) {
	user := &model.CommitteeUser{Email: "First.Last@Example.COM"}
	enriched := enrichCommitteeUserIfEmailOnly(user, "first.last@example.com", "first-last", "")
	assert.True(t, enriched)
}

// ── splitName ─────────────────────────────────────────────────────────────────

func TestSplitName_EmptyString(t *testing.T) {
	first, last := splitName("")
	assert.Equal(t, "", first)
	assert.Equal(t, "", last)
}

func TestSplitName_WhitespaceOnly(t *testing.T) {
	first, last := splitName("   ")
	assert.Equal(t, "", first)
	assert.Equal(t, "", last)
}

func TestSplitName_SingleToken(t *testing.T) {
	first, last := splitName("First")
	assert.Equal(t, "First", first)
	assert.Equal(t, "", last)
}

func TestSplitName_TwoTokens(t *testing.T) {
	first, last := splitName("First Last")
	assert.Equal(t, "First", first)
	assert.Equal(t, "Last", last)
}

func TestSplitName_MultiWordLastName(t *testing.T) {
	// SplitN(name, " ", 2) — everything after the first space goes to last.
	first, last := splitName("First Middle Last")
	assert.Equal(t, "First", first)
	assert.Equal(t, "Middle Last", last)
}

func TestSplitName_TrimsLeadingTrailingWhitespace(t *testing.T) {
	first, last := splitName("  First  Last  ")
	assert.Equal(t, "First", first)
	assert.Equal(t, "Last", last)
}
