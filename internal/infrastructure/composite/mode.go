// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package composite holds what is needed to run the NATS KV and PostgreSQL stores side by side: the store mode
// the API and the CLI share and the metrics for the failures a secondary store must never turn into a failed
// request.
package composite

import (
	"fmt"
	"strings"
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

// envPostgresURL is the variable the PostgreSQL connection string is read from.
const envPostgresURL = "POSTGRES_URL"

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

// available reports whether the service can run in the mode yet. The other modes are accepted by ParseMode and
// refused here until the stores behind them exist, so that setting one cannot leave the service half switched over.
func (m Mode) available() bool { return m == ModeKV }

// ModeFromEnv reads the mode from the environment and checks that the service can run in it. With the variable unset
// the result is ModeKV and nothing else is checked, so a deployment that does not set it is not affected. A mode that
// is set on purpose must be usable: it must be recognized, a mode that uses PostgreSQL needs a connection string, and
// the mode must be available. The error names the variable and never the connection string, which holds a password.
func ModeFromEnv(getenv func(string) string) (Mode, error) {
	mode, err := ParseMode(getenv(EnvStorageMode))
	if err != nil {
		return "", err
	}
	if mode.UsesPostgres() && strings.TrimSpace(getenv(envPostgresURL)) == "" {
		return "", fmt.Errorf("%s=%s needs PostgreSQL but %s is not set", EnvStorageMode, mode, envPostgresURL)
	}
	if !mode.available() {
		return "", fmt.Errorf("%s=%s is not available yet: only %s is", EnvStorageMode, mode, ModeKV)
	}
	return mode, nil
}
