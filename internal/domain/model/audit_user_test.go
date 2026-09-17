// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── CloneCommitteeUser ────────────────────────────────────────────────────────

func TestCloneCommitteeUser_NilReturnsNil(t *testing.T) {
	assert.Nil(t, CloneCommitteeUser(nil))
}

func TestCloneCommitteeUser_ReturnsShallowCopy(t *testing.T) {
	orig := &CommitteeUser{
		Username: "first-last",
		Email:    "first.last@example.com",
		Name:     "First Last",
		Avatar:   "https://example.com/avatar.png",
	}
	clone := CloneCommitteeUser(orig)
	require.NotNil(t, clone)
	assert.Equal(t, orig, clone)
	assert.NotSame(t, orig, clone) // distinct pointer
}

func TestCloneCommitteeUser_MutationDoesNotAffectOriginal(t *testing.T) {
	orig := &CommitteeUser{Username: "first-last"}
	clone := CloneCommitteeUser(orig)
	clone.Username = "other-user"
	assert.Equal(t, "first-last", orig.Username)
}

// ── AuditCreatorUsername ──────────────────────────────────────────────────────

func TestAuditCreatorUsername_NilReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", AuditCreatorUsername(nil))
}

func TestAuditCreatorUsername_EmptyUsernameReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", AuditCreatorUsername(&CommitteeUser{}))
}

func TestAuditCreatorUsername_WhitespaceOnlyReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", AuditCreatorUsername(&CommitteeUser{Username: "  "}))
}

func TestAuditCreatorUsername_ReturnsUsername(t *testing.T) {
	assert.Equal(t, "first-last", AuditCreatorUsername(&CommitteeUser{Username: "first-last"}))
}

func TestAuditCreatorUsername_TrimsWhitespace(t *testing.T) {
	assert.Equal(t, "first-last", AuditCreatorUsername(&CommitteeUser{Username: "  first-last  "}))
}

// ── AuditUserNeedsMigration ───────────────────────────────────────────────────

func TestAuditUserNeedsMigration_NilReturnsFalse(t *testing.T) {
	assert.False(t, AuditUserNeedsMigration(nil))
}

func TestAuditUserNeedsMigration_EmptyUsernameReturnsFalse(t *testing.T) {
	// No username → nothing to migrate; the user record isn't an LFID user yet.
	u := &CommitteeUser{Name: "First Last"}
	assert.False(t, AuditUserNeedsMigration(u))
}

func TestAuditUserNeedsMigration_HasUsernameAndName_ReturnsFalse(t *testing.T) {
	u := &CommitteeUser{Username: "first-last", Name: "First Last"}
	assert.False(t, AuditUserNeedsMigration(u))
}

func TestAuditUserNeedsMigration_HasUsernameNoName_ReturnsTrue(t *testing.T) {
	// Has LFID but no profile name → needs backfill.
	u := &CommitteeUser{Username: "first-last"}
	assert.True(t, AuditUserNeedsMigration(u))
}

func TestAuditUserNeedsMigration_WhitespaceNameCountsAsMissing(t *testing.T) {
	u := &CommitteeUser{Username: "first-last", Name: "   "}
	assert.True(t, AuditUserNeedsMigration(u))
}

// ── NormalizeLegacyAuditUsers ─────────────────────────────────────────────────

func TestNormalizeLegacyAuditUsers_StructsPreservedWhenPresent(t *testing.T) {
	created := &CommitteeUser{Username: "first-last"}
	updated := &CommitteeUser{Username: "other-user"}
	gotCreated, gotUpdated := NormalizeLegacyAuditUsers(created, updated, "legacy-creator", "legacy-uploader")
	assert.Same(t, created, gotCreated)
	assert.Same(t, updated, gotUpdated)
}

func TestNormalizeLegacyAuditUsers_NilCreatedFallsBackToLegacyCreated(t *testing.T) {
	gotCreated, _ := NormalizeLegacyAuditUsers(nil, nil, "legacy-creator", "legacy-uploader")
	require.NotNil(t, gotCreated)
	assert.Equal(t, "legacy-creator", gotCreated.Username)
}

func TestNormalizeLegacyAuditUsers_NilCreatedFallsBackToLegacyUploaded(t *testing.T) {
	// legacyCreatedByUsername is empty → fall through to legacyUploadedByUsername.
	gotCreated, _ := NormalizeLegacyAuditUsers(nil, nil, "", "legacy-uploader")
	require.NotNil(t, gotCreated)
	assert.Equal(t, "legacy-uploader", gotCreated.Username)
}

func TestNormalizeLegacyAuditUsers_NilCreatedBothLegacyEmpty_ReturnsNils(t *testing.T) {
	gotCreated, gotUpdated := NormalizeLegacyAuditUsers(nil, nil, "", "")
	assert.Nil(t, gotCreated)
	assert.Nil(t, gotUpdated)
}

func TestNormalizeLegacyAuditUsers_NilUpdatedCopiesFromCreated(t *testing.T) {
	created := &CommitteeUser{Username: "first-last"}
	_, gotUpdated := NormalizeLegacyAuditUsers(created, nil, "", "")
	require.NotNil(t, gotUpdated)
	assert.Equal(t, "first-last", gotUpdated.Username)
	assert.NotSame(t, created, gotUpdated) // must be a clone, not the same pointer
}

func TestNormalizeLegacyAuditUsers_LegacyCreatedTrimsWhitespace(t *testing.T) {
	gotCreated, _ := NormalizeLegacyAuditUsers(nil, nil, "  spaced-user  ", "")
	require.NotNil(t, gotCreated)
	assert.Equal(t, "spaced-user", gotCreated.Username)
}

// ── CommitteeSettings.GetWriters / GetAuditors ────────────────────────────────

func TestGetWriters_NilReceiverReturnsNil(t *testing.T) {
	var s *CommitteeSettings
	assert.Nil(t, s.GetWriters())
}

func TestGetWriters_EmptySlice(t *testing.T) {
	s := &CommitteeSettings{}
	assert.Empty(t, s.GetWriters())
}

func TestGetWriters_ReturnsWritersSlice(t *testing.T) {
	writers := []CommitteeUser{{Username: "first-last"}}
	s := &CommitteeSettings{Writers: writers}
	assert.Equal(t, writers, s.GetWriters())
}

func TestGetAuditors_NilReceiverReturnsNil(t *testing.T) {
	var s *CommitteeSettings
	assert.Nil(t, s.GetAuditors())
}

func TestGetAuditors_EmptySlice(t *testing.T) {
	s := &CommitteeSettings{}
	assert.Empty(t, s.GetAuditors())
}

func TestGetAuditors_ReturnsAuditorsSlice(t *testing.T) {
	auditors := []CommitteeUser{{Username: "other-user"}}
	s := &CommitteeSettings{Auditors: auditors}
	assert.Equal(t, auditors, s.GetAuditors())
}

func TestGetWriters_DoesNotReturnAuditors(t *testing.T) {
	s := &CommitteeSettings{
		Writers:  []CommitteeUser{{Username: "writer-user"}},
		Auditors: []CommitteeUser{{Username: "auditor-user"}},
	}
	got := s.GetWriters()
	require.Len(t, got, 1)
	assert.Equal(t, "writer-user", got[0].Username)
}
