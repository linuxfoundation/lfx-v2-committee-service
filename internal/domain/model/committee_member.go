// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

// CommitteeMember represents the complete committee member business entity
type CommitteeMember struct {
	CommitteeMemberBase
}

// CommitteeMemberBase represents the base committee member attributes
type CommitteeMemberBase struct {
	UID               string                      `json:"uid"`
	Username          string                      `json:"username"`
	Email             string                      `json:"email"`
	FirstName         string                      `json:"first_name"`
	LastName          string                      `json:"last_name"`
	JobTitle          string                      `json:"job_title,omitempty"`
	LinkedInProfile   string                      `json:"linkedin_profile,omitempty"`
	Role              CommitteeMemberRole         `json:"role"`
	AppointedBy       string                      `json:"appointed_by"`
	Status            string                      `json:"status"`
	Voting            CommitteeMemberVotingInfo   `json:"voting"`
	Agency            string                      `json:"agency,omitempty"`
	Country           string                      `json:"country,omitempty"`
	Organization      CommitteeMemberOrganization `json:"organization"`
	CommitteeUID      string                      `json:"committee_uid"`
	CommitteeName     string                      `json:"committee_name"`
	CommitteeCategory string                      `json:"committee_category"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
}

// Role represents committee role information
type CommitteeMemberRole struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// VotingInfo represents voting information for the committee member
type CommitteeMemberVotingInfo struct {
	Status    string `json:"status"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// Organization represents organization information for the committee member
type CommitteeMemberOrganization struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Website string `json:"website,omitempty"`
}

// BuildIndexKey generates a SHA-256 hash for use as a NATS KV key.
// The hash is generated from the committee UID and the member's email (i.e., committee_uid + email).
// This enforces uniqueness for committee members within a committee.
// This is necessary because the original input may contain special characters,
// exceed length limits, or have inconsistent formatting, and we do not control its content.
// Using a hash ensures a safe, fixed-length, and deterministic key.
func (cm *CommitteeMember) BuildIndexKey(ctx context.Context) string {

	committee := strings.TrimSpace(strings.ToLower(cm.CommitteeUID))
	email := strings.TrimSpace(strings.ToLower(cm.Email))
	// Combine normalized values with a delimiter
	data := fmt.Sprintf("%s|%s", committee, email)

	hash := sha256.Sum256([]byte(data))

	key := hex.EncodeToString(hash[:])

	slog.DebugContext(ctx, "member index key built",
		"committee_uid", cm.CommitteeUID,
		"email", redaction.RedactEmail(cm.Email),
		"key", key,
	)

	return key
}

// Tags generates a consistent set of tags for the committee member.
// IMPORTANT: If you modify this method, please update the Committee Tags documentation in the README.md
// to ensure consumers understand how to use these tags for searching.
func (cm *CommitteeMember) Tags() []string {
	var tags []string

	if cm == nil {
		return nil
	}

	if cm.UID != "" {
		// without prefix
		tags = append(tags, cm.UID)
		// with prefix
		tag := fmt.Sprintf("committee_member_uid:%s", cm.UID)
		tags = append(tags, tag)
	}

	if cm.CommitteeUID != "" {
		tag := fmt.Sprintf("committee_uid:%s", cm.CommitteeUID)
		tags = append(tags, tag)
	}

	if cm.CommitteeCategory != "" {
		tag := fmt.Sprintf("committee_category:%s", cm.CommitteeCategory)
		tags = append(tags, tag)
	}

	if cm.Username != "" {
		tag := fmt.Sprintf("username:%s", cm.Username)
		tags = append(tags, tag)
	}

	if cm.Email != "" {
		tag := fmt.Sprintf("email:%s", cm.Email)
		tags = append(tags, tag)
	}

	if cm.Voting.Status != "" {
		tag := fmt.Sprintf("voting_status:%s", cm.Voting.Status)
		tags = append(tags, tag)
	}

	// Add organization information as tags
	if cm.Organization.ID != "" {
		tag := fmt.Sprintf("organization_id:%s", cm.Organization.ID)
		tags = append(tags, tag)
	}

	if cm.Organization.Name != "" {
		tag := fmt.Sprintf("organization_name:%s", cm.Organization.Name)
		tags = append(tags, tag)
	}

	if cm.Organization.Website != "" {
		tag := fmt.Sprintf("organization_website:%s", cm.Organization.Website)
		tags = append(tags, tag)
	}

	return tags
}

// Validate validates the committee member against the committee's requirements
func (cm *CommitteeMember) Validate(committee *Committee) error {
	if cm == nil {
		return errs.NewValidation("committee member cannot be nil")
	}

	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}

	// Validate basic required fields
	if err := cm.validateRequiredFields(); err != nil {
		return err
	}

	// Validate committee-specific requirements
	if err := cm.validateCategory(committee); err != nil {
		return err
	}

	return nil
}

// validateRequiredFields validates basic required fields for all committee members
func (cm *CommitteeMember) validateRequiredFields() error {
	if cm.Email == "" {
		return errs.NewValidation("email is required")
	}

	return nil
}

// validateCategory validates the committee member against the committee's category
func (cm *CommitteeMember) validateCategory(committee *Committee) error {
	// Government Advisory Council specific validation
	if committee.IsGovernmentAdvisoryCouncil() {
		missingFields := []string{}
		if cm.Agency == "" {
			missingFields = append(missingFields, "agency")
		}

		if cm.Country == "" {
			missingFields = append(missingFields, "country")
		}

		if len(missingFields) > 0 {
			return errs.NewValidation("missing required fields for Government Advisory Council members: " + strings.Join(missingFields, ", "))
		}

		return nil
	}

	if cm.Agency != "" || cm.Country != "" {
		return errs.NewValidation("agency and country should not be set for non-Government Advisory Council members")
	}

	return nil
}
