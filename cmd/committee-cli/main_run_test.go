// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

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

func TestCommitteeAuditCommandContext(t *testing.T) {
	for _, tc := range []struct {
		command, subcommand string
		projection          bool
	}{
		{"sync", "member-avatar-attribute", true}, {"sync", "member-project-attribute", true}, {"sync", "promote-email-only-members", true},
		{"sync", "committee-fields", false}, {"sync", "member-cdp-org-id", false}, {"audit", "bootstrap", false},
	} {
		t.Run(tc.command+"/"+tc.subcommand, func(t *testing.T) {
			ctx := committeeAuditCommandContext(context.Background(), tc.command, tc.subcommand)
			require.NotNil(t, ctx.Value(constants.AuditAttributionContextID))
			projection, _ := ctx.Value(constants.AuditProjectionContextID).(bool)
			require.Equal(t, tc.projection, projection)
		})
	}
}

func TestRun_ListFieldsNeedsNoNATS(t *testing.T) {
	out, err := runWithArgs(t, "sync", "committee-fields", "--list-fields")

	require.NoError(t, err, "--list-fields is a static catalog and must not need a NATS connection")
	assert.Contains(t, out, "Fields supported by committee-fields")
}

func TestRun_V1AuditHistoryIsOffline(t *testing.T) {
	t.Setenv("STORAGE_MODE", "invalid-mode")
	input := filepath.Join(t.TempDir(), "audit.ndjson")
	require.NoError(t, os.WriteFile(input, nil, 0600))
	out, err := runWithArgs(t, "report", "v1-audit-history", "--source=fixture-v1", "--input="+input)
	require.NoError(t, err, "offline preview must run before NATS/storage configuration and connections")
	require.Contains(t, out, `"dry_run": true`)
	require.Contains(t, out, `"rows": 0`)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &report), "stdout must be one JSON report, not mixed initialization logs")
}

func TestRun_UnknownCommandDoesNotEchoPrivateArguments(t *testing.T) {
	const private = "/private/first.last@example.com/audit.ndjson"
	for _, tc := range []struct {
		name string
		args []string
	}{
		// A misplaced value-taking flag turns its value into the subcommand positional.
		{"misplaced flag", []string{"report", "--input", private, "v1-audit-history"}},
		{"misplaced flag with help", []string{"report", "--input", private, "v1-audit-history", "--help"}},
		{"unknown command", []string{private, "v1-audit-history"}},
		{"unknown command with help", []string{private, "v1-audit-history", "--help"}},
		// A value that looks like a command name is not echoed either.
		{"misplaced username", []string{"report", "--committee-id", "first-last", "v1-audit-history"}},
		{"misplaced username with help", []string{"report", "--committee-id", "first-last", "v1-audit-history", "--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldStderr := os.Stderr
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			os.Stderr = writer
			t.Cleanup(func() { os.Stderr = oldStderr })

			out, runErr := runWithArgs(t, tc.args...)

			require.NoError(t, writer.Close())
			stderr, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Error(t, runErr)
			assert.Contains(t, runErr.Error(), "unknown")
			for _, output := range []string{runErr.Error(), out, string(stderr)} {
				assert.NotContains(t, output, "first.last@example.com")
				assert.NotContains(t, output, "first-last")
			}
		})
	}
}

