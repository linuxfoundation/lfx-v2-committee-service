// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

// appendPath joins two URL path components with exactly one slash separating
// them. Shared by all query-service adapters in this package.
func appendPath(base, extra string) string {
	if base == "" {
		return extra
	}
	if base[len(base)-1] == '/' && len(extra) > 0 && extra[0] == '/' {
		return base + extra[1:]
	}
	if base[len(base)-1] != '/' && (len(extra) == 0 || extra[0] != '/') {
		return base + "/" + extra
	}
	return base + extra
}
