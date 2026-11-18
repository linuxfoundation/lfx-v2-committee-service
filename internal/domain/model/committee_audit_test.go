// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func auditMutationFixture(t *testing.T, kind ResourceType, action MessageAction) CommitteeAuditMutation {
	t.Helper()
	attribution, err := indexertypes.NewAuditAttribution(indexertypes.AuditActor{Ref: "user:first-last", JWT: "test.actor.evidence"}, nil)
	require.NoError(t, err)
	uid := "committee-1"
	var snapshot, data any
	switch kind {
	case ResourceCommittee:
		snapshot = CommitteeAuditSnapshot{UID: uid, ProjectUID: "project-1", Name: "Group"}
		data = CommitteeBase{UID: uid, ProjectUID: "project-1", Name: "Group"}
	case ResourceCommitteeSettings:
		snapshot = CommitteeSettingsAuditSnapshot{UID: uid, ProjectUID: "project-1"}
		data = CommitteeSettings{UID: uid}
	case ResourceCommitteeMember:
		uid = "member-1"
		snapshot = CommitteeMemberAuditSnapshot{UID: uid, CommitteeUID: "committee-1", ProjectUID: "project-1", Username: "first-last"}
		data = CommitteeMember{CommitteeMemberBase: CommitteeMemberBase{UID: uid, CommitteeUID: "committee-1", ProjectUID: "project-1", Username: "first-last"}}
	}
	m := CommitteeAuditMutation{
		ObjectType: kind, EventID: "event-1", OccurredAt: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
		Revision: 42, Action: action, Attribution: attribution, Data: data,
		IndexingConfig: &indexertypes.IndexingConfig{
			ObjectID: uid, AccessCheckObject: "committee:committee-1", AccessCheckRelation: "viewer",
			HistoryCheckObject: "committee:committee-1", HistoryCheckRelation: "auditor",
		},
	}
	if kind == ResourceCommitteeSettings {
		m.IndexingConfig.AccessCheckObject = "committee_settings:" + uid
		m.IndexingConfig.AccessCheckRelation = "auditor"
	}
	refs := []string{"project:project-1", "committee:committee-1"}
	if action != ActionCreated {
		m.Before, m.Scope.Before = snapshot, refs
	}
	if action != ActionDeleted {
		m.After, m.Scope.After = snapshot, refs
	} else {
		m.Data = uid
	}
	return m
}

