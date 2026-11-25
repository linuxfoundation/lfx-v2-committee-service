// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// CommitteeAuditStore is the transaction-bound storage surface for audited
// committee mutations. Do not retain it or use it after the callback returns.
type CommitteeAuditStore interface {
	CommitteeBaseReader
	CommitteeBaseWriter
	CommitteeSettingsReader
	CommitteeSettingsWriter
	CommitteeMemberReader
	CommitteeMemberWriter
	CommitteeCascadeDeleter
	NextAuditRevision(context.Context) (uint64, error)
}

// CommitteeAuditRecord pairs the immutable envelope with its subject resource
// type. The storage boundary revalidates the pair before committing either.
type CommitteeAuditRecord struct {
	ObjectType model.ResourceType
	Message    *indexertypes.MessageV2
}

// CommitteeAuditTransactions commits source changes and frozen audit records in
// the same transaction. The callback must use only the supplied storage handle
// for mutations; publishing or other external side effects do not belong in it.
type CommitteeAuditTransactions interface {
	MutateWithAudit(context.Context, func(CommitteeAuditStore) ([]CommitteeAuditRecord, error)) error
}

// CommitteeAuditOutbox dispatches retained immutable events with broker acknowledgment.
type CommitteeAuditOutbox interface {
	DeliverNextAuditEvent(context.Context, func(context.Context, model.ResourceType, *indexertypes.MessageV2) error) (bool, error)
}

// CommitteeAuditRetirement prunes only explicitly retired repair sources.
type CommitteeAuditRetirement interface {
	PruneRetiredAuditEvents(context.Context, time.Duration) (int64, error)
}
