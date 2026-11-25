// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build unix

package v1auditimport

import (
	"os"
	"syscall"
)

// lockJournal takes a non-blocking advisory lock on the journal file itself:
// exclusive to write, shared to read. Any path or link to the file contends
// for the same lock, and closing the file or ending the process releases it.
func lockJournal(_ string, file *os.File, exclusive bool) (func(), error) {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}

// syncDir syncs a directory, so a name just created in it survives a crash.
func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
