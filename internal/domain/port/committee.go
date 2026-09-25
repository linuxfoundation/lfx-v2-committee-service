// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// CommitteeReaderWriter provides access to committee reading and writing operations
type CommitteeReaderWriter interface {
	CommitteeReader
	CommitteeWriter

	IsReady(ctx context.Context) error
}
