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
		{name: "email is hashed", username: "accept@example.com", want: MapUsernameToAuthSub("accept@example.com")},
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
