// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// The variables that tune a mode which writes both stores. They are read only for dual and dual-pg, and are not looked at
// in kv or postgres mode, so a deployment that does not use a secondary store is never affected by them.
const (
	// EnvReadCompareRate is the fraction of reads that are also made against the secondary store and compared, from 0 to 1.
	EnvReadCompareRate = "STORAGE_READ_COMPARE_RATE"
	// EnvCompareTimeout bounds the extra read of a compared request, as a Go duration such as 2s.
	EnvCompareTimeout = "STORAGE_COMPARE_TIMEOUT"
	// EnvSecondaryWriteTimeout bounds the copy of a write to the secondary store, as a Go duration such as 3s. The copy is
	// made on the request's path, so it is the most a degraded secondary store adds to a write.
	EnvSecondaryWriteTimeout = "STORAGE_SECONDARY_WRITE_TIMEOUT"
)

// OptionsFromEnv returns the options the environment sets for the mode. A variable that is unset keeps the default, and one
// that is set on purpose and cannot be used is an error, so that a typo does not leave an operator believing a fraction of
// reads is compared when it is not.
func OptionsFromEnv(mode Mode, getenv func(string) string) ([]Option, error) {
	if !mode.HasSecondary() {
		return nil, nil
	}
	var opts []Option
	if raw := strings.TrimSpace(getenv(EnvReadCompareRate)); raw != "" {
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(rate) || rate < 0 || rate > 1 {
			return nil, fmt.Errorf("invalid %s %q: use a number from 0 to 1", EnvReadCompareRate, raw)
		}
		opts = append(opts, WithSampleRate(rate))
	}
	compare, err := durationFromEnv(getenv, EnvCompareTimeout)
	if err != nil {
		return nil, err
	}
	write, err := durationFromEnv(getenv, EnvSecondaryWriteTimeout)
	if err != nil {
		return nil, err
	}
	if compare > 0 || write > 0 {
		opts = append(opts, WithTimeouts(compare, write))
	}
	return opts, nil
}

// durationFromEnv reads a positive duration, or zero when the variable is unset.
func durationFromEnv(getenv func(string) string, name string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: use a duration greater than zero, such as 3s", name, raw)
	}
	return d, nil
}
