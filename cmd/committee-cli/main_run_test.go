// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runWithArgs runs the entry point with the given arguments against a NATS address that nothing
// listens on, and returns what it wrote to stdout along with its error.
func runWithArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("NATS_URL", "nats://127.0.0.1:1")
	t.Setenv("NATS_TIMEOUT", "1s")

	oldArgs, oldStdout := os.Args, os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Args = append([]string{"committee-cli"}, args...)
	os.Stdout = writer
	t.Cleanup(func() { os.Args, os.Stdout = oldArgs, oldStdout })

	runErr := run()

	require.NoError(t, writer.Close())
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(out), runErr
}

func TestRun_ListFieldsNeedsNoNATS(t *testing.T) {
	out, err := runWithArgs(t, "sync", "committee-fields", "--list-fields")

	require.NoError(t, err, "--list-fields is a static catalog and must not need a NATS connection")
	assert.Contains(t, out, "Fields supported by committee-fields")
}

func TestRun_CommitteeFieldsStillNeedsNATSToDoRealWork(t *testing.T) {
	// The control for the test above: without --list-fields the same invocation connects to
	// NATS, which is unreachable here, so it must fail. This shows the broker really is down.
	_, err := runWithArgs(t, "sync", "committee-fields", "--committee-uid=committee-1", "--field=public", "--from=v1")

	require.Error(t, err)
}

func TestRun_StorageMode(t *testing.T) {
	args := []string{"sync", "committee-fields", "--committee-uid=committee-1", "--field=public", "--from=v1"}

	t.Run("a mode set on purpose that cannot be used stops the command before it connects", func(t *testing.T) {
		t.Setenv("STORAGE_MODE", "dual-write")

		_, err := runWithArgs(t, args...)

		require.ErrorContains(t, err, "invalid storage mode")
		assert.NotContains(t, err.Error(), "NATS", "nothing was connected")
	})

	t.Run("with it unset or kv the command goes on to connect, as it always did", func(t *testing.T) {
		for _, mode := range []string{"", "kv"} {
			t.Setenv("STORAGE_MODE", mode)

			_, err := runWithArgs(t, args...)

			require.ErrorContains(t, err, "failed to connect to NATS", "mode %q", mode)
		}
	})

	t.Run("a command that needs no infrastructure is not affected", func(t *testing.T) {
		t.Setenv("STORAGE_MODE", "dual-write")

		out, err := runWithArgs(t, "sync", "committee-fields", "--list-fields")

		require.NoError(t, err)
		assert.Contains(t, out, "Fields supported by committee-fields")
	})
}
