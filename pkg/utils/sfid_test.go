// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package utils

import "testing"

func TestIsSFIDShaped(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"15-char alphanumeric", "0017000000abcde", true},
		{"18-char alphanumeric", "001QP00001BMxFmYAL", true},
		{"lf-prefix 15-char (post b2b/b2c split)", "lf00000001Te0OK", true},
		{"lf-prefix 18-char (post b2b/b2c split)", "lf00000001Te0OKAAZ", true},
		{"CDP UUID (hyphenated)", "abc12345-6789-abcd-ef01-234567890abc", false},
		{"32-char hex digest", "abc123de45fg6789abcd1234ef567890", false},
		{"too short", "001ABC", false},
		{"too long", "001QP00001BMxFmYALx", false},
		{"15-char with hyphen", "001-00000abcde0", false},
		{"whitespace-padded 18-char", "  001QP00001BMxFmYAL  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSFIDShaped(tt.in); got != tt.want {
				t.Fatalf("IsSFIDShaped(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeAccountSFID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"trims whitespace", "  001B000000IqhSLIAZ  ", "001B000000IqhSLIAZ"},
		{"18-char passthrough", "001QP00001BMxFmYAL", "001QP00001BMxFmYAL"},
		// All-lowercase/digit 15-char id → every 5-char chunk has no uppercase bits set, so each
		// checksum char is 'A' (alphabet index 0): suffix "AAA".
		{"15-char all-lowercase upgraded to 18", "0017000000abcde", "0017000000abcdeAAA"},
		{"non-sfid length left as-is", "lfEQOaDy1TUPBMS02U", "lfEQOaDy1TUPBMS02U"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAccountSFID(tt.in); got != tt.want {
				t.Fatalf("NormalizeAccountSFID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A 15-char id and its 18-char canonical must normalize to the same value, so the index and the
// route uid match regardless of which form was stored.
func TestNormalizeAccountSFID_15and18Converge(t *testing.T) {
	fifteen := "0017000000abcde"
	eighteen := NormalizeAccountSFID(fifteen)
	if NormalizeAccountSFID(eighteen) != eighteen {
		t.Fatalf("18-char form is not stable under normalization: %q", eighteen)
	}
	if len(eighteen) != 18 {
		t.Fatalf("expected 18-char output, got %d (%q)", len(eighteen), eighteen)
	}
}
