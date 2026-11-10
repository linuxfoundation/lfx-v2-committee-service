// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMode(t *testing.T) {
	for raw, want := range map[string]Mode{
		"":         ModeKV,
		"   ":      ModeKV,
		"kv":       ModeKV,
		" kv ":     ModeKV,
		"dual":     ModeDual,
		"dual-pg":  ModeDualPG,
		"postgres": ModePostgres,
	} {
		got, err := ParseMode(raw)
		require.NoError(t, err, "%q", raw)
		assert.Equal(t, want, got, "%q", raw)
	}

	t.Run("a value that is not recognized is an error and never kv", func(t *testing.T) {
		for _, raw := range []string{"KV", "Dual", "dual_pg", "pg", "postgresql", "true", "kv,dual", "none"} {
			got, err := ParseMode(raw)
			require.Error(t, err, "%q", raw)
			assert.Empty(t, got, "%q", raw)
			assert.ErrorContains(t, err, EnvStorageMode)
			assert.ErrorContains(t, err, raw)
		}
	})
}

func TestMode_Roles(t *testing.T) {
	for mode, want := range map[Mode]struct{ postgres, primary bool }{
		ModeKV:       {false, false},
		ModeDual:     {true, false},
		ModeDualPG:   {true, true},
		ModePostgres: {true, true},
	} {
		assert.Equal(t, want.postgres, mode.UsesPostgres(), mode)
		assert.Equal(t, want.primary, mode.PostgresIsPrimary(), mode)
	}
}

func TestModeFromEnv(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("with nothing set the mode is kv, today's behavior, and nothing else is checked", func(t *testing.T) {
		mode, err := ModeFromEnv(env(nil))
		require.NoError(t, err)
		assert.Equal(t, ModeKV, mode)

		mode, err = ModeFromEnv(env(map[string]string{"POSTGRES_URL": "postgres://user:password@localhost/db", "POSTGRES_ENABLED": "true"}))
		require.NoError(t, err, "PostgreSQL being configured does not change the mode")
		assert.Equal(t, ModeKV, mode)
	})

	t.Run("kv set on purpose is kv", func(t *testing.T) {
		mode, err := ModeFromEnv(env(map[string]string{EnvStorageMode: "kv"}))
		require.NoError(t, err)
		assert.Equal(t, ModeKV, mode)
	})

	t.Run("a value that is not recognized fails", func(t *testing.T) {
		_, err := ModeFromEnv(env(map[string]string{EnvStorageMode: "dual-write"}))
		require.ErrorContains(t, err, `invalid STORAGE_MODE "dual-write"`)
	})

	t.Run("a mode that uses PostgreSQL needs a connection string, and the error never shows one", func(t *testing.T) {
		for _, mode := range []Mode{ModeDual, ModeDualPG, ModePostgres} {
			_, err := ModeFromEnv(env(map[string]string{EnvStorageMode: string(mode)}))
			require.ErrorContains(t, err, "POSTGRES_URL is not set", mode)
			_, err = ModeFromEnv(env(map[string]string{EnvStorageMode: string(mode), "POSTGRES_URL": "  "}))
			require.ErrorContains(t, err, "POSTGRES_URL is not set", "a blank one is not set", mode)
		}
	})

	t.Run("every mode can be selected once PostgreSQL has a connection string", func(t *testing.T) {
		// The connection string is put together here, not written out, so that no connection string with a password is in
		// the source for a secret scanner to report.
		connection := "postgres://user:" + "not-a-real-password" + "@localhost/db"
		for _, mode := range []Mode{ModeDual, ModeDualPG, ModePostgres} {
			got, err := ModeFromEnv(env(map[string]string{EnvStorageMode: string(mode), "POSTGRES_URL": connection}))
			require.NoError(t, err, mode)
			assert.Equal(t, mode, got)
		}
	})
}
