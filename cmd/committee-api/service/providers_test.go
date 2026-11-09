// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
)

func TestLFXSelfServeBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		environment string
		want        string
	}{
		{
			name:    "explicit base URL takes precedence",
			baseURL: "https://custom.example.com",
			want:    "https://custom.example.com",
		},
		{
			name:        "prod environment",
			environment: "prod",
			want:        "https://app.lfx.dev",
		},
		{
			name:        "production alias",
			environment: "production",
			want:        "https://app.lfx.dev",
		},
		{
			name:        "staging environment",
			environment: "staging",
			want:        "https://app.staging.lfx.dev",
		},
		{
			name:        "stg alias",
			environment: "stg",
			want:        "https://app.staging.lfx.dev",
		},
		{
			name:        "stage alias",
			environment: "stage",
			want:        "https://app.staging.lfx.dev",
		},
		{
			name:        "dev environment",
			environment: "dev",
			want:        "https://app.dev.lfx.dev",
		},
		{
			name:        "development alias",
			environment: "development",
			want:        "https://app.dev.lfx.dev",
		},
		{
			name: "unset environment defaults to prod",
			want: "https://app.lfx.dev",
		},
		{
			name:        "unrecognized environment defaults to prod",
			environment: "qa",
			want:        "https://app.lfx.dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LFX_SELF_SERVE_BASE_URL", tt.baseURL)
			t.Setenv("LFX_ENVIRONMENT", tt.environment)

			assert.Equal(t, tt.want, LFXSelfServeBaseURL())
		})
	}
}

func TestInitStorageMode(t *testing.T) {
	ctx := context.Background()
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("with STORAGE_MODE unset the mode is kv and the service is as it has always been", func(t *testing.T) {
		mode, err := initStorageMode(ctx, env(nil))
		require.NoError(t, err)
		assert.Equal(t, composite.ModeKV, mode)

		mode, err = initStorageMode(ctx, env(map[string]string{"POSTGRES_ENABLED": "true", "POSTGRES_URL": "postgres://user:password@localhost/db"}))
		require.NoError(t, err, "PostgreSQL being on does not change the mode")
		assert.Equal(t, composite.ModeKV, mode)
	})

	t.Run("the effective mode is logged, so it can be read from the pod logs", func(t *testing.T) {
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		t.Cleanup(func() { slog.SetDefault(previous) })

		_, err := initStorageMode(ctx, env(nil))

		require.NoError(t, err)
		assert.Contains(t, logs.String(), `msg="storage mode" mode=kv`)
	})

	t.Run("a mode set on purpose that cannot be used is an error and never kv", func(t *testing.T) {
		for name, values := range map[string]map[string]string{
			"not recognized": {"STORAGE_MODE": "dual-write"},
			"no connection":  {"STORAGE_MODE": "dual"},
		} {
			mode, err := initStorageMode(ctx, env(values))
			require.Error(t, err, name)
			assert.Empty(t, mode, name)
		}
	})
}

// natsInitChildEnv marks the test binary as the child that runs natsInit, which ends the process when it fails.
const natsInitChildEnv = "LFX_TEST_RUN_NATS_INIT"

// TestNatsInit_StorageMode runs natsInit in a child process, because a configuration it cannot accept ends the process
// with log.Fatalf. NATS is pointed at an address nothing listens on, so how far natsInit got shows in which error ends it.
func TestNatsInit_StorageMode(t *testing.T) {
	if os.Getenv(natsInitChildEnv) == "1" {
		natsInit(context.Background())
		return
	}

	run := func(t *testing.T, storageMode string) (string, error) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestNatsInit_StorageMode$")
		cmd.Env = append(os.Environ(), natsInitChildEnv+"=1", "STORAGE_MODE="+storageMode,
			"NATS_URL=nats://127.0.0.1:1", "NATS_TIMEOUT=1s", "NATS_MAX_RECONNECT=0", "NATS_RECONNECT_WAIT=100ms")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	t.Run("a mode set on purpose that cannot be used ends startup before NATS is contacted", func(t *testing.T) {
		out, err := run(t, "dual-write")

		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.NotZero(t, exitErr.ExitCode())
		assert.Contains(t, out, "invalid storage mode")
		assert.NotContains(t, out, "failed to create NATS client", "the check runs before NATS is connected")
	})

	t.Run("with STORAGE_MODE unset or kv startup goes on to connect to NATS, as it always did", func(t *testing.T) {
		for _, mode := range []string{"", "kv"} {
			out, err := run(t, mode)

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr, "mode %q", mode)
			assert.Contains(t, out, "failed to create NATS client", "mode %q: the only thing in the way is the NATS that is not there", mode)
			assert.NotContains(t, out, "invalid storage mode", "mode %q", mode)
		}
	})
}
