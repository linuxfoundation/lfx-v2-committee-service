// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package utils

import "testing"

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
