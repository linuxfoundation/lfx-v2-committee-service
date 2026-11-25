// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build !unix

package v1auditimport

import "os"

// lockJournal creates a <path>.lock file exclusively, outside Unix. It is keyed
// on the path, and a killed run leaves it behind for the operator to remove.
func lockJournal(path string, _ *os.File, _ bool) (func(), error) {
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_ = lock.Close()
	return func() { _ = os.Remove(lockPath) }, nil
}

// syncDir is a no-op outside Unix, where a directory cannot be opened to sync.
func syncDir(string) error { return nil }
