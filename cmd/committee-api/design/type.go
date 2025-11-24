// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"goa.design/goa/v3/dsl"
)

// CommitteeBase is the DSL type for a committee base.
var CommitteeBase = dsl.Type("committee-base", func() {
	dsl.Description("A base representation of LFX committees without sub-objects.")

	CommitteeBaseAttributes()

})

// CommitteeBaseAttributes is the DSL attributes for a committee base.
func CommitteeBaseAttributes() {
	ProjectUIDAttribute()
	NameAttribute()
	CategoryAttribute()
	DescriptionAttribute()
	WebsiteAttribute()
	EnableVotingAttribute()
	SSOGroupEnabledAttribute()
	RequiresReviewAttribute()
	PublicAttribute()
	CalendarAttribute()
	DisplayNameAttribute()
	ParentCommitteeUIDAttribute()
}

// CommitteeSettings is the DSL type for a committee settings.
var CommitteeSettings = dsl.Type("committee-settings", func() {
	dsl.Description("A representation of LF Committee settings.")

	CommitteeSettingsAttributes()
})

// CommitteeSettingsAttributes is the DSL attributes for a committee settings.
func CommitteeSettingsAttributes() {
	BusinessEmailRequiredAttribute()
	LastReviewedAtAttribute()
	LastReviewedByAttribute()
}

// CommitteeFull is the DSL type for a committee full.
var CommitteeFull = dsl.Type("committee-full", func() {
	dsl.Description("A full representation of LFX committees with sub-objects.")

	CommitteeBaseAttributes()

	CommitteeSettingsAttributes()

	WritersAttribute()
	AuditorsAttribute()
})

var CommitteeBaseWithReadonlyAttributes = dsl.Type("committee-base-with-readonly-attributes", func() {
	dsl.Description("A base representation of LFX committees with readonly attributes.")

	CommitteeUIDAttribute()

	CommitteeBaseAttributes()

	ProjectNameAttribute()
	SSOGroupNameAttribute()

	TotalMembersAttribute()
	TotalVotingReposAttribute()

})

var CommitteeFullWithReadonlyAttributes = dsl.Type("committee-full-with-readonly-attributes", func() {
	dsl.Description("A complete representation of LFX committees with base, settings and readonly attributes.")

	CommitteeUIDAttribute()

	CommitteeBaseAttributes()

	SSOGroupNameAttribute()

	TotalMembersAttribute()
	TotalVotingReposAttribute()

	// Include settings attributes for complete representation
	CommitteeSettingsAttributes()

	WritersAttribute()
	AuditorsAttribute()

})

var CommitteeSettingsWithReadonlyAttributes = dsl.Type("committee-settings-with-readonly-attributes", func() {
	dsl.Description("A representation of LF Committee settings with readonly attributes.")

	CommitteeUIDAttribute()

	CommitteeSettingsAttributes()

	CreatedAtAttribute()
	UpdatedAtAttribute()

})

// CommitteeUIDAttribute is the DSL attribute for committee UID.
func CommitteeUIDAttribute() {
	dsl.Attribute("uid", dsl.String, "Committee UID -- v2 uid, not related to v1 id directly", func() {
		// Read-only attribute
		dsl.Example("7cad5a8d-19d0-41a4-81a6-043453daf9ee")
		dsl.Format(dsl.FormatUUID)
	})
}

// ProjectUIDAttribute is the DSL attribute for project UID.
func ProjectUIDAttribute() {
	dsl.Attribute("project_uid", dsl.String, "Project UID this committee belongs to -- v2 uid, not related to v1 id directly", func() {
		// Read-only attribute
		dsl.Example("7cad5a8d-19d0-41a4-81a6-043453daf9ee")
		dsl.Format(dsl.FormatUUID)
	})
}

// ProjectNameAttribute is the DSL attribute for project name.
func ProjectNameAttribute() {
	dsl.Attribute("project_name", dsl.String, "The name of the project this committee belongs to", func() {
		dsl.MaxLength(100)
		dsl.Example("Linux Foundation Project")
	})
}

