// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build unix

package report

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func makeV1AuditFIFO(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, syscall.Mkfifo(path, 0600))
}
