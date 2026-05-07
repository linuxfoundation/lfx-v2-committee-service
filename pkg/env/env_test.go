// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package env

import (
	"testing"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string // empty string means "unset"
		defaultValue string
		expected     string
	}{
		{
			name:         "returns env value when set",
			key:          "TEST_ENV_KEY",
			envValue:     "from-env",
			defaultValue: "default",
			expected:     "from-env",
		},
		{
			name:         "returns default when var is unset",
			key:          "TEST_ENV_KEY_UNSET",
			envValue:     "",
			defaultValue: "default",
			expected:     "default",
		},
		{
			name:         "returns default when var is empty string",
			key:          "TEST_ENV_KEY_EMPTY",
			envValue:     "",
			defaultValue: "fallback",
			expected:     "fallback",
		},
		{
			name:         "returns empty default when both unset and default are empty",
			key:          "TEST_ENV_KEY_BOTH_EMPTY",
			envValue:     "",
			defaultValue: "",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}
			result := Get(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("Get(%q, %q) = %q, want %q", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}
