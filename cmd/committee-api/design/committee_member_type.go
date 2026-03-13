// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"goa.design/goa/v3/dsl"
)

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
	LinkedInProfileAttribute()
	RoleInfoAttributes()
	AppointedByAttribute()
	StatusAttribute()
	VotingInfoAttributes()
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

// OrganizationInfoAttributes defines organization information attributes for a committee member.
func OrganizationInfoAttributes() {
	dsl.Attribute("organization", func() {
		dsl.Description("Organization information for the committee member")
		OrganizationIDAttribute()
		OrganizationNameAttribute()
		OrganizationWebsiteAttribute()
	})
}

// RoleInfoAttributes defines role information attributes for a committee member.
func RoleInfoAttributes() {
	dsl.Attribute("role", func() {
		dsl.Description("Committee role information")
		RoleNameAttribute()
		RoleStartDateAttribute()
		RoleEndDateAttribute()
	})
}

// VotingInfoAttributes defines voting information attributes for a committee member.
func VotingInfoAttributes() {
	dsl.Attribute("voting", func() {
		dsl.Description("Voting information for the committee member")
		VotingStatusAttribute()
		VotingStartDateAttribute()
		VotingEndDateAttribute()
	})
}

// CommitteeMemberUIDAttribute is the DSL attribute for committee member UID.
func CommitteeMemberUIDAttribute() {
	dsl.Attribute("uid", dsl.String, "Committee member UID -- v2 uid, not related to v1 id directly", func() {
		dsl.Example("2200b646-fbb2-4de7-ad80-fd195a874baf")
		dsl.Format(dsl.FormatUUID)
	})
}

// CommitteeUIDMemberAttribute is the DSL attribute for committee UID in member context.
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

// LinkedInProfileAttribute is the DSL attribute for LinkedIn profile URL.
func LinkedInProfileAttribute() {
	dsl.Attribute("linkedin_profile", dsl.String, "LinkedIn profile URL", func() {
		dsl.Format(dsl.FormatURI)
		dsl.Pattern(`^(https?://)?([a-z]{2,3}\.)?linkedin\.com/.*$`)
		dsl.Example("https://www.linkedin.com/in/johndoe")
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
