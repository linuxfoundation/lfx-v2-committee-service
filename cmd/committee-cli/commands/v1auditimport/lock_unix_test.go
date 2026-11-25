// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build unix

package v1auditimport_test

// fileLock is true where the journal lock is an advisory lock on the file
// itself (lock_unix.go), so links share it and previews share it.
const fileLock = true
