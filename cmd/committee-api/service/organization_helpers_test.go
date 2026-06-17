// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func TestAcceptInviteOrganization(t *testing.T) {
	invite := &model.CommitteeInvite{
		Organization: &model.CommitteeMemberOrganization{
			Name:    "Invite Org",
			Website: "https://invite.org",
		},
	}

	t.Run("uses full payload organization when ID present", func(t *testing.T) {
		orgID := "org-123456"
		orgName := "Payload Org"
		orgWebsite := "https://payload.org"
		org := acceptInviteOrganization(invite, &orgID, &orgName, &orgWebsite)
		assert.Equal(t, "org-123456", org.ID)
		assert.Equal(t, "Payload Org", org.Name)
		assert.Equal(t, "https://payload.org", org.Website)
	})

	t.Run("ignores partial payload without ID", func(t *testing.T) {
		orgName := "Payload Org"
		org := acceptInviteOrganization(invite, nil, &orgName, nil)
		assert.Equal(t, "Invite Org", org.Name)
		assert.Equal(t, "https://invite.org", org.Website)
		assert.Empty(t, org.ID)
	})

	t.Run("falls back to invite when payload has no organization", func(t *testing.T) {
		org := acceptInviteOrganization(invite, nil, nil, nil)
		assert.Equal(t, "Invite Org", org.Name)
		assert.Equal(t, "https://invite.org", org.Website)
	})
}
