// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package uid

import (
	"github.com/google/uuid"
)

// NewUUID generates a new UUID string
func New() string {
	return uuid.New().String()
}
