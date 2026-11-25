// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestStore_DerivedAuditRevision(t *testing.T) {
	plain := newTestStore(t)
	s := NewAuditedStore(plain.pool)
	ctx := auditContext(t)
	require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
	_, businessRevision, err := s.GetBase(ctx, "committee-1")
	require.NoError(t, err)
	_, changed, err := s.UpdateTotalMembers(ctx, "committee-1", 42)
	require.NoError(t, err)
	require.True(t, changed)
	base, rowRevision, err := s.GetBase(ctx, "committee-1")
	require.NoError(t, err)
	require.Greater(t, rowRevision, businessRevision, "derived row writes still advance concurrency ETags")
	events := capturedAuditEnvelopes(t, s)
	last := events[len(events)-1]
	require.Equal(t, indexertypes.AuditOriginReindex, last.Audit.Origin)
	require.Equal(t, businessRevision, last.Audit.ResourceRevision)
	require.Nil(t, last.Audit.Content)
	base.Description = "Business edit"
	require.NoError(t, s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, rowRevision))
	events = capturedAuditEnvelopes(t, s)
	last = events[len(events)-1]
	require.Equal(t, indexertypes.AuditOriginMutation, last.Audit.Origin)
	require.Greater(t, last.Audit.ResourceRevision, businessRevision)

	require.NoError(t, s.CreateMember(ctx, testMember("member-1", "committee-1", "first.last@example.com")))
	member, revision, err := s.GetMember(ctx, "member-1")
	require.NoError(t, err)
	member.Role.Name = "Vice Chair"
	_, err = s.UpdateMember(model.WithCommitteeProjection(ctx), member, revision)
	require.Error(t, err, "a caller cannot hide a role change inside a projection")
	actual, _, err := s.GetMember(ctx, "member-1")
	require.NoError(t, err)
	require.NotEqual(t, "Vice Chair", actual.Role.Name)
}

func TestStore_AuditRetirement(t *testing.T) {
	plain := newTestStore(t)
	s := NewAuditedStore(plain.pool)
	ctx := auditContext(t)
	require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
	events := capturedAuditEnvelopes(t, s)
	id := events[0].Audit.EventID
	require.Error(t, s.RetireAuditEvent(ctx, id, 1), "a pending event cannot be retired")
	require.Error(t, s.PreviewAuditRetirement(ctx, id, 1))
	_, err := s.DeliverNextAuditEvent(ctx, func(context.Context, model.ResourceType, *indexertypes.MessageV2) error { return nil })
	require.NoError(t, err)
	count, err := s.PruneRetiredAuditEvents(ctx, time.Nanosecond)
	require.NoError(t, err)
	require.Zero(t, count, "PubAck alone never retires or prunes")
	verified, err := s.AuditDeliveryAttempt(ctx, id)
	require.NoError(t, err)
	require.NoError(t, s.PreviewAuditRetirement(ctx, id, verified))
	require.NoError(t, s.RetireAuditEvent(ctx, id, verified))
	require.NoError(t, s.RequeueAuditEvent(ctx, id))
	var retired bool
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT retired_at IS NOT NULL FROM committee_audit_outbox WHERE event_id = $1", id).Scan(&retired))
	require.False(t, retired, "replay cancels explicit retirement")
	_, err = s.DeliverNextAuditEvent(ctx, func(context.Context, model.ResourceType, *indexertypes.MessageV2) error { return nil })
	require.NoError(t, err)
	// The pending settings record may precede the requeued committee event.
	_, err = s.DeliverNextAuditEvent(ctx, func(context.Context, model.ResourceType, *indexertypes.MessageV2) error { return nil })
	require.NoError(t, err)
	require.Error(t, s.RetireAuditEvent(ctx, id, verified), "confirmation for an older delivery must not retire a replay")
	require.Error(t, s.PreviewAuditRetirement(ctx, id, verified))
	current, err := s.AuditDeliveryAttempt(ctx, id)
	require.NoError(t, err)
	require.NoError(t, s.RetireAuditEvent(ctx, id, current))
	_, err = s.pool.Exec(ctx, "UPDATE committee_audit_outbox SET retired_at = clock_timestamp() - interval '2 days' WHERE event_id = $1", id)
	require.NoError(t, err)
	count, err = s.PruneRetiredAuditEvents(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	_, _, err = s.ReadRetainedAuditEvent(ctx, id)
	require.Error(t, err)
	status, err := s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, status.Retained, "unretired settings event remains a repair source")
	var lineage int
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT count(*) FROM committee_audit_revisions").Scan(&lineage))
	require.Equal(t, 2, lineage, "retired payload pruning must not destroy revision lineage")
}

func TestStore_BootstrapAfterParentChange(t *testing.T) {
	s := newTestStore(t)
	ctx := auditContext(t)
	require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
	require.NoError(t, s.CreateMember(ctx, testMember("member-1", "committee-1", "first.last@example.com")))
	_, err := s.QueueAuditBootstrap(ctx, "committee-1")
	require.NoError(t, err)
	base, revision, err := s.GetBase(ctx, "committee-1")
	require.NoError(t, err)
	base.DisplayName = "Updated display name"
	base.ProjectUID = "project-2"
	require.NoError(t, s.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	_, err = s.PreviewAuditBootstrap(ctx, "committee-1")
	require.NoError(t, err)
	_, err = s.QueueAuditBootstrap(ctx, "committee-1")
	require.NoError(t, err)
	status, err := s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 6, status.Retained, "parent-only edits need a fresh projection identity, not an immutable-content conflict")
	var settings []*indexertypes.IndexerMessageEnvelope
	for _, event := range capturedAuditEnvelopes(t, s) {
		if event.IndexingConfig.AccessCheckObject == "committee:committee-1" && event.IndexingConfig.AccessCheckRelation == "auditor" {
			settings = append(settings, event)
		}
	}
	require.Len(t, settings, 2)
	require.Equal(t, settings[0].Audit.ResourceRevision, settings[1].Audit.ResourceRevision)
	require.NotEqual(t, settings[0].Audit.EventID, settings[1].Audit.EventID)
	require.Contains(t, settings[0].Audit.Scope.After, "project:project-1")
	require.Contains(t, settings[1].Audit.Scope.After, "project:project-2")
}

func TestStore_PruneRetiredAuditEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO committee_audit_outbox
		(event_id, object_type, object_id, resource_revision, payload, broker_delivered_at, retired_at)
		SELECT 'retired-' || i, 'committee', 'committee-' || i, i, convert_to('{}','UTF8'), now(), now() - interval '2 days'
		FROM generate_series(1,$1) i`, CommitteeAuditPruneBatchSize+1)
	require.NoError(t, err)
	count, err := s.PruneRetiredAuditEvents(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, CommitteeAuditPruneBatchSize, count)
	status, err := s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, status.Retained)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.PruneRetiredAuditEvents(cancelled, 24*time.Hour)
	require.Error(t, err)
	status, err = s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, status.Retained, "canceling a later batch cannot roll back the earlier committed batch")
	count, err = s.PruneRetiredAuditEvents(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
