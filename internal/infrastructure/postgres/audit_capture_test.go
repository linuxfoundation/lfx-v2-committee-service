// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func auditContext(t *testing.T) context.Context {
	t.Helper()
	ctx, err := model.WithAuditServiceActor(context.Background(), "committee-test-system")
	require.NoError(t, err)
	return ctx
}

func capturedAuditEnvelopes(t *testing.T, s *Store) []*indexertypes.IndexerMessageEnvelope {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), "SELECT object_type, payload FROM committee_audit_outbox ORDER BY captured_at, event_id")
	require.NoError(t, err)
	defer rows.Close()
	var envelopes []*indexertypes.IndexerMessageEnvelope
	for rows.Next() {
		var kind string
		var payload []byte
		require.NoError(t, rows.Scan(&kind, &payload))
		message, err := indexertypes.RestoreMessageV2(payload, kind, model.ValidateCommitteeAuditContent)
		require.NoError(t, err)
		envelope, err := message.Envelope("Bearer fresh-test-relay", model.ValidateCommitteeAuditContent)
		require.NoError(t, err)
		require.NoError(t, model.ValidateCommitteeAuditEnvelope(envelope, model.ResourceType(kind)))
		envelopes = append(envelopes, envelope)
	}
	require.NoError(t, rows.Err())
	return envelopes
}