// NameAttribute is the DSL attribute for committee name.
func NameAttribute() {
	dsl.Attribute("name", dsl.String, "The name of the committee", func() {
		dsl.MaxLength(100)
		dsl.Example("Technical Steering Committee")
	})
}

// CategoryAttribute is the DSL attribute for committee category.
func CategoryAttribute() {
	dsl.Attribute("category", dsl.String, "The category of the committee", func() {
		dsl.Enum(
			"Ambassador",
			"Board",
			"Code of Conduct",
			"Committers",
			"Expert Group",
			"Finance Committee",
			"Government Advisory Council",
			"Legal Committee",
			"Maintainers",
			"Marketing Committee/Sub Committee",
			"Marketing Mailing List",
			"Marketing Oversight Committee/Marketing Advisory Committee",
			"Other",
			"Product Security",
			"Special Interest Group",
			"Technical Mailing List",
			"Technical Oversight Committee/Technical Advisory Committee",
			"Technical Steering Committee",
			"Working Group",
		)
		dsl.Example("Technical Steering Committee")
	})
}

// DescriptionAttribute is the DSL attribute for committee description.
func DescriptionAttribute() {
	dsl.Attribute("description", dsl.String, "The description of the committee", func() {
		dsl.MaxLength(2000)
		dsl.Example("Main technical oversight committee for the project")
	})
}

// WebsiteAttribute is the DSL attribute for committee website.
func WebsiteAttribute() {
	dsl.Attribute("website", dsl.String, "The website URL of the committee", func() {
		dsl.Format(dsl.FormatURI)
		dsl.Pattern(`^(https?://)?[^\s/$.?#].[^\s]*$`)
		dsl.Example("https://committee.example.org")
	})
}

