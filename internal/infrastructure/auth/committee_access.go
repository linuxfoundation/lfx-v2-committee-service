// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// HeimdallEdgeAccessChecker is the production CommitteeAccessChecker. It
// performs no in-process check because Heimdall enforces the writer relation
// on the committee:{uid} object at the API edge before the request reaches
// this service. The type exists so unit tests can substitute a denying stub
// without altering production authz behaviour.
type HeimdallEdgeAccessChecker struct{}

// NewHeimdallEdgeAccessChecker returns the default access checker.
func NewHeimdallEdgeAccessChecker() port.CommitteeAccessChecker {
	return &HeimdallEdgeAccessChecker{}
}

// CanWriteCommittee always returns nil: Heimdall already verified writer
// access at the edge. Returning an error here would double-deny.
func (HeimdallEdgeAccessChecker) CanWriteCommittee(_ context.Context, _ string) error {
	return nil
}
