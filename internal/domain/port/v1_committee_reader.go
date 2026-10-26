// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// V1CommitteeReader provides read-only access to the LFX v1 committee record that
// the v1 sync helper mirrors into NATS, for operational data-repair tooling.
type V1CommitteeReader interface {
	// GetV1Committee returns the raw v1 committee (platform-collaboration__c) record
	// that corresponds to a v2 committee UID, resolved through the v1 sync helper's
	// v2-UID to v1-SFID mapping. The record is the sync helper's mirror of v1, so it
	// can lag live v1 by one replication cycle. It returns a NotFound error when the
	// committee has no live mapping or no mirrored record.
	GetV1Committee(ctx context.Context, committeeUID string) (map[string]any, error)
}
