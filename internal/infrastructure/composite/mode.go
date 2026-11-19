// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package composite holds what is needed to run the NATS KV and PostgreSQL stores side by side: the store mode
// the API and the CLI share and the metrics for the failures a secondary store must never turn into a failed
// request.
package composite

import (
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// Mode says which store is authoritative for reads and writes and which one, if any, follows it.
type Mode string

// The store modes. KV is the default and is what the service has always done.
const (
	// ModeKV reads and writes NATS KV only.
	ModeKV Mode = "kv"
	// ModeDual reads KV and writes KV first, then PostgreSQL; a PostgreSQL failure never fails the request.
	ModeDual Mode = "dual"
	// ModeDualPG reads PostgreSQL and writes PostgreSQL first, then KV.
	ModeDualPG Mode = "dual-pg"
	// ModePostgres reads and writes PostgreSQL only.
	ModePostgres Mode = "postgres"
)

// EnvStorageMode is the environment variable the API and the CLI read the mode from.
const EnvStorageMode = "STORAGE_MODE"

var allModes = []Mode{ModeKV, ModeDual, ModeDualPG, ModePostgres}

// ParseMode returns the mode a setting names. An unset or blank setting is ModeKV, the behavior the service has
// without it. Anything else must be one of the four values exactly: a value that is not recognized is an error and is
// never taken for ModeKV, so a typo cannot leave an operator believing another mode is on.
func ParseMode(raw string) (Mode, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ModeKV, nil
	}
	for _, mode := range allModes {
		if raw == string(mode) {
			return mode, nil
		}
	}
	return "", fmt.Errorf("invalid %s %q: use one of %v", EnvStorageMode, raw, allModes)
}

// UsesPostgres reports whether the mode reads or writes PostgreSQL at all.
func (m Mode) UsesPostgres() bool { return m != ModeKV }

// HasSecondary reports whether the mode writes both stores, the primary one deciding and the other following it.
func (m Mode) HasSecondary() bool { return m == ModeDual || m == ModeDualPG }

// PostgresIsPrimary reports whether PostgreSQL is the authoritative store, which is when its writes decide the result.
func (m Mode) PostgresIsPrimary() bool { return m == ModeDualPG || m == ModePostgres }

// ModeFromEnv reads the mode from the environment and checks that the service can run in it. With the variable unset
// the result is ModeKV and nothing else is checked, so a deployment that does not set it is not affected. A mode that
// is set on purpose must be usable: it must be recognized, and a mode that uses PostgreSQL needs a connection string.
// The error names the variable and never the connection string, which holds a password.
func ModeFromEnv(getenv func(string) string) (Mode, error) {
	mode, err := ParseMode(getenv(EnvStorageMode))
	if err != nil {
		return "", err
	}
	if !mode.UsesPostgres() {
		// In kv nothing else is read, not even the PostgreSQL settings, so no connection string is built.
		return mode, nil
	}
	if _, ok := postgres.ConnectionURL(getenv); !ok {
		return "", fmt.Errorf("%s=%s needs PostgreSQL but POSTGRES_URL is not set (nor are PGHOST, PGUSER, PGPASSWORD and PGDATABASE)", EnvStorageMode, mode)
	}
	return mode, nil
}
