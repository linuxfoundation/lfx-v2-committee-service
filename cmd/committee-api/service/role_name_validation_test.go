// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	server "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoleNameValidation_TechnicalLead verifies that "Technical Lead" is a
// valid value for the committee member role name enum.
func TestRoleNameValidation_TechnicalLead(t *testing.T) {
	email := "member@example.com"
	roleName := "Technical Lead"

	body := &server.CreateCommitteeMemberRequestBody{
		Email: &email,
		Role: &struct {
			Name      *string `form:"name" json:"name" xml:"name"`
			StartDate *string `form:"start_date" json:"start_date" xml:"start_date"`
			EndDate   *string `form:"end_date" json:"end_date" xml:"end_date"`
		}{
			Name: &roleName,
		},
	}

	err := server.ValidateCreateCommitteeMemberRequestBody(body)
	require.NoError(t, err, "\"Technical Lead\" should be a valid role name")
}

// TestRoleNameValidation_AllRoles verifies every expected role name is accepted.
func TestRoleNameValidation_AllRoles(t *testing.T) {
	email := "member@example.com"

	validRoles := []string{
		"Chair",
		"Developer Seat",
		"TAC/TOC Representative",
		"Director",
		"Lead",
		"None",
		"Secretary",
		"Technical Lead",
		"Treasurer",
		"Vice Chair",
		"LF Staff",
	}

	for _, role := range validRoles {
		role := role
		t.Run(role, func(t *testing.T) {
			roleName := role
			body := &server.CreateCommitteeMemberRequestBody{
				Email: &email,
				Role: &struct {
					Name      *string `form:"name" json:"name" xml:"name"`
					StartDate *string `form:"start_date" json:"start_date" xml:"start_date"`
					EndDate   *string `form:"end_date" json:"end_date" xml:"end_date"`
				}{
					Name: &roleName,
				},
			}
			err := server.ValidateCreateCommitteeMemberRequestBody(body)
			assert.NoError(t, err, "role %q should be valid", role)
		})
	}
}

// TestRoleNameValidation_InvalidRole verifies that an unknown role name is rejected.
func TestRoleNameValidation_InvalidRole(t *testing.T) {
	email := "member@example.com"
	roleName := "Unknown Role"

	body := &server.CreateCommitteeMemberRequestBody{
		Email: &email,
		Role: &struct {
			Name      *string `form:"name" json:"name" xml:"name"`
			StartDate *string `form:"start_date" json:"start_date" xml:"start_date"`
			EndDate   *string `form:"end_date" json:"end_date" xml:"end_date"`
		}{
			Name: &roleName,
		},
	}

	err := server.ValidateCreateCommitteeMemberRequestBody(body)
	assert.Error(t, err, "\"Unknown Role\" should be rejected")
}
