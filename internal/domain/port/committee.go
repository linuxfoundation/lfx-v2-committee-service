// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

// CommitteeReaderWriter provides access to committee reading and writing operations
type CommitteeReaderWriter interface {
	CommitteeReader
	CommitteeWriter
}
