// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSanitizeKVKey verifies that sanitizeKVKey flattens '/', ':', and ' ' (':'
// and ' ' are forbidden by JetStream KV; '/' is permitted but flattened too so
// index keys stay single-token) while preserving the characters real UIDs are
// made of (alphanumerics, '-', '.').
func TestSanitizeKVKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "slash-and-dash-uid",
			in:   "committees/abc-def.ghi/weekly-briefs/2026-05-12",
		},
		{
			name: "colon-separated",
			in:   "committee:abc:def",
		},
		{
			name: "spaces-and-slashes-and-colons",
			in:   "committees/abc def:ghi/2026 05 12",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeKVKey(tc.in)
			require.False(t, strings.ContainsAny(got, "/: "),
				"sanitized key %q must not contain '/', ':', or space (input %q)", got, tc.in)
		})
	}
}

// TestSanitizeKVKey_PreservesSafeChars asserts that UID-style inputs containing
// only safe characters are unchanged.
func TestSanitizeKVKey_PreservesSafeChars(t *testing.T) {
	safe := []string{
		"abc-def.ghi-12345",
		"7cad5a8d-19d0-41a4-81a6-043453daf9ee",
		"abc123def456.20260512",
	}
	for _, s := range safe {
		assert.Equal(t, s, sanitizeKVKey(s), "expected safe input %q to pass through unchanged", s)
	}
}

// TestBuildBriefIndexKey verifies the {committee_uid}.{yyyymmdd} key shape and
// that it routes through sanitizeKVKey so future UID changes can't introduce
// forbidden characters.
func TestBuildBriefIndexKey(t *testing.T) {
	got := buildBriefIndexKey("abc:def/ghi", "20260510")
	require.False(t, strings.ContainsAny(got, "/: "))
	assert.Contains(t, got, "20260510")
}
