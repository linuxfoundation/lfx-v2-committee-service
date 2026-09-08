// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommitteeInvite_MarshalJSON_OmitsZeroExpiryAndNilInviter guards the wire shape the
// indexer publishes (it marshals the domain object directly). A zero ExpiresAt must be
// omitted rather than serialized as "0001-01-01T00:00:00Z" (omitempty does not omit a zero
// time.Time — omitzero does), and a nil Inviter must be omitted entirely.
func TestCommitteeInvite_MarshalJSON_OmitsZeroExpiryAndNilInviter(t *testing.T) {
	t.Run("legacy invite: zero expiry and nil inviter are absent", func(t *testing.T) {
		invite := &model.CommitteeInvite{
			UID:          "invite-1",
			CommitteeUID: "committee-1",
			InviteeEmail: "invitee@example.com",
			Status:       "pending",
			CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		}

		raw, err := json.Marshal(invite)
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))

		_, hasExpiry := decoded["expires_at"]
		assert.False(t, hasExpiry, "zero ExpiresAt must be omitted, got: %s", raw)
		_, hasInviter := decoded["inviter"]
		assert.False(t, hasInviter, "nil Inviter must be omitted, got: %s", raw)
	})

	t.Run("populated invite: expiry and inviter are present", func(t *testing.T) {
		invite := &model.CommitteeInvite{
			UID:          "invite-2",
			CommitteeUID: "committee-1",
			InviteeEmail: "invitee@example.com",
			Status:       "pending",
			CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ExpiresAt:    time.Date(2026, 2, 1, 3, 4, 5, 0, time.UTC),
			Inviter:      &model.CommitteeUser{Name: "First Last", Username: "first-last"},
		}

		raw, err := json.Marshal(invite)
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))

		assert.Equal(t, "2026-02-01T03:04:05Z", decoded["expires_at"])
		require.Contains(t, decoded, "inviter")
		inviterMap, ok := decoded["inviter"].(map[string]any)
		require.True(t, ok, "inviter must be a JSON object, got: %T", decoded["inviter"])
		assert.Equal(t, "First Last", inviterMap["name"], "inviter.name mismatch")
		assert.Equal(t, "first-last", inviterMap["username"], "inviter.username mismatch")
	})
}
