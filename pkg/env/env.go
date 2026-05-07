// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package env

import "os"

// Get returns the value of the environment variable named by key.
// If the variable is unset or empty, defaultValue is returned.
func Get(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
