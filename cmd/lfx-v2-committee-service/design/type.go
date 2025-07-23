// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"goa.design/goa/v3/dsl"
)

// Committee defines the complete committee data structure
var Committee = dsl.Type("committee", func() {

	dsl.Description("A representation of LFX committee")

	IDAttribute()
	ProjectIDAttribute()
	NameAttribute()
	CategoryAttribute()
	DescriptionAttribute()
	WebsiteAttribute()
	EnableVotingAttribute()
	BusinessEmailRequiredAttribute()
	SSOGroupEnabledAttribute()
	IsAuditEnabledAttribute()
	PublicAttribute()
	CalendarAttribute()
	PublicNameAttribute()
	ParentCommitteeIDAttribute()
	WritersAttribute()
})

// IDAttribute is the DSL attribute for committee ID.
func IDAttribute() {
	dsl.Attribute("id", dsl.String, "The unique identifier of the committee", func() {
		dsl.Format(dsl.FormatUUID)
		dsl.Example("52ec9e74-e8d3-40d9-953c-bc2d2c6ae516")
	})
}

// ProjectIDAttribute is the DSL attribute for project ID.
func ProjectIDAttribute() {
	dsl.Attribute("project_id", dsl.String, "The project identifier this committee belongs to -- v2 id, not related to v1 id directly", func() {
		dsl.Example("7cad5a8d-19d0-41a4-81a6-043453daf9ee")
		dsl.Format(dsl.FormatUUID)
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

// IsAuditEnabledAttribute is the DSL attribute for audit enablement.
func IsAuditEnabledAttribute() {
	dsl.Attribute("is_audit_enabled", dsl.Boolean, "Whether audit logging is enabled for this committee", func() {
		dsl.Default(false)
		dsl.Example(false)
	})
}

// PublicAttribute is the DSL attribute for public visibility.
func PublicAttribute() {
	dsl.Attribute("public", dsl.Boolean, "General committee visibility/access permissions", func() {
		dsl.Default(false)
		dsl.Example(true)
	})
}

// CalendarAttribute is the DSL attribute for calendar public visibility.
func CalendarAttribute() {
	dsl.Attribute("calendar", func() {
		dsl.Description("Settings related to the committee calendar")
		dsl.Attribute("public", dsl.Boolean, "Whether the committee calendar is publicly visible", func() {
			dsl.Default(false)
			dsl.Example(true)
		})
	})
}

// PublicNameAttribute is the DSL attribute for public name.
func PublicNameAttribute() {
	dsl.Attribute("public_name", dsl.String, "The public display name of the committee", func() {
		dsl.MaxLength(100)
		dsl.Example("TSC Committee Calendar")
	})
}

// ParentCommitteeIDAttribute is the DSL attribute for parent committee ID.
func ParentCommitteeIDAttribute() {
	dsl.Attribute("parent_committee_id", dsl.String, "The ID of the parent committee, should be empty if there is none", func() {
		dsl.Format(dsl.FormatUUID)
		dsl.Example("90b147f2-7cdd-157a-a2f4-9d4a567123fc")
	})
}

// WritersAttribute is the DSL attribute for committee writers.
func WritersAttribute() {
	dsl.Attribute("writers", dsl.ArrayOf(dsl.String), "Manager user IDs who can edit/modify this committee", func() {
		dsl.Example([]string{"manager_user_id1", "manager_user_id2"})
	})
}

// CommitteeIDAttribute is the DSL attribute for committee ID parameter.
func CommitteeIDAttribute() {
	dsl.Attribute("id", dsl.String, "The unique identifier of the committee", func() {
		dsl.Format(dsl.FormatUUID)
		dsl.Description("Committee ID")
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
