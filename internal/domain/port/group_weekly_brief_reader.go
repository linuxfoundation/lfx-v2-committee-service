// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// GroupWeeklyBriefReader reads working-group weekly briefs from the persistence
// layer. The Phase-1 read path only needs lookup by (committee, window). A miss
// returns (nil, nil, nil) — the endpoint surfaces a miss as HTTP 200 with a
// null brief, per the BFF contract.
type GroupWeeklyBriefReader interface {
	// GetGroupWeeklyBriefForWindow returns the brief for committeeUID whose
	// WindowStart matches the date encoded in window.WindowStart, plus any
	// raw throttle JSON bytes for the same window. A miss is not an error.
	GetGroupWeeklyBriefForWindow(
		ctx context.Context,
		committeeUID string,
		window model.GroupWeeklyBrief,
	) (*model.GroupWeeklyBrief, []byte, error)
}
