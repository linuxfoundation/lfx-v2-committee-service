// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func sampleInviteOrganizationPayload() *struct {
	ID      *string
	Name    *string
	Website *string
} {
	name := "The Linux Foundation"
	website := "https://linuxfoundation.org"
	return &struct {
		ID      *string
		Name    *string
		Website *string
	}{
		Name:    &name,
		Website: &website,
	}
}

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

func TestCreateInvite_OrganizationOptionalOnOrgGatedCommittee(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()

	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "no-org@example.com",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.Organization)
}

func TestCreateInvite_OrganizationStored(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()

	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-1",
		InviteeEmail: "with-org@example.com",
		Organization: sampleInviteOrganizationPayload(),
	})
	require.NoError(t, err)
	require.NotNil(t, result.Organization)
	require.NotNil(t, result.Organization.Name)
	assert.Equal(t, "The Linux Foundation", *result.Organization.Name)
	require.NotNil(t, result.Organization.Website)
	assert.Equal(t, "https://linuxfoundation.org", *result.Organization.Website)
}

func TestCreateInvite_OrganizationOptionalWhenNotRequired(t *testing.T) {
	svc, _, _ := setupServiceTestWithRepo()

	result, err := svc.CreateInvite(context.Background(), &committeeservice.CreateInvitePayload{
		UID:          "committee-2",
		InviteeEmail: "open@example.com",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.Organization)
}

func TestAcceptInvite_OrganizationMerge(t *testing.T) {
	svc, mockOrch, repo := setupServiceTestWithRepo()
	svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

	invite := &model.CommitteeInvite{
		UID:          "invite-org-merge",
		CommitteeUID: "committee-1",
		InviteeEmail: "accept@example.com",
		Status:       "pending",
		Organization: model.CommitteeMemberOrganization{
			Name:    "Invite Org",
			Website: "https://invite.org",
		},
		CreatedAt: time.Now(),
	}
	repo.AddCommitteeInvite(invite)

	mockOrch.createMember = &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-1",
			CommitteeUID: "committee-1",
			Email:        "accept@example.com",
			Status:       "Active",
		},
	}

	overrideName := "Payload Org"
	_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
		UID:       "committee-1",
		InviteUID: "invite-org-merge",
		Organization: &struct {
			ID      *string
			Name    *string
			Website *string
		}{
			Name: &overrideName,
		},
	})
	require.NoError(t, err)
	require.Len(t, mockOrch.createMemberCalls, 1)
	member := mockOrch.createMemberCalls[0]
	assert.Equal(t, "Payload Org", member.Organization.Name)
	assert.Equal(t, "https://invite.org", member.Organization.Website)
}

func TestAcceptInvite_OrganizationFromInviteWhenPayloadUnset(t *testing.T) {
	svc, mockOrch, repo := setupServiceTestWithRepo()
	svc.userReader = mockReaderForPrincipalEmail("accept@example.com", "accept@example.com")

	invite := &model.CommitteeInvite{
		UID:          "invite-org-fallback",
		CommitteeUID: "committee-1",
		InviteeEmail: "accept@example.com",
		Status:       "pending",
		Organization: model.CommitteeMemberOrganization{
			Name:    "Invite Org",
			Website: "https://invite.org",
		},
		CreatedAt: time.Now(),
	}
	repo.AddCommitteeInvite(invite)

	mockOrch.createMember = &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "member-1",
			CommitteeUID: "committee-1",
			Email:        "accept@example.com",
			Status:       "Active",
		},
	}

	_, err := svc.AcceptInvite(testCtx("accept@example.com"), &committeeservice.AcceptInvitePayload{
		UID:       "committee-1",
		InviteUID: "invite-org-fallback",
	})
	require.NoError(t, err)
	require.Len(t, mockOrch.createMemberCalls, 1)
	member := mockOrch.createMemberCalls[0]
	assert.Equal(t, "Invite Org", member.Organization.Name)
	assert.Equal(t, "https://invite.org", member.Organization.Website)
}
