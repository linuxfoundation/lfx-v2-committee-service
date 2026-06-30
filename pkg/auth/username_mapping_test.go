// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapUsernameToAuthSub(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     string
	}{
		{name: "empty", username: "", want: ""},
		{name: "safe username", username: "joiner", want: "auth0|joiner"},
		{name: "email is hashed", username: "accept@example.com", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapUsernameToAuthSub(tt.username)
			if tt.name == "email is hashed" {
				assert.NotEqual(t, "auth0|accept@example.com", got)
				assert.True(t, len(got) > len("auth0|"))
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestAuthSubLookupKey(t *testing.T) {
	tests := []struct {
		name      string
		principal string
		want      string
	}{
		{name: "empty", principal: "", want: ""},
		{name: "blank trimmed to empty", principal: "   ", want: ""},
		{name: "bare LFID is mapped to sub", principal: "alice", want: "auth0|alice"},
		{name: "already qualified auth0 sub passes through", principal: "auth0|alice", want: "auth0|alice"},
		{name: "other provider sub passes through", principal: "oidc|google|123", want: "oidc|google|123"},
		{name: "surrounding whitespace trimmed before mapping", principal: " bob ", want: "auth0|bob"},
		{name: "legacy username routes through the hash branch", principal: "accept@example.com", want: MapUsernameToAuthSub("accept@example.com")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, AuthSubLookupKey(tt.principal))
		})
	}
}
