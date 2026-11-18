// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

type auditedCommitteePublisher struct{ port.CommitteePublisher }

// NewAuditedCommitteePublisher fences legacy search writes for transactionally
// audited types. All other resource, FGA and domain-event publication is unchanged.
// Select it only together with the authoritative audited PostgreSQL store.
func NewAuditedCommitteePublisher(publisher port.CommitteePublisher) port.CommitteePublisher {
	return &auditedCommitteePublisher{CommitteePublisher: publisher}
}

func (p *auditedCommitteePublisher) Indexer(ctx context.Context, subject string, message any, sync bool) error {
	switch subject {
	case constants.IndexCommitteeSubject, constants.IndexCommitteeSettingsSubject, constants.IndexCommitteeMemberSubject:
		// The committed source transaction already retained the immutable event.
		// Never race that event with an unversioned best-effort projection.
		return nil
	default:
		return p.CommitteePublisher.Indexer(ctx, subject, message, sync)
	}
}
