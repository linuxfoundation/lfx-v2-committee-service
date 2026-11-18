// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package audit

import (
	"context"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestOperation_Run(t *testing.T) {
	t.Run("argument validation is offline", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			args      []string
			wantError bool
		}{
			{"status", []string{"--help"}, false}, {"bootstrap", nil, true}, {"replay", nil, true},
			{"status", []string{"unexpected"}, true}, {"status", nil, true},
			{"retire", nil, true}, {"retire", []string{"--event-id", "event-1"}, true},
		} {
			err := (&operation{tc.name}).Run(context.Background(), commands.RunContext{Args: tc.args})
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		}
	})
	t.Run("bootstrap preview status and immutable replay", func(t *testing.T) {
		store := postgrestest.NewStore(t)
		ctx := context.Background()
		now := time.Now().UTC()
		require.NoError(t, store.Create(ctx, &model.Committee{CommitteeBase: model.CommitteeBase{
			UID: "committee-1", ProjectUID: "project-1", Name: "Group", CreatedAt: now, UpdatedAt: now,
		}}))
		var closed int
		run := func(name string, args ...string) error {
			return (&operation{name}).Run(ctx, commands.RunContext{Args: args, PostgresStore: func(context.Context) (*postgres.Store, func(), error) { return store, func() { closed++ }, nil }})
		}
		require.NoError(t, run("bootstrap", "--committee", "committee-1", "--dry-run"))
		status, err := store.AuditQueueStatus(ctx)
		require.NoError(t, err)
		require.Zero(t, status.Retained)
		require.NoError(t, run("bootstrap", "--committee", "committee-1"))
		require.Error(t, run("retire", "--event-id", "event-1", "--delivery-attempt", "1", "--dry-run"))
		require.NoError(t, run("status"))
		var eventID string
		found, err := store.DeliverNextAuditEvent(ctx, func(_ context.Context, _ model.ResourceType, message *indexertypes.MessageV2) error {
			envelope, err := message.Envelope("Bearer test-relay", model.ValidateCommitteeAuditContent)
			require.NoError(t, err)
			eventID = envelope.Audit.EventID
			return nil
		})
		require.NoError(t, err)
		require.True(t, found)
		require.Error(t, run("retire", "--event-id", eventID))
		require.NoError(t, run("retire", "--event-id", eventID, "--delivery-attempt", "1", "--dry-run"))
		require.NoError(t, run("retire", "--event-id", eventID, "--delivery-attempt", "1", "--confirmed-persisted"))
		require.NoError(t, run("replay", "--event-id", eventID, "--dry-run"))
		status, err = store.AuditQueueStatus(ctx)
		require.NoError(t, err)
		require.Zero(t, status.Pending)
		require.NoError(t, run("replay", "--event-id", eventID))
		status, err = store.AuditQueueStatus(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 1, status.Pending)
		require.Equal(t, 8, closed, "each connected operation must release its connector")
	})
}