func TestNewCommitteeAuditMutation(t *testing.T) {
	t.Run("publisher output fields are rejected and optional evidence is retained", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
		m.Attribution.ActorJWT = "expired.test.evidence"
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		payload, err := json.Marshal(message)
		require.NoError(t, err)
		require.Contains(t, string(payload), `"actor_jwt":"expired.test.evidence"`)
		for _, field := range []string{"actor_profile", "effective_actor_profile", "actor_verified", "effective_actor_verified", "actor_identity"} {
			var raw map[string]any
			require.NoError(t, json.Unmarshal(payload, &raw))
			attribution := raw["audit"].(map[string]any)["attribution"].(map[string]any)
			attribution[field] = nil
			if field == "actor_verified" || field == "effective_actor_verified" {
				attribution[field] = false
			}
			invalid, err := json.Marshal(raw)
			require.NoError(t, err)
			_, err = indexertypes.RestoreMessageV2(invalid, string(ResourceCommittee), ValidateCommitteeAuditContent)
			require.Error(t, err)
		}
	})
	for _, kind := range []ResourceType{ResourceCommittee, ResourceCommitteeSettings, ResourceCommitteeMember} {
		for _, action := range []MessageAction{ActionCreated, ActionUpdated, ActionDeleted} {
			t.Run(string(kind)+"/"+string(action), func(t *testing.T) {
				m := auditMutationFixture(t, kind, action)
				message, err := NewCommitteeAuditMutation(m)
				require.NoError(t, err)
				payload, err := json.Marshal(message)
				require.NoError(t, err)
				assert.Contains(t, string(payload), `"headers":null`)
				restored, err := indexertypes.RestoreMessageV2(payload, string(kind), ValidateCommitteeAuditContent)
				require.NoError(t, err)
				envelope, err := restored.Envelope("Bearer fresh-service-token", ValidateCommitteeAuditContent)
				require.NoError(t, err)
				require.NoError(t, ValidateCommitteeAuditEnvelope(envelope, kind))
				assert.Equal(t, m.EventID, envelope.Audit.EventID)
				assert.Equal(t, m.Revision, envelope.Audit.ResourceRevision)
				assert.Equal(t, m.OccurredAt, envelope.Audit.OccurredAt)
				assert.Equal(t, m.Scope, envelope.Audit.Scope)
				assert.Equal(t, m.Attribution, envelope.Audit.Attribution)
				assert.Equal(t, "committee:committee-1", envelope.IndexingConfig.HistoryCheckObject)
				if action == ActionDeleted {
					assert.Equal(t, m.IndexingConfig.ObjectID, envelope.Data)
				}
				// Fresh dispatch credentials never modify the retained repair source.
				again, err := json.Marshal(restored)
				require.NoError(t, err)
				assert.Equal(t, payload, again)
				assert.NotContains(t, string(again), "fresh-service-token")
			})
		}
	}

	t.Run("delegation and evidence are frozen", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
		var err error
		m.Attribution, err = indexertypes.NewAuditAttribution(
			indexertypes.AuditActor{Ref: "service:importer"},
			&indexertypes.AuditActor{Ref: "user:first-last", JWT: "test.effective.evidence"})
		require.NoError(t, err)
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		m.Attribution.EffectiveActorJWT = "changed"
		envelope, err := message.Envelope("Bearer relay", ValidateCommitteeAuditContent)
		require.NoError(t, err)
		assert.Equal(t, "service:importer", envelope.Audit.Attribution.ActorRef)
		assert.Equal(t, "user:first-last", envelope.Audit.Attribution.EffectiveActorRef)
		assert.Equal(t, "test.effective.evidence", envelope.Audit.Attribution.EffectiveActorJWT)
	})

	t.Run("webhook credentials are excluded without mutating source", func(t *testing.T) {
		m := auditMutationFixture(t, ResourceCommitteeSettings, ActionUpdated)
		secret := "https://example.com/webhook/secret-token"
		settings := m.Data.(CommitteeSettings)
		settings.ChatWebhookURL = &secret
		m.Data = settings
		var err error
		m.After, err = SnapshotCommitteeSettings(settings, "project-1")
		require.NoError(t, err)
		message, err := NewCommitteeAuditMutation(m)
		require.NoError(t, err)
		payload, err := json.Marshal(message)
		require.NoError(t, err)
		assert.NotContains(t, string(payload), "secret-token")
		assert.NotContains(t, string(payload), "chat_webhook_url")
		assert.Equal(t, secret, *settings.ChatWebhookURL)
	})

	for _, tc := range []struct {
		name   string
		change func(*CommitteeAuditMutation)
	}{
		{"missing actor", func(m *CommitteeAuditMutation) { m.Attribution = indexertypes.AuditAttribution{} }},
		{"missing source time", func(m *CommitteeAuditMutation) { m.OccurredAt = time.Time{} }},
		{"missing event identity", func(m *CommitteeAuditMutation) { m.EventID = "" }},
		{"missing source revision", func(m *CommitteeAuditMutation) { m.Revision = 0 }},
		{"missing config", func(m *CommitteeAuditMutation) { m.IndexingConfig = nil }},
		{"wrong resource identity", func(m *CommitteeAuditMutation) { m.IndexingConfig.ObjectID = "another" }},
		{"wrong history anchor", func(m *CommitteeAuditMutation) { m.IndexingConfig.HistoryCheckObject = "project:project-1" }},
		{"viewer history relation", func(m *CommitteeAuditMutation) { m.IndexingConfig.HistoryCheckRelation = "viewer" }},
		{"missing project scope", func(m *CommitteeAuditMutation) { m.Scope.After = []string{"committee:committee-1"} }},
		{"arbitrary snapshot", func(m *CommitteeAuditMutation) { m.After = map[string]any{"uid": "committee-1", "token": "secret"} }},
		{"raw projection", func(m *CommitteeAuditMutation) {
			m.Data = map[string]any{"uid": "committee-1", "authorization": "secret"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := auditMutationFixture(t, ResourceCommittee, ActionCreated)
			tc.change(&m)
			_, err := NewCommitteeAuditMutation(m)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestValidateCommitteeAuditContent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		publisher string
		kind      ResourceType
		schema    string
		snapshot  string
	}{
		{"unknown publisher", "other", ResourceCommittee, "committee.v1", `{}`},
		{"unknown version", CommitteeAuditPublisher, ResourceCommittee, "committee.v2", `{}`},
		{"wrong resource", CommitteeAuditPublisher, ResourceCommitteeMember, "committee.v1", `{}`},
		{"unknown field", CommitteeAuditPublisher, ResourceCommittee, "committee.v1", `{"uid":"c","token":"secret"}`},
		{"missing fields", CommitteeAuditPublisher, ResourceCommittee, "committee.v1", `{"uid":"c"}`},
		{"null snapshot", CommitteeAuditPublisher, ResourceCommittee, "committee.v1", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCommitteeAuditContent(tc.publisher, string(tc.kind), indexertypes.AuditContent{
				Schema: tc.schema, Representation: indexertypes.AuditRepresentationSnapshots, After: json.RawMessage(tc.snapshot),
			})
			require.Error(t, err)
			assert.False(t, strings.Contains(err.Error(), "secret"))
		})
	}
	for _, tc := range []struct {
		name   string
		kind   ResourceType
		change func(map[string]any)
	}{
		{"case variant", ResourceCommittee, func(raw map[string]any) { raw["UID"] = raw["uid"]; delete(raw, "uid") }},
		{"nested secret", ResourceCommitteeMember, func(raw map[string]any) { raw["role"].(map[string]any)["token"] = "secret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := auditMutationFixture(t, tc.kind, ActionCreated)
			message, err := NewCommitteeAuditMutation(m)
			require.NoError(t, err)
			envelope, err := message.Envelope("Bearer test", ValidateCommitteeAuditContent)
			require.NoError(t, err)
			content := *envelope.Audit.Content
			require.NoError(t, ValidateCommitteeAuditContent(CommitteeAuditPublisher, string(tc.kind), content))
			var raw map[string]any
			require.NoError(t, json.Unmarshal(content.After, &raw))
			tc.change(raw)
			content.After, err = json.Marshal(raw)
			require.NoError(t, err)
			require.Error(t, ValidateCommitteeAuditContent(CommitteeAuditPublisher, string(tc.kind), content))
		})
	}
}

func TestCommitteeAuditSnapshotCoverage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   any
		snapshot any
		excluded []string
		added    []string
	}{
		{"committee", CommitteeBase{}, CommitteeAuditSnapshot{}, nil, nil},
		{"settings", CommitteeSettings{}, CommitteeSettingsAuditSnapshot{}, []string{"chat_webhook_url"}, []string{"project_uid"}},
		{"member", CommitteeMemberBase{}, CommitteeMemberAuditSnapshot{}, nil, nil},
		{"role", CommitteeMemberRole{}, CommitteeAuditRole{}, nil, nil},
		{"voting", CommitteeMemberVotingInfo{}, CommitteeAuditVoting{}, nil, nil},
		{"organization", CommitteeMemberOrganization{}, CommitteeAuditOrganization{}, nil, nil},
		{"user", CommitteeUser{}, CommitteeAuditUser{}, nil, nil},
		{"calendar", Calendar{}, CommitteeAuditCalendar{}, nil, nil},
		{"key date", KeyDate{}, CommitteeAuditKeyDate{}, nil, nil},
		{"external source", ExternalSource{}, CommitteeAuditExternalSource{}, nil, nil},
		{"charter", Charter{}, CommitteeAuditCharter{}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := func(value any, excluded []string) []string {
				var names []string
				typ := reflect.TypeOf(value)
				for i := 0; i < typ.NumField(); i++ {
					name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
					if !containsAuditRef(excluded, name) {
						names = append(names, name)
					}
				}
				return names
			}
			want := append(fields(tc.source, tc.excluded), tc.added...)
			assert.ElementsMatch(t, want, fields(tc.snapshot, nil), "a source schema change requires explicit audit coverage/privacy review")
		})
	}
}

func TestWithAuditAttribution(t *testing.T) {
	attribution, err := indexertypes.NewAuditAttribution(indexertypes.AuditActor{Ref: "user:first-last", JWT: "test.actor.evidence"}, nil)
	require.NoError(t, err)
	ctx, err := WithAuditAttribution(context.Background(), attribution)
	require.NoError(t, err)
	attribution.ActorJWT = "Changed"
	captured := ctx.Value(constants.AuditAttributionContextID).(indexertypes.AuditAttribution)
	assert.Equal(t, "test.actor.evidence", captured.ActorJWT)
	assert.Equal(t, "test.actor.evidence", captured.EffectiveActorJWT)
	_, err = WithAuditAttribution(context.Background(), indexertypes.AuditAttribution{})
	require.Error(t, err)
}