func TestNewAuditedStore(t *testing.T) {
	t.Run("a lagging source sequence cannot commit an unaudited update", func(t *testing.T) {
		plain := newTestStore(t)
		ctx := auditContext(t)
		importCtx, err := WithRevision(ctx, 5)
		require.NoError(t, err)
		require.NoError(t, plain.Create(importCtx, testCommittee("committee-1", "project-1", "Group")))
		s := NewAuditedStore(plain.pool)
		for _, nextRevision := range []int{4, 5} {
			_, err := plain.pool.Exec(ctx, "SELECT setval('committee_revision_seq', $1, true)", nextRevision-1)
			require.NoError(t, err)
			base, revision, err := s.GetBase(ctx, "committee-1")
			require.NoError(t, err)
			before := base.Description
			base.Description = "Must not bypass source revision ordering"
			require.Error(t, s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
			actual, actualRevision, err := s.GetBase(ctx, "committee-1")
			require.NoError(t, err)
			require.Equal(t, before, actual.Description)
			require.EqualValues(t, 5, actualRevision)
			require.Empty(t, capturedAuditEnvelopes(t, s))
		}
	})

	t.Run("create update and cascade retain complete attributable source events", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		ctx := auditContext(t)
		started := time.Now().UTC()
		committee := testCommittee("committee-1", "project-1", "Group")
		secret := "https://example.com/webhook/secret-token"
		committee.ChatWebhookURL = &secret
		require.NoError(t, s.Create(ctx, committee))
		member := testMember("member-1", "committee-1", "first.last@example.com")
		require.NoError(t, s.CreateMember(ctx, member))
		base, revision, err := s.GetBase(ctx, "committee-1")
		require.NoError(t, err)
		base.Description = "Revised description"
		require.NoError(t, s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
		settings, revision, err := s.GetSettings(ctx, "committee-1")
		require.NoError(t, err)
		settings.MemberVisibility = "private"
		require.NoError(t, s.UpdateSetting(ctx, settings, revision))
		storedMember, revision, err := s.GetMember(ctx, "member-1")
		require.NoError(t, err)
		storedMember.Role.Name = "Vice Chair"
		_, err = s.UpdateMember(ctx, storedMember, revision)
		require.NoError(t, err)
		_, revision, err = s.GetBase(ctx, "committee-1")
		require.NoError(t, err)
		removed, err := s.DeleteCommitteeCascade(ctx, "committee-1", revision)
		require.NoError(t, err)
		assert.Equal(t, []string{"member-1"}, removed.MemberUIDs)
		envelopes := capturedAuditEnvelopes(t, s)
		require.Len(t, envelopes, 9)
		seen := make(map[string]bool)
		var deletedMembers, deletedSettings, deletedCommittees int
		var descriptionChange, roleChange bool
		for _, envelope := range envelopes {
			assert.False(t, seen[envelope.Audit.EventID])
			seen[envelope.Audit.EventID] = true
			assert.False(t, envelope.Audit.OccurredAt.Before(started))
			assert.Equal(t, "service:committee-test-system", envelope.Audit.Attribution.ActorRef)
			assert.Equal(t, "service:committee-test-system", envelope.Audit.Attribution.EffectiveActorRef)
			assert.NotZero(t, envelope.Audit.ResourceRevision)
			assert.Equal(t, "committee:committee-1", envelope.IndexingConfig.HistoryCheckObject)
			assert.Equal(t, "auditor", envelope.IndexingConfig.HistoryCheckRelation)
			payload, err := json.Marshal(envelope)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), "secret-token")
			assert.NotContains(t, string(payload), "chat_webhook_url")
			if string(envelope.Action) == "updated" && envelope.Audit.Content.Schema == "committee.v1" {
				var before, after model.CommitteeAuditSnapshot
				require.NoError(t, json.Unmarshal(envelope.Audit.Content.Before, &before))
				require.NoError(t, json.Unmarshal(envelope.Audit.Content.After, &after))
				require.NotEqual(t, before.Description, after.Description)
				require.Equal(t, "Revised description", after.Description)
				descriptionChange = true
			}
			if string(envelope.Action) == "updated" && envelope.IndexingConfig.ObjectID == "member-1" {
				assert.Contains(t, string(envelope.Audit.Content.Before), `"name":"Chair"`)
				assert.Contains(t, string(envelope.Audit.Content.After), `"name":"Vice Chair"`)
				roleChange = true
			}
			if string(envelope.Action) == "deleted" {
				assert.Nil(t, envelope.Audit.Scope.After)
				assert.ElementsMatch(t, []string{"committee:committee-1", "project:project-1"}, envelope.Audit.Scope.Before)
				assert.NotEmpty(t, envelope.Audit.Content.Before)
				switch envelope.IndexingConfig.AccessCheckObject {
				case "committee_settings:committee-1":
					deletedSettings++
				case "committee:committee-1":
					if envelope.IndexingConfig.ObjectID == "member-1" {
						deletedMembers++
					} else {
						deletedCommittees++
					}
				}
			}
		}
		assert.True(t, descriptionChange)
		assert.True(t, roleChange)
		assert.Equal(t, 1, deletedMembers)
		assert.Equal(t, 1, deletedSettings)
		assert.Equal(t, 1, deletedCommittees)
	})

	t.Run("social listening access changes remain authoritative settings mutations", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		ctx := auditContext(t)
		require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
		for _, enabled := range []bool{true, false} {
			settings, revision, err := s.GetSettings(ctx, "committee-1")
			require.NoError(t, err)
			settings.MembersCanViewSocialListening = enabled
			require.NoError(t, s.UpdateSetting(ctx, settings, revision))
			events := capturedAuditEnvelopes(t, s)
			event := events[len(events)-1]
			require.Equal(t, indexertypes.AuditOriginMutation, event.Audit.Origin)
			var before, after model.CommitteeSettingsAuditSnapshot
			require.NoError(t, json.Unmarshal(event.Audit.Content.Before, &before))
			require.NoError(t, json.Unmarshal(event.Audit.Content.After, &after))
			require.Equal(t, !enabled, before.MembersCanViewSocialListening)
			require.Equal(t, enabled, after.MembersCanViewSocialListening)
		}
	})

	t.Run("missing attribution and stale writes cannot change source or append success", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		committee := testCommittee("committee-1", "project-1", "Group")
		require.Error(t, s.Create(context.Background(), committee))
		_, _, err := s.GetBase(context.Background(), "committee-1")
		requireNotFound(t, err)
		assert.Empty(t, capturedAuditEnvelopes(t, s))
		ctx := auditContext(t)
		require.NoError(t, s.Create(ctx, committee))
		base, revision, err := s.GetBase(ctx, "committee-1")
		require.NoError(t, err)
		base.Description = "Must not be committed"
		requireConflict(t, s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision+100))
		actual, _, err := s.GetBase(ctx, "committee-1")
		require.NoError(t, err)
		assert.NotEqual(t, base.Description, actual.Description)
		assert.Len(t, capturedAuditEnvelopes(t, s), 2)
	})

	t.Run("counter noops do not fabricate changes and real system changes do", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		ctx := auditContext(t)
		committee := testCommittee("committee-1", "project-1", "Group")
		require.NoError(t, s.Create(ctx, committee))
		_, changed, err := s.UpdateTotalMembers(ctx, "committee-1", committee.TotalMembers)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Len(t, capturedAuditEnvelopes(t, s), 2)
		_, changed, err = s.UpdateTotalMembers(ctx, "committee-1", committee.TotalMembers+1)
		require.NoError(t, err)
		assert.True(t, changed)
		assert.Len(t, capturedAuditEnvelopes(t, s), 3)
	})

	t.Run("event construction failure rolls back the source mutation", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		committee := testCommittee("committee-1", "invalid scope", "Group")
		err := s.Create(auditContext(t), committee)
		require.Error(t, err)
		_, _, err = s.GetBase(context.Background(), "committee-1")
		requireNotFound(t, err)
		assert.Empty(t, capturedAuditEnvelopes(t, s))
	})

	t.Run("outbox insert failure rolls back committee and settings", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		ctx, err := WithRevision(auditContext(t), 123)
		require.NoError(t, err)
		_, err = s.pool.Exec(ctx, `INSERT INTO committee_audit_outbox
			(event_id, object_type, object_id, resource_revision, payload)
			VALUES ('existing-event', 'committee', 'committee-1', 123, $1)`, []byte(`{}`))
		require.NoError(t, err)
		requireConflict(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
		_, _, err = s.GetBase(ctx, "committee-1")
		requireNotFound(t, err)
		_, _, err = s.GetSettings(ctx, "committee-1")
		requireNotFound(t, err)
		var count int
		require.NoError(t, s.pool.QueryRow(ctx, "SELECT count(*) FROM committee_audit_outbox").Scan(&count))
		assert.Equal(t, 1, count)
	})

	t.Run("member deletion retains its scope while parent remains live", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		ctx := auditContext(t)
		require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
		require.NoError(t, s.CreateMember(ctx, testMember("member-1", "committee-1", "first.last@example.com")))
		_, revision, err := s.GetMember(ctx, "member-1")
		require.NoError(t, err)
		require.NoError(t, s.DeleteMember(ctx, "member-1", revision))
		envelopes := capturedAuditEnvelopes(t, s)
		require.Len(t, envelopes, 4)
		deleted := envelopes[len(envelopes)-1]
		assert.Equal(t, "deleted", string(deleted.Action))
		assert.Greater(t, deleted.Audit.ResourceRevision, revision)
		assert.Equal(t, "member-1", deleted.Data)
		assert.Equal(t, "committee:committee-1", deleted.IndexingConfig.HistoryCheckObject)
	})

	t.Run("project moves retain both scopes and member source before images", func(t *testing.T) {
		plain := newTestStore(t)
		s := NewAuditedStore(plain.pool)
		ctx := auditContext(t)
		require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
		member := testMember("member-1", "committee-1", "first.last@example.com")
		member.ProjectUID = "project-1"
		require.NoError(t, s.CreateMember(ctx, member))
		base, revision, err := s.GetBase(ctx, "committee-1")
		require.NoError(t, err)
		base.ProjectUID = "project-2"
		require.NoError(t, s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
		member, revision, err = s.GetMember(ctx, "member-1")
		require.NoError(t, err)
		member.ProjectUID = "project-2"
		_, err = s.UpdateMember(ctx, member, revision)
		require.NoError(t, err)
		envelopes := capturedAuditEnvelopes(t, s)
		var moves int
		for _, envelope := range envelopes {
			if string(envelope.Action) != "updated" {
				continue
			}
			moves++
			assert.ElementsMatch(t, []string{"committee:committee-1", "project:project-1"}, envelope.Audit.Scope.Before)
			assert.ElementsMatch(t, []string{"committee:committee-1", "project:project-2"}, envelope.Audit.Scope.After)
			assert.Contains(t, string(envelope.Audit.Content.Before), `"project_uid":"project-1"`)
			assert.Contains(t, string(envelope.Audit.Content.After), `"project_uid":"project-2"`)
		}
		assert.Equal(t, 2, moves)
	})
}
