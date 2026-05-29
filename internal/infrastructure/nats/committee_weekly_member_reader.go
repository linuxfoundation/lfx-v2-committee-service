// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// CommitteeWeeklyMemberReader is the live implementation of
// port.CommitteeWeeklyMemberReader. It re-uses the existing
// port.CommitteeMemberReader to load the whole committee membership and then
// partitions the records into "joined this week" and "updated this week"
// buckets.
//
// Members are always considered a non-public source (privacy-protected
// information), so the orchestrator sets PrivateSourcePresent=true on the
// brief whenever this reader returns any rows.
type CommitteeWeeklyMemberReader struct {
	memberReader port.CommitteeMemberReader
}

// NewCommitteeWeeklyMemberReader builds the live reader from any
// port.CommitteeMemberReader. In production this is the NATS storage adapter;
// tests can pass a stub directly.
func NewCommitteeWeeklyMemberReader(r port.CommitteeMemberReader) *CommitteeWeeklyMemberReader {
	return &CommitteeWeeklyMemberReader{memberReader: r}
}

// ListMemberActivityForWindow returns the joined / updated member sets for the
// committee within [windowStart, windowEnd].
//
//   - "Joined" = created_at within window
//   - "Updated" = updated_at within window AND created_at outside window
//     (avoids double-counting joins)
func (r *CommitteeWeeklyMemberReader) ListMemberActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) (port.WeeklyMemberActivity, error) {
	members, err := r.memberReader.ListMembers(ctx, committeeUID)
	if err != nil {
		return port.WeeklyMemberActivity{}, err
	}
	out := port.WeeklyMemberActivity{}
	for _, m := range members {
		if m == nil {
			continue
		}
		createdIn := !m.CreatedAt.Before(windowStart) && !m.CreatedAt.After(windowEnd)
		updatedIn := !m.UpdatedAt.Before(windowStart) && !m.UpdatedAt.After(windowEnd)
		switch {
		case createdIn:
			out.Joined = append(out.Joined, m)
		case updatedIn:
			out.Updated = append(out.Updated, m)
		}
	}
	return out, nil
}
