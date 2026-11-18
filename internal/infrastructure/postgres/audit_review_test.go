// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestStore_AuditDeliveryTimeout(t *testing.T) {
	s := newTestStore(t)
	ctx := auditContext(t)
	for _, id := range []string{"event-1", "event-2"} {
		require.NoError(t, s.MutateWithAudit(ctx, func(tx port.CommitteeAuditStore) ([]port.CommitteeAuditRecord, error) {
			return createAuditCommittee(ctx, tx, id, "committee-"+id)
		}))
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	found, err := s.DeliverNextAuditEvent(bounded, func(deliveryCtx context.Context, _ model.ResourceType, _ *indexertypes.MessageV2) error {
		<-deliveryCtx.Done()
		return deliveryCtx.Err()
	})
	require.True(t, found)
	require.Error(t, err)
	var attempts int
	var last, next time.Time
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT delivery_attempts,last_attempt_at,next_attempt_at FROM committee_audit_outbox WHERE event_id = 'event-1'").Scan(&attempts, &last, &next))
	require.Equal(t, 1, attempts)
	require.True(t, next.After(last), "delivery timeout must commit retry backoff")
	found, err = s.DeliverNextAuditEvent(ctx, func(_ context.Context, _ model.ResourceType, m *indexertypes.MessageV2) error {
		e, err := m.Envelope("Bearer test", model.ValidateCommitteeAuditContent)
		require.NoError(t, err)
		require.Equal(t, "event-2", e.Audit.EventID, "the newer eligible event must not be starved")
		return nil
	})
	require.NoError(t, err)
	require.True(t, found)
}

func TestStore_BootstrapPreflight(t *testing.T) {
	s := newTestStore(t)
	ctx := auditContext(t)
	committee := testCommittee("committee-1", "project-1", "Group")
	website := "https://example.com/" + strings.Repeat("a", 400*1024)
	committee.Website = &website
	require.NoError(t, s.Create(ctx, committee))
	_, err := s.PreviewAuditBootstrap(ctx, "committee-1")
	require.Error(t, err)
	_, err = s.QueueAuditBootstrap(ctx, "committee-1")
	require.Error(t, err)
	status, err := s.AuditQueueStatus(ctx)
	require.NoError(t, err)
	require.Zero(t, status.Retained)
	var revisions int
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT count(*) FROM committee_audit_revisions").Scan(&revisions))
	require.Zero(t, revisions)
}

func TestStore_BootstrapLockFailure(t *testing.T) {
	s := newTestStore(t)
	ctx := auditContext(t)
	require.NoError(t, s.Create(ctx, testCommittee("committee-1", "project-1", "Group")))
	tx, err := s.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "SELECT uid FROM committees WHERE uid = 'committee-1' FOR UPDATE")
	require.NoError(t, err)
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = s.PreviewAuditBootstrap(bounded, "committee-1")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "not found")
}
