// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func TestMergeInviteOrganization(t *testing.T) {
	base := model.CommitteeMemberOrganization{
		Name:    "Stored Org",
		Website: "https://stored.org",
	}
	override := model.CommitteeMemberOrganization{
		Name: "Accepted Org",
	}

	merged := mergeInviteOrganization(base, override)
	assert.Equal(t, "Accepted Org", merged.Name)
	assert.Equal(t, "https://stored.org", merged.Website)

	partial := mergeInviteOrganization(base, model.CommitteeMemberOrganization{
		Website: "https://override.org",
	})
	assert.Equal(t, "Stored Org", partial.Name)
	assert.Equal(t, "https://override.org", partial.Website)
}
