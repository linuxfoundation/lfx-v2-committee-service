// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/require"
)

type auditLegacyPublisher struct {
	port.CommitteePublisher
	calls int
}

func (p *auditLegacyPublisher) Indexer(context.Context, string, any, bool) error {
	p.calls++
	return nil
}

func TestNewAuditedCommitteePublisher(t *testing.T) {
	for _, tc := range []struct {
		subject string
		want    int
	}{
		{constants.IndexCommitteeSubject, 0}, {constants.IndexCommitteeSettingsSubject, 0}, {constants.IndexCommitteeMemberSubject, 0},
		{constants.IndexCommitteeInviteSubject, 1}, {constants.IndexCommitteeApplicationSubject, 1},
	} {
		t.Run(tc.subject, func(t *testing.T) {
			legacy := &auditLegacyPublisher{}
			publisher := NewAuditedCommitteePublisher(legacy)
			require.NoError(t, publisher.Indexer(context.Background(), tc.subject, "resource", true))
			require.Equal(t, tc.want, legacy.calls)
		})
	}
}
