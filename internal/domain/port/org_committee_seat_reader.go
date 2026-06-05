// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// OrgCommitteeSeatReader reads the committee members held by a B2B organization, scoped to a
// project family, for the Org Lens Board & Committee tab (LFXV2-1865). The live implementation does a
// privileged own-data read of committee-service's datastore (the NATS KV committee-members-by-organization
// index now, Postgres post-migration) so private-committee seats are included (the account-level b2b_org
// gate is enforced at the edge by Heimdall, not per-seat FGA).
type OrgCommitteeSeatReader interface {
	// ListOrgCommitteeSeats returns the committee members for orgSFID (the 18-char Salesforce
	// Account SFID = canonical b2b_org uid), scoped to projectUIDs (foundation root + descendants).
	// When projectUIDs is empty the result is scoped by organization only.
	ListOrgCommitteeSeats(ctx context.Context, orgSFID string, projectUIDs []string) ([]*model.CommitteeMember, error)
}
