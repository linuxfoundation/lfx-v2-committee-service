// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLFXSelfServeBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		environment string
		want        string
	}{
		{
			name:    "explicit base URL takes precedence",
			baseURL: "https://custom.example.com",
			want:    "https://custom.example.com",
		},
		{
			name:        "prod environment",
			environment: "prod",
			want:        "https://app.lfx.dev",
		},
		{
			name:        "production alias",
			environment: "production",
			want:        "https://app.lfx.dev",
		},
		{
			name:        "staging environment",
			environment: "staging",
			want:        "https://app.staging.lfx.dev",
		},
		{
			name:        "stg alias",
			environment: "stg",
			want:        "https://app.staging.lfx.dev",
		},
		{
			name:        "stage alias",
			environment: "stage",
			want:        "https://app.staging.lfx.dev",
		},
		{
			name:        "dev environment",
			environment: "dev",
			want:        "https://app.dev.lfx.dev",
		},
		{
			name:        "development alias",
			environment: "development",
			want:        "https://app.dev.lfx.dev",
		},
		{
			name: "unset environment defaults to prod",
			want: "https://app.lfx.dev",
		},
		{
			name:        "unrecognized environment defaults to prod",
			environment: "qa",
			want:        "https://app.lfx.dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LFX_SELF_SERVE_BASE_URL", tt.baseURL)
			t.Setenv("LFX_ENVIRONMENT", tt.environment)

			assert.Equal(t, tt.want, LFXSelfServeBaseURL())
		})
	}
}
