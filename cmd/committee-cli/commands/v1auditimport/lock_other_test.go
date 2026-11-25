// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build !unix

package v1auditimport_test

// fileLock is false where the journal lock is a <path>.lock file
// (lock_other.go): keyed on the path, and exclusive for previews too.
const fileLock = false
