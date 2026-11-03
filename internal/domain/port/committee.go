// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// ReadinessChecker reports whether a dependency can serve requests. A nil error means ready.
type ReadinessChecker interface {
	IsReady(ctx context.Context) error
}

// CommitteeReaderWriter provides access to committee reading and writing operations
type CommitteeReaderWriter interface {
	CommitteeReader
	CommitteeWriter

	IsReady(ctx context.Context) error
}
