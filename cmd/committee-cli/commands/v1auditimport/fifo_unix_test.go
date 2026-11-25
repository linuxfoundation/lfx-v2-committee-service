// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

//go:build unix

package v1auditimport_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport/v1auditimporttest"
)

func TestNamedPipesAreRefusedWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	path, _ := v1auditimporttest.WriteEvents(t, dir)
	opened, err := v1auditimport.OpenEvents(context.Background(), path)
	require.NoError(t, err)
	defer opened.Close()
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0600))

	done := make(chan []error, 1)
	go func() {
		var errs []error
		_, err := v1auditimport.OpenEvents(context.Background(), fifo)
		errs = append(errs, err)
		_, err = v1auditimport.ReadAuthorization(fifo)
		errs = append(errs, err)
		for _, create := range []bool{false, true} {
			_, err = v1auditimport.OpenJournal(fifo, opened, create)
			errs = append(errs, err)
		}
		done <- errs
	}()
	select {
	case errs := <-done:
		for _, err := range errs {
			require.ErrorContains(t, err, "regular file")
		}
	case <-time.After(5 * time.Second):
		// Best effort: unblock an open waiting for a writer, without blocking
		// here if none is waiting, then fail.
		if writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
		t.Fatal("opening a named pipe blocked")
	}
}
