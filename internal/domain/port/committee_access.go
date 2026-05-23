// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// CommitteeAccessChecker verifies whether the caller carries a particular
// committee-scoped relation.
//
// Production deployments rely on Heimdall to enforce write access at the edge
// before the request reaches this service, so the default implementation can
// be a no-op (always allow). The port exists so handlers like
// GET /committees/{uid}/weekly-briefs/current can express the writer-relation
// requirement explicitly, and so unit tests can mock a denial path without
// introducing new FGA contract entries.
type CommitteeAccessChecker interface {
	// CanWriteCommittee returns nil if the caller (identified by the
	// principal already attached to ctx via JWT parsing) has writer access
	// on committee:{committeeUID}. Implementations must return a non-nil
	// error of type errors.Forbidden when access is denied.
	CanWriteCommittee(ctx context.Context, committeeUID string) error
}
