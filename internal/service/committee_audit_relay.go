// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// CommitteeAuditRelay dispatches retained events until shutdown. Failed events
// are rescheduled by the outbox so one poison event cannot starve the queue.
// PubAck is broker persistence only; retained source reconciliation is still required.
func CommitteeAuditRelay(ctx context.Context, outbox port.CommitteeAuditOutbox, deliver func(context.Context, model.ResourceType, *indexertypes.MessageV2) error) {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		found, err := outbox.DeliverNextAuditEvent(attemptCtx, deliver)
		cancel()
		if err != nil && ctx.Err() == nil {
			// Neither an external error nor an event payload belongs in logs.
			slog.ErrorContext(ctx, "committee audit delivery failed; retained event will retry")
		}
		if found && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

// CommitteeAuditRetention applies the configured limit without pruning pending
// or merely broker-delivered events. It shares the API's cancellation lifecycle.
func CommitteeAuditRetention(ctx context.Context, store port.CommitteeAuditRetirement, retention time.Duration) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		count, err := store.PruneRetiredAuditEvents(operationCtx, retention)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "retired audit pruning failed; records retained")
		}
		if err == nil && count > 0 {
			slog.InfoContext(ctx, "pruned retired committee audit records", "records", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
