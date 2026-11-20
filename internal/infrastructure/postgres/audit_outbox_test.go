// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createAuditCommittee(ctx context.Context, tx port.CommitteeAuditStore, eventID, uid string) ([]port.CommitteeAuditRecord, error) {
	base := model.CommitteeBase{UID: uid, ProjectUID: "project-1", Name: uid, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := tx.Create(ctx, &model.Committee{CommitteeBase: base}); err != nil {
		return nil, err
	}
	stored, revision, err := tx.GetBase(ctx, uid)
	if err != nil {
		return nil, err
	}
	base = *stored
	snapshot, err := model.SnapshotCommittee(base)
	if err != nil {
		return nil, err
	}
	message, err := model.NewCommitteeAuditMutation(model.CommitteeAuditMutation{
		ObjectType: model.ResourceCommittee, EventID: eventID, OccurredAt: base.CreatedAt,
		Revision: revision, Action: model.ActionCreated, Data: base,
		Attribution: indexertypes.AuditAttribution{ActorRef: "service:importer", EffectiveActorRef: "service:importer"},
		Scope:       indexertypes.AuditScope{After: []string{"project:project-1", "committee:" + uid}},
		After:       snapshot,
		IndexingConfig: &indexertypes.IndexingConfig{
			ObjectID: uid, AccessCheckObject: "committee:" + uid, AccessCheckRelation: "viewer",
			HistoryCheckObject: "committee:" + uid, HistoryCheckRelation: "auditor",
		},
	})
	if err != nil {
		return nil, err
	}
	return []port.CommitteeAuditRecord{{ObjectType: model.ResourceCommittee, Message: message}}, nil
}

func TestStore_MutateWithAudit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		change    func([]port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error)
		wantError bool
	}{
		{"commit", func(records []port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) { return records, nil }, false},
		{"callback failure", func([]port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) {
			return nil, errors.New("mutation failed")
		}, true},
		{"missing events", func([]port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) { return nil, nil }, true},
		{"projection cannot suppress mutation history", func(records []port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) {
			envelope, err := records[0].Message.Envelope("Bearer test-relay", model.ValidateCommitteeAuditContent)
			if err != nil {
				return nil, err
			}
			base := model.CommitteeBase{UID: "committee-1", ProjectUID: "project-1", Name: "Group"}
			snapshot, err := model.SnapshotCommittee(base)
			if err != nil {
				return nil, err
			}
			projection, err := model.NewCommitteeAuditBootstrap(model.CommitteeAuditMutation{
				ObjectType: model.ResourceCommittee, EventID: envelope.Audit.EventID, OccurredAt: envelope.Audit.OccurredAt,
				Revision: envelope.Audit.ResourceRevision, Action: model.ActionCreated, Data: base, After: snapshot,
				Attribution: envelope.Audit.Attribution, Scope: envelope.Audit.Scope, IndexingConfig: envelope.IndexingConfig,
			})
			if err != nil {
				return nil, err
			}
			return []port.CommitteeAuditRecord{{ObjectType: model.ResourceCommittee, Message: projection}}, nil
		}, true},
		{"invalid frozen event", func([]port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) {
			return []port.CommitteeAuditRecord{{ObjectType: model.ResourceCommittee}}, nil
		}, true},
		{"wrong subject", func(records []port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) {
			records[0].ObjectType = model.ResourceCommitteeMember
			return records, nil
		}, true},
		{"duplicate event rolls back all inserts", func(records []port.CommitteeAuditRecord) ([]port.CommitteeAuditRecord, error) {
			return append(records, records[0]), nil
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			err := s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
				records, err := createAuditCommittee(ctx, tx, "event-1", "committee-1")
				if err != nil {
					return nil, err
				}
				return tc.change(records)
			})
			if tc.wantError {
				require.Error(t, err)
				if tc.name == "projection cannot suppress mutation history" {
					require.ErrorContains(t, err, "source mutation cannot use a projection-only audit origin")
				}
				_, _, readErr := s.GetBase(ctx, "committee-1")
				requireNotFound(t, readErr)
				_, _, readErr = s.ReadRetainedAuditEvent(ctx, "event-1")
				requireNotFound(t, readErr)
			} else {
				require.NoError(t, err)
				_, _, err = s.GetBase(ctx, "committee-1")
				require.NoError(t, err)
				message, kind, err := s.ReadRetainedAuditEvent(ctx, "event-1")
				require.NoError(t, err)
				assert.Equal(t, model.ResourceCommittee, kind)
				payload, err := json.Marshal(message)
				require.NoError(t, err)
				assert.Contains(t, string(payload), `"headers":null`)
			}
		})
	}

	t.Run("failed mutation has no event", func(t *testing.T) {
		s := newTestStore(t)
		require.NoError(t, s.Create(ctx, &model.Committee{CommitteeBase: model.CommitteeBase{UID: "committee-1", ProjectUID: "project-1", Name: "committee-1"}}))
		err := s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
			return createAuditCommittee(ctx, tx, "event-1", "committee-1")
		})
		require.Error(t, err)
		_, _, err = s.ReadRetainedAuditEvent(ctx, "event-1")
		requireNotFound(t, err)
	})

	t.Run("event identity collision rolls back a different source mutation", func(t *testing.T) {
		s := newTestStore(t)
		require.NoError(t, s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
			return createAuditCommittee(ctx, tx, "event-1", "committee-1")
		}))
		err := s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
			return createAuditCommittee(ctx, tx, "event-1", "committee-2")
		})
		requireConflict(t, err)
		_, _, err = s.GetBase(ctx, "committee-2")
		requireNotFound(t, err)
	})
}