func TestRun_V1AuditHistoryBrokenStdoutDiscardsDraft(t *testing.T) {
	if raw := os.Getenv("COMMITTEE_CLI_TEST_ARGS"); raw != "" {
		// Child process: run the CLI with stdout already broken.
		var args []string
		require.NoError(t, json.Unmarshal([]byte(raw), &args))
		os.Args = append([]string{"committee-cli"}, args...)
		if run() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if runtime.GOOS == "windows" {
		t.Skip("SIGPIPE is a unix signal")
	}
	dir := t.TempDir()
	input, plan := filepath.Join(dir, "audit.ndjson"), filepath.Join(dir, "draft.ndjson")
	require.NoError(t, os.WriteFile(input, nil, 0600))
	args, err := json.Marshal([]string{"report", "v1-audit-history", "--source=fixture-v1", "--input=" + input, "--plan-output=" + plan})
	require.NoError(t, err)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close(), "a stdout pipe with no reader breaks on the first write")
	defer func() { _ = writer.Close() }()

	// A stuck child is killed rather than hanging the package until its timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRun_V1AuditHistoryBrokenStdoutDiscardsDraft$")
	child.Env = append(os.Environ(), "COMMITTEE_CLI_TEST_ARGS="+string(args))
	child.Stdout = writer
	runErr := child.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr)
	require.Equal(t, 1, exitErr.ExitCode(), "the summary write must fail with an error, not kill the process with SIGPIPE")
	_, statErr := os.Stat(plan)
	require.True(t, os.IsNotExist(statErr), "the draft must be removed")
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

func TestRun_AuditOperationsDoNotRequireNATS(t *testing.T) {
	t.Setenv("COMMITTEE_AUDIT_ENABLED", "false")
	t.Setenv("STORAGE_MODE", "postgres")
	t.Setenv("POSTGRES_URL", "postgres://127.0.0.1:1/committee?sslmode=disable")
	for _, key := range []string{"PGHOST", "PGUSER", "PGPASSWORD", "PGDATABASE"} {
		t.Setenv(key, "")
	}
	for _, args := range [][]string{
		{"audit", "status"}, {"audit", "bootstrap", "--committee", "committee-1"},
		{"audit", "replay", "--event-id", "event-1"},
		{"audit", "retire", "--event-id", "event-1", "--delivery-attempt", "1", "--confirmed-persisted"},
		{"audit", "prune"},
	} {
		t.Run(args[1], func(t *testing.T) {
			_, err := runWithArgs(t, args...)
			require.ErrorContains(t, err, "connect")
			require.NotContains(t, err.Error(), "NATS", "audit operations must reach the PostgreSQL connector even when NATS is unreachable")
		})
	}
}

func TestRun_AuditedOperationRefusesLegacyMaintenance(t *testing.T) {
	t.Setenv("COMMITTEE_AUDIT_ENABLED", "true")
	t.Setenv("STORAGE_MODE", "postgres")
	t.Setenv("COMMITTEE_AUDIT_OUTBOX_RETENTION", "24h")
	for _, key := range []string{"AUTH_SOURCE", "JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL", "REPOSITORY_SOURCE", "MESSAGING_SOURCE"} {
		t.Setenv(key, "")
	}
	for _, args := range [][]string{
		{"sync", "reindex-settings"}, {"sync", "backfill-weekly-brief-index"},
		{"sync", "repair-settings-index"}, {"storage", "migrate"},
	} {
		t.Run(args[1], func(t *testing.T) {
			_, err := runWithArgs(t, args...)
			require.ErrorContains(t, err, "legacy storage/projection maintenance must be paused")
		})
	}
}

func TestPrintUsage_ListsTheStorageMode(t *testing.T) {
	var out strings.Builder

	printUsage(&out, buildRegistry())

	assert.Contains(t, out.String(), "STORAGE_MODE")
	assert.Contains(t, out.String(), "default: kv")
}

// The PostgreSQL variables matter to every command in the PostgreSQL modes, not only to storage migrate, and the pool there
// has to hold at least two connections; the built-in help says so.
func TestPrintUsage_SaysWhereThePostgreSQLVariablesApply(t *testing.T) {
	var out strings.Builder

	printUsage(&out, buildRegistry())

	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "  POSTGRES_URL") || strings.HasPrefix(line, "  POSTGRES_MAX_CONNS") {
			assert.Contains(t, line, "every command in dual, dual-pg and postgres", line)
		}
	}
	assert.Contains(t, out.String(), "at least 2")
}
