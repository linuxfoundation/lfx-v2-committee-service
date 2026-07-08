// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package orgid

import "testing"

func TestIsCDPUUID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"001B000000IqhSLIAZ", false},
		{"001B000000IqhSLIAZ", false},
		{"0014100000Te2ovAAB", false},
		{"51fde723-67df-4e0e-91c6-936d01d59559", true},
		{"4340abc06f4e11f1944c4bb16c3aa46c", true},
		{"111", false},
	}
	for _, tt := range tests {
		if got := IsCDPUUID(tt.in); got != tt.want {
			t.Fatalf("IsCDPUUID(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