func TestStore_DeliverNextAuditEvent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	require.NoError(t, s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
		return createAuditCommittee(ctx, tx, "event-1", "committee-1")
	}))
	original, _, err := s.ReadRetainedAuditEvent(ctx, "event-1")
	require.NoError(t, err)
	want, err := json.Marshal(original)
	require.NoError(t, err)
	var attempts int
	deliver := func(_ context.Context, kind model.ResourceType, message *indexertypes.MessageV2) error {
		attempts++
		assert.Equal(t, model.ResourceCommittee, kind)
		payload, err := json.Marshal(message)
		require.NoError(t, err)
		assert.Equal(t, want, payload, "source identity, time, profiles and snapshots must not be regenerated")
		if attempts == 1 {
			return errors.New("unknown publish outcome containing secret-token")
		}
		return nil
	}
	found, err := s.DeliverNextAuditEvent(ctx, deliver)
	require.True(t, found)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-token")
	var lastAttempt, nextAttempt time.Time
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT last_attempt_at, next_attempt_at FROM committee_audit_outbox WHERE event_id = 'event-1'").Scan(&lastAttempt, &nextAttempt))
	require.True(t, nextAttempt.After(lastAttempt), "failure must schedule persisted backoff independently of test runner timing")
	require.NoError(t, s.RequeueAuditEvent(ctx, "event-1"))
	found, err = s.DeliverNextAuditEvent(ctx, deliver)
	require.NoError(t, err)
	require.True(t, found)
	found, err = s.DeliverNextAuditEvent(ctx, deliver)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, 2, attempts)
	var delivered bool
	var storedAttempts int
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT broker_delivered_at IS NOT NULL, delivery_attempts FROM committee_audit_outbox WHERE event_id = 'event-1'").Scan(&delivered, &storedAttempts))
	assert.True(t, delivered)
	assert.Equal(t, 2, storedAttempts)
	// Resource/account disappearance must not remove the retained repair copy.
	_, revision, err := s.GetBase(ctx, "committee-1")
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, "committee-1", revision))
	retained, _, err := s.ReadRetainedAuditEvent(ctx, "event-1")
	require.NoError(t, err)
	got, err := json.Marshal(retained)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestStore_QueueAuditBootstrap(t *testing.T) {
	s := newTestStore(t)
	ctx := auditContext(t)
	require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
	require.NoError(t, s.CreateMember(ctx, testMember("member-1", "committee-1", "first.last@example.com")))
	_, revision, err := s.GetBase(ctx, "committee-1")
	require.NoError(t, err)
	count, err := s.PreviewAuditBootstrap(ctx, "committee-1")
	require.NoError(t, err)
	require.Equal(t, 3, count)
	previewStatus, err := s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.Zero(t, previewStatus.Retained, "dry-run must validate without persisting events")
	for i := 0; i < 2; i++ {
		count, err := s.QueueAuditBootstrap(ctx, "committee-1")
		require.NoError(t, err)
		require.Equal(t, 3, count)
	}
	status, err := s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 6, status.Retained, "each explicit bootstrap is a distinct projection operation")
	require.EqualValues(t, 6, status.Pending)
	_, gotRevision, err := s.GetBase(ctx, "committee-1")
	require.NoError(t, err)
	require.Equal(t, revision, gotRevision, "bootstrap must not mutate source revisions")
	var ids []string
	for {
		found, err := s.DeliverNextAuditEvent(ctx, func(_ context.Context, kind model.ResourceType, message *indexertypes.MessageV2) error {
			envelope, err := message.Envelope("Bearer relay-test-token", model.ValidateCommitteeAuditContent)
			require.NoError(t, err)
			require.NoError(t, model.ValidateCommitteeAuditEnvelope(envelope, kind))
			require.Equal(t, indexertypes.AuditOriginBackfill, envelope.Audit.Origin)
			require.Nil(t, envelope.Audit.Content, "bootstrap is not historical mutation history")
			ids = append(ids, envelope.Audit.EventID)
			return nil
		})
		require.NoError(t, err)
		if !found {
			break
		}
	}
	require.Len(t, ids, 6)
	require.NoError(t, s.RequeueAuditEvent(ctx, ids[0]))
	status, err = s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, status.Pending)
}

func TestStore_AuditDeliveryFairness(t *testing.T) {
	s := newTestStore(t)
	ctx := auditContext(t)
	for _, id := range []string{"event-1", "event-2"} {
		require.NoError(t, s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
			return createAuditCommittee(ctx, tx, id, "committee-"+id)
		}))
	}
	var visited []string
	deliver := func(_ context.Context, _ model.ResourceType, message *indexertypes.MessageV2) error {
		envelope, err := message.Envelope("Bearer test-relay", model.ValidateCommitteeAuditContent)
		require.NoError(t, err)
		visited = append(visited, envelope.Audit.EventID)
		if envelope.Audit.EventID == "event-1" {
			return errors.New("permanent admission failure")
		}
		return nil
	}
	found, err := s.DeliverNextAuditEvent(ctx, deliver)
	require.True(t, found)
	require.Error(t, err)
	found, err = s.DeliverNextAuditEvent(ctx, deliver)
	require.True(t, found)
	require.NoError(t, err)
	require.Equal(t, []string{"event-1", "event-2"}, visited)
}
