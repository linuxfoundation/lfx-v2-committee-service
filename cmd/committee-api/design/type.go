// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"goa.design/goa/v3/dsl"
)

// Shared attributes used across multiple entity types.

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

// Error types

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
