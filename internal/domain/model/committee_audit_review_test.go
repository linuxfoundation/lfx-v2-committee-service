// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"

	"github.com/stretchr/testify/require"
)

func TestCommitteeAuditReviewBoundaries(t *testing.T) {
	t.Run("after and projection must share the same state", func(t *testing.T) {
		for _, kind := range []ResourceType{ResourceCommittee, ResourceCommitteeSettings, ResourceCommitteeMember} {
			t.Run(string(kind), func(t *testing.T) {
				m := auditMutationFixture(t, kind, ActionCreated)
				message, err := NewCommitteeAuditMutation(m)
				require.NoError(t, err)
				switch value := m.Data.(type) {
				case CommitteeBase:
					value.Name = "Changed name"
					m.Data = value
				case CommitteeSettings:
					value.MemberVisibility = "private"
					m.Data = value
				case CommitteeMember:
					value.Role.Name = "Chair"
					m.Data = value
				}
				_, err = NewCommitteeAuditMutation(m)
				require.Error(t, err)
				_, err = NewCommitteeAuditBootstrap(m)
				require.Error(t, err)
				payload, err := json.Marshal(message)
				require.NoError(t, err)
				var raw map[string]any
				require.NoError(t, json.Unmarshal(payload, &raw))
				raw["data"] = m.Data
				payload, err = json.Marshal(raw)
				require.NoError(t, err)
				restored, err := indexertypes.RestoreMessageV2(payload, string(kind), ValidateCommitteeAuditContent)
				require.NoError(t, err)
				require.Error(t, PreflightCommitteeAuditMessage(restored, kind), "restoring a shared-valid message must not bypass domain state consistency")
			})
		}
	})
	t.Run("typed validation for missing indexing configuration", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
		m.IndexingConfig = nil
		_, err := NewCommitteeAuditMutation(m)
		var validation errs.Validation
		require.True(t, errors.As(err, &validation))
	})
	t.Run("charter email redaction is a legitimate state difference", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
		base := m.Data.(CommitteeBase)
		base.Charter = &Charter{URL: "https://example.com/charter", UpdatedBy: &CommitteeUser{Name: "First Last", Email: "first.last@example.com"}}
		var err error
		m.After, err = SnapshotCommittee(base)
		require.NoError(t, err)
		m.Data = base
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		require.NoError(t, PreflightCommitteeAuditMessage(message, ResourceCommittee))
	})
	t.Run("size admission measures sanitized legacy projection", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
		base := m.Data.(CommitteeBase)
		base.Charter = &Charter{URL: (&url.URL{Scheme: "https", Host: "example.com", Path: "/charter", User: url.UserPassword("first-last", strings.Repeat("a", CommitteeAuditMaxResourceBytes))}).String()}
		var err error
		m.Data = base
		m.After, err = SnapshotCommittee(base)
		require.NoError(t, err)
		_, err = NewCommitteeAuditBootstrap(m)
		require.NoError(t, err)
	})
	t.Run("projections require an after snapshot", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommitteeSettings, ActionCreated)
		m.After = nil
		m.IndexingConfig.HistoryCheckObject = "committee:other-committee"
		_, err := NewCommitteeAuditBootstrap(m)
		require.Error(t, err)
	})
	t.Run("committee indexing visibility must match source", func(t *testing.T) {
		for _, public := range []bool{false, true} {
			m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
			base := m.Data.(CommitteeBase)
			base.Public = public
			m.Data = base
			var err error
			m.After, err = SnapshotCommittee(base)
			require.NoError(t, err)
			m.IndexingConfig = base.IndexingConfig()
			message, err := NewCommitteeAuditMutation(m)
			require.NoError(t, err)
			require.NoError(t, PreflightCommitteeAuditMessage(message, ResourceCommittee))
			wrong := !public
			m.IndexingConfig.Public = &wrong
			_, err = NewCommitteeAuditMutation(m)
			require.Error(t, err)
		}
	})
	t.Run("source budget keeps create and future update eligible", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
		base := m.Data.(CommitteeBase)
		website := "https://example.com/" + strings.Repeat("a", 400*1024)
		base.Website = &website
		m.Data = base
		var err error
		m.After, err = SnapshotCommittee(base)
		require.NoError(t, err)
		_, err = NewCommitteeAuditMutation(m)
		require.Error(t, err)
		_, err = NewCommitteeAuditBootstrap(m)
		require.Error(t, err)
		website = "https://example.com/" + strings.Repeat("a", 100*1024)
		m.Data = base
		m.After, err = SnapshotCommittee(base)
		require.NoError(t, err)
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		require.NoError(t, PreflightCommitteeAuditMessage(message, ResourceCommittee))
		m.Before = m.After
		m.Action = ActionUpdated
		m.Scope.Before = m.Scope.After
		message, err = NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		require.NoError(t, PreflightCommitteeAuditMessage(message, ResourceCommittee))
	})
	t.Run("preflight reserves dispatch credential overhead", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionUpdated)
		m.Attribution.ActorJWT = strings.Repeat("a", 980*1024)
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		_, err = message.Envelope("preflight-only", ValidateCommitteeAuditContent)
		require.NoError(t, err)
		require.Error(t, PreflightCommitteeAuditMessage(message, ResourceCommittee))
	})
	t.Run("legacy charter credentials never enter history or projections", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionUpdated)
		base := m.Data.(CommitteeBase)
		legacyURL := (&url.URL{Scheme: "https", Host: "example.com", Path: "/charter", User: url.UserPassword("first-last", "secret-token")}).String()
		base.Charter = &Charter{URL: legacyURL}
		snapshot, err := SnapshotCommittee(base)
		require.NoError(t, err)
		require.Equal(t, "https://example.com/charter", snapshot.Charter.URL)
		m.Before = snapshot
		m.After = snapshot
		m.Data = base
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		raw, err := json.Marshal(message)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "secret-token")
		require.Equal(t, legacyURL, base.Charter.URL, "sanitizing must not mutate source pointers")
		for _, value := range []string{"", "https://example.com/charter", "http://example.com/charter"} {
			require.NoError(t, ValidateCharterURL(value))
		}
		require.Error(t, ValidateCharterURL(base.Charter.URL))
	})
}