// EnableVotingAttribute is the DSL attribute for enabling voting.
func EnableVotingAttribute() {
	dsl.Attribute("enable_voting", dsl.Boolean, "Whether voting is enabled for this committee", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// BusinessEmailRequiredAttribute is the DSL attribute for business email requirement.
func BusinessEmailRequiredAttribute() {
	dsl.Attribute("business_email_required", dsl.Boolean, "Whether business email is required for committee members", func() {
		dsl.Default(false)
		dsl.Example(false)
	})
}

// SSOGroupEnabledAttribute is the DSL attribute for SSO group enablement.
func SSOGroupEnabledAttribute() {
	dsl.Attribute("sso_group_enabled", dsl.Boolean, "Whether SSO group integration is enabled", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// SSOGroupNameAttribute is the DSL attribute for SSO group name.
func SSOGroupNameAttribute() {
	dsl.Attribute("sso_group_name", dsl.String, "The name of the SSO group - read-only", func() {
		dsl.Example("lfx-committee-group")
	})
}

// RequiresReviewAttribute is the DSL attribute for committee review requirement.
func RequiresReviewAttribute() {
	dsl.Attribute("requires_review", dsl.Boolean, "Whether this committee is expected to be reviewed", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// PublicAttribute is the DSL attribute for public visibility.
func PublicAttribute() {
	dsl.Attribute("public", dsl.Boolean, "General committee visibility/access permissions", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// CalendarAttribute is the DSL attribute for calendar settings.
func CalendarAttribute() {
	dsl.Attribute("calendar", func() {
		dsl.Description("Settings related to the committee calendar")
		CalendarPublicAttribute()
	})
}

// CalendarPublicAttribute is the DSL attribute for calendar public visibility.
func CalendarPublicAttribute() {
	dsl.Attribute("public", dsl.Boolean, "Whether the committee calendar is publicly visible", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// LastReviewedAtAttribute is the DSL attribute for last review timestamp.
func LastReviewedAtAttribute() {
	dsl.Attribute("last_reviewed_at", dsl.String, "The timestamp when the committee was last reviewed in RFC3339 format", func() {
		dsl.Format(dsl.FormatDateTime)
		dsl.Example("2025-08-04T09:00:00Z")
	})
}

// LastReviewedByAttribute is the DSL attribute for last review user.
func LastReviewedByAttribute() {
	dsl.Attribute("last_reviewed_by", dsl.String, "The user ID who last reviewed this committee", func() {
		dsl.Example("user_id_12345")
	})
}

// DisplayNameAttribute is the DSL attribute for display name.
func DisplayNameAttribute() {
	dsl.Attribute("display_name", dsl.String, "The display name of the committee", func() {
		dsl.MaxLength(100)
		dsl.Example("TSC Committee Calendar")
	})
}

// ParentCommitteeUIDAttribute is the DSL attribute for parent committee UID.
func ParentCommitteeUIDAttribute() {
	dsl.Attribute("parent_uid", dsl.String, "The UID of the parent committee -- v2 uid, not related to v1 id directly, should be empty if there is none", func() {
		dsl.Format(dsl.FormatUUID)
		dsl.Example("90b147f2-7cdd-157a-a2f4-9d4a567123fc")
	})
}

// TotalMembersAttribute is the DSL attribute for total members count.
func TotalMembersAttribute() {
	dsl.Attribute("total_members", dsl.Int, "The total number of members in this committee", func() {
		dsl.Minimum(0)
		dsl.Example(15)
	})
}

// TotalVotingReposAttribute is the DSL attribute for total voting repositories count.
func TotalVotingReposAttribute() {
	dsl.Attribute("total_voting_repos", dsl.Int, "The total number of repositories with voting permissions for this committee", func() {
		dsl.Minimum(0)
		dsl.Example(3)
	})
}

// WritersAttribute is the DSL attribute for committee writers.
func WritersAttribute() {
	dsl.Attribute("writers", dsl.ArrayOf(dsl.String), "Manager user IDs who can edit/modify this committee", func() {
		dsl.Example([]string{"manager_user_id1", "manager_user_id2"})
	})
}

// VersionAttribute is the DSL attribute for API version.
func VersionAttribute() {
	dsl.Attribute("version", dsl.String, "Version of the API", func() {
		dsl.Example("1")
		dsl.Enum("1")
	})
}

// ETagAttribute is the DSL attribute for ETag header.
func ETagAttribute() {
	dsl.Attribute("etag", dsl.String, "ETag header value", func() {
		dsl.Example("123")
	})
}

// IfMatchAttribute is the DSL attribute for If-Match header (for conditional requests).
func IfMatchAttribute() {
	dsl.Attribute("if_match", dsl.String, "If-Match header value for conditional requests", func() {
		dsl.Example("123")
	})
}

// BearerTokenAttribute is the DSL attribute for bearer token.
func BearerTokenAttribute() {
	dsl.Token("bearer_token", dsl.String, func() {
		dsl.Description("JWT token issued by Heimdall")
		dsl.Example("eyJhbGci...")
	})
}

// XSyncAttribute is the DSL attribute for X-Sync header (for synchronous/asynchronous operations).
func XSyncAttribute() {
	dsl.Attribute("x_sync", dsl.Boolean, "Determines if the operation should be synchronous (true) or asynchronous (false, default)", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// CreatedAtAttribute is the DSL attribute for creation timestamp.
func CreatedAtAttribute() {
	dsl.Attribute("created_at", dsl.String, "The timestamp when the resource was created (read-only)", func() {
		dsl.Format(dsl.FormatDateTime)
		dsl.Example("2023-01-15T10:30:00Z")
	})
}

// UpdatedAtAttribute is the DSL attribute for update timestamp.
func UpdatedAtAttribute() {
	dsl.Attribute("updated_at", dsl.String, "The timestamp when the resource was last updated (read-only)", func() {
		dsl.Format(dsl.FormatDateTime)
		dsl.Example("2023-06-20T14:45:30Z")
	})
}

// LastAuditedByAttribute is the DSL attribute for last audited by user.
func LastAuditedByAttribute() {
	dsl.Attribute("last_audited_by", dsl.String, "The user ID who last audited the committee", func() {
		dsl.Example("user_id_12345")
	})
}

// LastAuditedTimeAttribute is the DSL attribute for last audit timestamp.
func LastAuditedTimeAttribute() {
	dsl.Attribute("last_audited_time", dsl.String, "The timestamp when the committee was last audited", func() {
		dsl.Format(dsl.FormatDateTime)
		dsl.Example("2023-05-10T09:15:00Z")
	})
}

// AuditorsAttribute is the DSL attribute for committee auditors.
func AuditorsAttribute() {
	dsl.Attribute("auditors", dsl.ArrayOf(dsl.String), "Auditor user IDs who can audit this committee", func() {
		dsl.Example([]string{"auditor_user_id1", "auditor_user_id2"})
	})
}

// Committee Member Types and Attributes

// CommitteeMemberBase is the DSL type for a committee member base.
var CommitteeMemberBase = dsl.Type("committee-member-base", func() {
	dsl.Description("A base representation of committee members.")

	CommitteeMemberBaseAttributes()
})

// CommitteeMemberBaseAttributes defines the base attributes for a committee member.
func CommitteeMemberBaseAttributes() {
	UsernameAttribute()
	EmailAttribute()
	FirstNameAttribute()
	LastNameAttribute()
	JobTitleAttribute()
	RoleInfoAttributes()
	AppointedByAttribute()
	StatusAttribute()
	VotingInfoAttributes()
	AgencyAttribute()
	CountryAttribute()
	OrganizationInfoAttributes()
}

// CommitteeMemberFull is the DSL type for a complete committee member.
var CommitteeMemberFull = dsl.Type("committee-member-full", func() {
	dsl.Description("A complete representation of committee members with all attributes.")

	CommitteeMemberBaseAttributes()
})

// CommitteeMemberFullWithReadonlyAttributes is the DSL type for a complete committee member with readonly attributes.
var CommitteeMemberFullWithReadonlyAttributes = dsl.Type("committee-member-full-with-readonly-attributes", func() {
	dsl.Description("A complete representation of committee members with readonly attributes.")

	CommitteeMemberUIDAttribute()
	CommitteeUIDMemberAttribute()
	CommitteeNameMemberAttribute()
	CommitteeCategoryMemberAttribute()
	CommitteeMemberBaseAttributes()
	CreatedAtAttribute()
	UpdatedAtAttribute()
})

// CommitteeMemberCreateAttributes defines attributes for creating a committee member.
func CommitteeMemberCreateAttributes() {
	CommitteeMemberBaseAttributes()
}

// CommitteeMemberUpdateAttributes defines attributes for updating a committee member.
func CommitteeMemberUpdateAttributes() {
	CommitteeMemberBaseAttributes()
}

// Organization Information Attributes
func OrganizationInfoAttributes() {
	dsl.Attribute("organization", func() {
		dsl.Description("Organization information for the committee member")
		OrganizationIDAttribute()
		OrganizationNameAttribute()
		OrganizationWebsiteAttribute()
	})
}

// Role Information Attributes
func RoleInfoAttributes() {
	dsl.Attribute("role", func() {
		dsl.Description("Committee role information")
		RoleNameAttribute()
		RoleStartDateAttribute()
		RoleEndDateAttribute()
	})
}

// Voting Information Attributes
func VotingInfoAttributes() {
	dsl.Attribute("voting", func() {
		dsl.Description("Voting information for the committee member")
		VotingStatusAttribute()
		VotingStartDateAttribute()
		VotingEndDateAttribute()
	})
}

// Committee Member Specific Attributes

// CommitteeMemberUIDAttribute is the DSL attribute for committee member UID.
func CommitteeMemberUIDAttribute() {
	dsl.Attribute("uid", dsl.String, "Committee member UID -- v2 uid, not related to v1 id directly", func() {
		dsl.Example("2200b646-fbb2-4de7-ad80-fd195a874baf")
		dsl.Format(dsl.FormatUUID)
	})
}

// CommitteeUIDAttribute is the DSL attribute for committee UID.
func CommitteeUIDMemberAttribute() {
	dsl.Attribute("committee_uid", dsl.String, "Committee UID -- v2 uid, not related to v1 id directly", func() {
		dsl.Example("7cad5a8d-19d0-41a4-81a6-043453daf9ee")
		dsl.Format(dsl.FormatUUID)
	})
}

// CommitteeNameMemberAttribute is the DSL attribute for committee name in member context.
func CommitteeNameMemberAttribute() {
	dsl.Attribute("committee_name", dsl.String, "The name of the committee this member belongs to", func() {
		dsl.MaxLength(100)
		dsl.Example("Technical Steering Committee")
	})
}

// CommitteeCategoryMemberAttribute is the DSL attribute for committee category in member context.
func CommitteeCategoryMemberAttribute() {
	dsl.Attribute("committee_category", dsl.String, "The category of the committee this member belongs to", func() {
		dsl.MaxLength(100)
		dsl.Example("Board")
	})
}

// MemberUIDAttribute is the DSL attribute for member UID in URL paths.
func MemberUIDAttribute() {
	dsl.Attribute("member_uid", dsl.String, "Committee member UID -- v2 uid, not related to v1 id directly", func() {
		dsl.Example("2200b646-fbb2-4de7-ad80-fd195a874baf")
		dsl.Format(dsl.FormatUUID)
	})
}

// UsernameAttribute is the DSL attribute for username.
func UsernameAttribute() {
	dsl.Attribute("username", dsl.String, "User's LF ID", func() {
		dsl.MaxLength(100)
		dsl.Example("user123")
	})
}

// EmailAttribute is the DSL attribute for email.
func EmailAttribute() {
	dsl.Attribute("email", dsl.String, "Primary email address", func() {
		dsl.Format(dsl.FormatEmail)
		dsl.Example("user@example.com")
	})
}

// FirstNameAttribute is the DSL attribute for first name.
func FirstNameAttribute() {
	dsl.Attribute("first_name", dsl.String, "First name", func() {
		dsl.MaxLength(100)
		dsl.Example("John")
	})
}

// LastNameAttribute is the DSL attribute for last name.
func LastNameAttribute() {
	dsl.Attribute("last_name", dsl.String, "Last name", func() {
		dsl.MaxLength(100)
		dsl.Example("Doe")
	})
}

// JobTitleAttribute is the DSL attribute for job title.
func JobTitleAttribute() {
	dsl.Attribute("job_title", dsl.String, "Job title at organization", func() {
		dsl.MaxLength(200)
		dsl.Example("Chief Technology Officer")
	})
}

// RoleNameAttribute is the DSL attribute for committee role name.
func RoleNameAttribute() {
	dsl.Attribute("name", dsl.String, "Committee role name", func() {
		dsl.Enum(
			"Chair",
			"Counsel",
			"Developer Seat",
			"TAC/TOC Representative",
			"Director",
			"Lead",
			"None",
			"Secretary",
			"Treasurer",
			"Vice Chair",
			"LF Staff",
		)
		dsl.Default("None")
		dsl.Example("Chair")
	})
}

// RoleStartDateAttribute is the DSL attribute for role start date.
func RoleStartDateAttribute() {
	dsl.Attribute("start_date", dsl.String, "Role start date", func() {
		dsl.Format(dsl.FormatDate)
		dsl.Example("2023-01-01")
	})
}

// RoleEndDateAttribute is the DSL attribute for role end date.
func RoleEndDateAttribute() {
	dsl.Attribute("end_date", dsl.String, "Role end date", func() {
		dsl.Format(dsl.FormatDate)
		dsl.Example("2024-12-31")
	})
}

// AppointedByAttribute is the DSL attribute for appointed by.
func AppointedByAttribute() {
	dsl.Attribute("appointed_by", dsl.String, "How the member was appointed", func() {
		dsl.Enum(
			"Community",
			"Membership Entitlement",
			"Vote of End User Member Class",
			"Vote of TSC Committee",
			"Vote of TAC Committee",
			"Vote of Academic Member Class",
			"Vote of Lab Member Class",
			"Vote of Marketing Committee",
			"Vote of Governing Board",
			"Vote of General Member Class",
			"Vote of End User Committee",
			"Vote of TOC Committee",
			"Vote of Gold Member Class",
			"Vote of Silver Member Class",
			"Vote of Strategic Membership Class",
			"None",
		)
		dsl.Default("None")
		dsl.Example("Community")
	})
}

// StatusAttribute is the DSL attribute for member status.
func StatusAttribute() {
	dsl.Attribute("status", dsl.String, "Member status", func() {
		dsl.Enum("Active", "Inactive")
		dsl.Default("Active")
		dsl.Example("Active")
	})
}

// VotingStatusAttribute is the DSL attribute for voting status.
func VotingStatusAttribute() {
	dsl.Attribute("status", dsl.String, "Voting status", func() {
		dsl.Enum(
			"Alternate Voting Rep",
			"Observer",
			"Voting Rep",
			"Emeritus",
			"None",
		)
		dsl.Default("None")
		dsl.Example("Voting Rep")
	})
}

// VotingStartDateAttribute is the DSL attribute for voting start date.
func VotingStartDateAttribute() {
	dsl.Attribute("start_date", dsl.String, "Voting start date", func() {
		dsl.Format(dsl.FormatDate)
		dsl.Example("2023-01-01")
	})
}

// VotingEndDateAttribute is the DSL attribute for voting end date.
func VotingEndDateAttribute() {
	dsl.Attribute("end_date", dsl.String, "Voting end date", func() {
		dsl.Format(dsl.FormatDate)
		dsl.Example("2024-12-31")
	})
}

// AgencyAttribute is the DSL attribute for government agency.
func AgencyAttribute() {
	dsl.Attribute("agency", dsl.String, "Government agency (for GAC members)", func() {
		dsl.MaxLength(100)
		dsl.Example("GSA")
	})
}

// CountryAttribute is the DSL attribute for country.
func CountryAttribute() {
	dsl.Attribute("country", dsl.String, "Country (for GAC members)", func() {
		dsl.MaxLength(100)
		dsl.Example("United States")
	})
}

// Organization Specific Attributes

// OrganizationNameAttribute is the DSL attribute for organization name.
func OrganizationNameAttribute() {
	dsl.Attribute("name", dsl.String, "Organization name", func() {
		dsl.MaxLength(200)
		dsl.Example("The Linux Foundation")
	})
}

// OrganizationWebsiteAttribute is the DSL attribute for organization website.
func OrganizationWebsiteAttribute() {
	dsl.Attribute("website", dsl.String, "Organization website URL", func() {
		dsl.Format(dsl.FormatURI)
		dsl.Example("https://linuxfoundation.org")
	})
}

// OrganizationIDAttribute is the DSL attribute for organization ID.
func OrganizationIDAttribute() {
	dsl.Attribute("id", dsl.String, "Organization ID", func() {
		dsl.Example("org-123456")
	})
}

// Errors
// BadRequestError is the DSL type for a bad request error.
var BadRequestError = dsl.Type("bad-request-error", func() {
	dsl.Attribute("message", dsl.String, "Error message", func() {
		dsl.Example("The request was invalid.")
	})
	dsl.Required("message")
})

// NotFoundError is the DSL type for a not found error.
var NotFoundError = dsl.Type("not-found-error", func() {
	dsl.Attribute("message", dsl.String, "Error message", func() {
		dsl.Example("The resource was not found.")
	})
	dsl.Required("message")
})

// ConflictError is the DSL type for a conflict error.
var ConflictError = dsl.Type("conflict-error", func() {
	dsl.Attribute("message", dsl.String, "Error message", func() {
		dsl.Example("The resource already exists.")
	})
	dsl.Required("message")
})

// InternalServerError is the DSL type for an internal server error.
var InternalServerError = dsl.Type("internal-server-error", func() {
	dsl.Attribute("message", dsl.String, "Error message", func() {
		dsl.Example("An internal server error occurred.")
	})
	dsl.Required("message")
})

// ServiceUnavailableError is the DSL type for a service unavailable error.
var ServiceUnavailableError = dsl.Type("service-unavailable-error", func() {
	dsl.Attribute("message", dsl.String, "Error message", func() {
		dsl.Example("The service is unavailable.")
	})
	dsl.Required("message")
})
