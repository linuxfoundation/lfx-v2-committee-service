// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"regexp"
	"testing"
)

func TestURLPattern(t *testing.T) {
	re := regexp.MustCompile(urlPattern)

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"https URL", "https://github.com/example/repo", true},
		{"http URL", "http://committee.example.org", true},
		{"javascript scheme rejected", "javascript:alert(1)", false},
		{"data scheme rejected", "data:text/html,<script>alert(1)</script>", false},
		{"file scheme rejected", "file:///etc/passwd", false},
		{"schemeless domain rejected", "committee.example.org", false},
		{"empty string rejected", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := re.MatchString(tt.value); got != tt.want {
				t.Errorf("urlPattern.MatchString(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// TestCharterURLPattern covers charterURLPattern, which -- unlike urlPattern -- must accept
// the empty string, since "" is the explicit clear signal for a previously set charter.
func TestCharterURLPattern(t *testing.T) {
	re := regexp.MustCompile(charterURLPattern)

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"https URL", "https://example.org/governance/charter.pdf", true},
		{"http URL", "http://committee.example.org/charter", true},
		{"empty string accepted -- clear signal", "", true},
		{"javascript scheme rejected", "javascript:alert(1)", false},
		{"data scheme rejected", "data:text/html,<script>alert(1)</script>", false},
		{"file scheme rejected", "file:///etc/passwd", false},
		{"schemeless domain rejected", "committee.example.org", false},
		{"whitespace-only rejected", "   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := re.MatchString(tt.value); got != tt.want {
				t.Errorf("charterURLPattern.MatchString(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
