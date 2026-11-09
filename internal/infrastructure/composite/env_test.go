// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// appliedTo returns the core the options make for the mode.
func appliedTo(mode Mode, opts []Option) core { return newCore(mode, opts) }

func TestOptionsFromEnv_UnsetKeepsTheDefaults(t *testing.T) {
	for _, mode := range []Mode{ModeDual, ModeDualPG} {
		opts, err := OptionsFromEnv(mode, envOf(nil))

		require.NoError(t, err, mode)
		c := appliedTo(mode, opts)
		assert.Equal(t, defaultSampleRate, c.rate)
		assert.Equal(t, defaultCompareTimeout, c.compareTimeout)
		assert.Equal(t, defaultWriteTimeout, c.writeTimeout)
	}
}

func TestOptionsFromEnv_SetsWhatTheEnvironmentSays(t *testing.T) {
	opts, err := OptionsFromEnv(ModeDual, envOf(map[string]string{
		EnvReadCompareRate: " 0.25 ", EnvCompareTimeout: "500ms", EnvSecondaryWriteTimeout: "1s",
	}))

	require.NoError(t, err)
	c := appliedTo(ModeDual, opts)
	assert.Equal(t, 0.25, c.rate)
	assert.Equal(t, 500*time.Millisecond, c.compareTimeout)
	assert.Equal(t, time.Second, c.writeTimeout)
}

func TestOptionsFromEnv_ZeroAndOneAreValidRates(t *testing.T) {
	for raw, want := range map[string]float64{"0": 0, "1": 1} {
		opts, err := OptionsFromEnv(ModeDual, envOf(map[string]string{EnvReadCompareRate: raw}))

		require.NoError(t, err, raw)
		assert.Equal(t, want, appliedTo(ModeDual, opts).rate, raw)
	}
}

// A value set on purpose that cannot be used is an error, and is never replaced by the default.
func TestOptionsFromEnv_RefusesWhatCannotBeUsed(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"a rate that is not a number":              {EnvReadCompareRate: "lots"},
		"a rate below zero":                        {EnvReadCompareRate: "-0.1"},
		"a rate above one":                         {EnvReadCompareRate: "1.5"},
		"a compare timeout that is not a duration": {EnvCompareTimeout: "fast"},
		"a compare timeout of zero":                {EnvCompareTimeout: "0s"},
		"a negative write timeout":                 {EnvSecondaryWriteTimeout: "-1s"},
		"a write timeout without a unit":           {EnvSecondaryWriteTimeout: "3"},
	} {
		_, err := OptionsFromEnv(ModeDual, envOf(values))

		require.Error(t, err, name)
		for variable := range values {
			assert.ErrorContains(t, err, variable, name)
		}
	}
}

// The modes that write one store have no secondary store to tune, so a deployment that sets the variables, or sets them
// badly, is not affected.
func TestOptionsFromEnv_IsNotReadWhereThereIsNoSecondaryStore(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModePostgres} {
		opts, err := OptionsFromEnv(mode, envOf(map[string]string{EnvReadCompareRate: "lots", EnvCompareTimeout: "fast"}))

		require.NoError(t, err, mode)
		assert.Empty(t, opts, mode)
	}
}
