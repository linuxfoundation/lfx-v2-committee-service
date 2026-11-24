// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build !unix

package report

import "testing"

func makeV1AuditFIFO(t *testing.T, _ string) {
	t.Helper()
	t.Skip("FIFOs need a unix platform")
}
