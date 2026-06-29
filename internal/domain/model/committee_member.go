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

// votingStatusNone is the legacy sentinel value a member may have when a
// non-voting committee was converted to voting without migrating member statuses.
const votingStatusNone = "None"

// CommitteeMember represents the complete committee member business entity
type CommitteeMember struct {
	CommitteeMemberBase
	// SkipNotification is request-scoped intent to suppress the invite/notification
	// email when the member is created. It is tagged json:"-" so it is never
	// persisted to the member KV record nor included in the default event marshal;
	// it is carried into the create event via CommitteeMemberCreatedEventData.
	SkipNotification bool `json:"-"`
}

// CommitteeMemberBase represents the base committee member attributes
type CommitteeMemberBase struct {
	UID               string                      `json:"uid"`
	Username          string                      `json:"username"`
	Avatar            string                      `json:"avatar,omitempty"`
	Email             string                      `json:"email"`
	FirstName         string                      `json:"first_name"`
	LastName          string                      `json:"last_name"`
	JobTitle          string                      `json:"job_title,omitempty"`
	LinkedInProfile   string                      `json:"linkedin_profile,omitempty"`
	Role              CommitteeMemberRole         `json:"role"`
	AppointedBy       string                      `json:"appointed_by"`
	Status            string                      `json:"status"`
	Voting            CommitteeMemberVotingInfo   `json:"voting"`
	Organization      CommitteeMemberOrganization `json:"organization"`
	CommitteeUID      string                      `json:"committee_uid"`
	CommitteeName     string                      `json:"committee_name"`
	CommitteeCategory string                      `json:"committee_category"`
	ProjectUID        string                      `json:"project_uid,omitempty"`
	ProjectSlug       string                      `json:"project_slug,omitempty"`
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

// BuildEmailIndexKey returns the SHA-256 hex digest of the member's normalized email, used as the
// dot-free email segment of the lookup/committee-members-by-email/<hash>.<member_uid> secondary
// index. Returns "" when the member has no email (callers treat that as a no-op).
func (cm *CommitteeMember) BuildEmailIndexKey(ctx context.Context) string {
	email := strings.TrimSpace(strings.ToLower(cm.Email))
	if email == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(email))
	key := hex.EncodeToString(hash[:])
	slog.DebugContext(ctx, "member email index key built",
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

	if cm.ProjectUID != "" {
		tag := fmt.Sprintf("project_uid:%s", cm.ProjectUID)
		tags = append(tags, tag)
	}

	if cm.ProjectSlug != "" {
		tag := fmt.Sprintf("project_slug:%s", cm.ProjectSlug)
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

// NeedsSyncWith reports whether this member's denormalized committee fields
// differ from the current state of the given committee. Used by the sync handler
// to skip members that are already up to date, making re-runs idempotent.
func (cm *CommitteeMember) NeedsSyncWith(committee *CommitteeBase) bool {
	if cm == nil || committee == nil {
		return false
	}
	return cm.CommitteeName != committee.Name ||
		cm.CommitteeCategory != committee.Category ||
		cm.ProjectUID != committee.ProjectUID ||
		cm.ProjectSlug != committee.ProjectSlug
}

// isNoneVotingStatus reports whether s represents the "None" voting status,
// using a case-insensitive comparison to guard against legacy data casing variations.
func isNoneVotingStatus(s string) bool {
	return strings.EqualFold(s, votingStatusNone)
}

// Validate validates the committee member against the committee's requirements.
// It covers all standard cases including the restriction that voting-enabled
// committees may not have members with voting_status "None".
func (cm *CommitteeMember) Validate(committee *Committee) error {
	return cm.validate(committee, "")
}

// ValidateUpdate runs all of the same checks as Validate and additionally
// applies the legacy-None exemption: if the existing member already carries
// voting_status "None" (a v1 migration artifact), the incoming update may keep
// or correct it without being rejected.
func (cm *CommitteeMember) ValidateUpdate(committee *Committee, existing *CommitteeMember) error {
	existingStatus := ""
	if existing != nil {
		existingStatus = existing.Voting.Status
	}
	return cm.validate(committee, existingStatus)
}

// validate is the shared implementation for Validate and ValidateUpdate.
// existingStatus is the current voting status of the member being updated
// (empty string for creates).
func (cm *CommitteeMember) validate(committee *Committee, existingStatus string) error {
	if cm == nil {
		return errs.NewValidation("committee member cannot be nil")
	}

	if committee == nil {
		return errs.NewValidation("committee cannot be nil")
	}

	if err := cm.validateRequiredFields(); err != nil {
		return err
	}

	if err := cm.validateOrganizationFields(committee); err != nil {
		return err
	}

	if err := cm.validateVotingStatus(committee, existingStatus); err != nil {
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

// validateVotingStatus rejects "None" as an incoming voting status on voting-enabled committees,
// unless the existing member already carries "None" (the legacy corrective path).
// Pass an empty string for existingStatus on creates.
func (cm *CommitteeMember) validateVotingStatus(committee *Committee, existingStatus string) error {
	if committee.EnableVoting && isNoneVotingStatus(cm.Voting.Status) && !isNoneVotingStatus(existingStatus) {
		return errs.NewValidation(`voting_status "None" is not allowed on voting-enabled committees`)
	}
	return nil
}

// ValidateOrganizationForCommittee checks whether organization info satisfies committee requirements.
// When business_email_required or voting is enabled, either organization ID or both name and website
// must be present. Used for invite creation as well as member create/update validation.
func ValidateOrganizationForCommittee(org CommitteeMemberOrganization, committee *Committee) error {
	return (&CommitteeMember{CommitteeMemberBase: CommitteeMemberBase{Organization: org}}).validateOrganizationFields(committee)
}

// validateOrganizationFields validates that organization information is provided when required.
// When business_email_required or voting is enabled on the committee, the member must supply
// either an organization ID or both an organization name and domain (website).
func (cm *CommitteeMember) validateOrganizationFields(committee *Committee) error {
	businessEmailRequired := committee.CommitteeSettings != nil && committee.BusinessEmailRequired
	votingEnabled := committee.EnableVoting

	if !businessEmailRequired && !votingEnabled {
		return nil
	}

	hasOrgID := cm.Organization.ID != ""
	hasOrgNameAndDomain := cm.Organization.Name != "" && cm.Organization.Website != ""

	if !hasOrgID && !hasOrgNameAndDomain {
		return errs.NewValidation("organization id or organization name and domain are required when business email is required or voting is enabled")
	}

	return nil
}
