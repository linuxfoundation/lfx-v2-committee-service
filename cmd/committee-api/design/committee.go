// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"goa.design/goa/v3/dsl"
)

var _ = dsl.API("committee", func() {
	dsl.Title("Committee Management Service")
})

// JWTAuth is the DSL JWT security type for authentication.
var JWTAuth = dsl.JWTSecurity("jwt", func() {
	dsl.Description("Heimdall authorization")
})

// Service describes the committee service
var _ = dsl.Service("committee-service", func() {
	dsl.Description("Committee management service")

	// Base committee endpoints
	// used by public users, readers, and writers.
	dsl.Method("create-committee", func() {
		dsl.Description("Create Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()

			CommitteeBaseAttributes()

			CommitteeSettingsAttributes()

			WritersAttribute()
			AuditorsAttribute()

			dsl.Required("name", "category", "project_uid")
		})

		dsl.Result(CommitteeFullWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees")
			dsl.Param("version:v")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)

		})
	})

	dsl.Method("get-committee-base", func() {
		dsl.Description("Get Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
		})

		dsl.Result(func() {
			dsl.Attribute("committee-base", CommitteeBaseWithReadonlyAttributes)
			ETagAttribute()
			dsl.Required("committee-base")
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("committee-base")
				dsl.Header("etag:ETag")
			})
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("update-committee-base", func() {
		dsl.Description("Update Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()

			CommitteeUIDAttribute()
			CommitteeBaseAttributes()

			dsl.Required("name", "category", "project_uid")
		})

		dsl.Result(CommitteeBaseWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.PUT("/committees/{uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("delete-committee", func() {
		dsl.Description("Delete Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()
			CommitteeUIDAttribute()
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Committee Settings endpoints
	// used by writers and auditors.
	dsl.Method("get-committee-settings", func() {
		dsl.Description("Get Committee Settings")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
		})

		dsl.Result(func() {
			dsl.Attribute("committee-settings", CommitteeSettingsWithReadonlyAttributes)
			ETagAttribute()
			dsl.Required("committee-settings")
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/settings")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("committee-settings")
				dsl.Header("etag:ETag")
			})
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("update-committee-settings", func() {
		dsl.Description("Update Committee Settings")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()

			CommitteeUIDAttribute()
			CommitteeSettingsAttributes()

			WritersAttribute()
			AuditorsAttribute()

			dsl.Required("business_email_required")
		})

		dsl.Result(CommitteeSettingsWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.PUT("/committees/{uid}/settings")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Health check endpoints
	dsl.Method("readyz", func() {
		dsl.Description("Check if the service is able to take inbound requests.")
		dsl.Meta("swagger:generate", "false")
		dsl.Result(dsl.Bytes, func() {
			dsl.Example("OK")
		})

		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/readyz")
			dsl.Response(dsl.StatusOK, func() {
				dsl.ContentType("text/plain")
			})
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("livez", func() {
		dsl.Description("Check if the service is alive.")
		dsl.Meta("swagger:generate", "false")
		dsl.Result(dsl.Bytes, func() {
			dsl.Example("OK")
		})
		dsl.HTTP(func() {
			dsl.GET("/livez")
			dsl.Response(dsl.StatusOK, func() {
				dsl.ContentType("text/plain")
			})
		})
	})

	// Committee members Endpoints
	// POST - Create committee member (requires essential fields)
	dsl.Method("create-committee-member", func() {
		dsl.Description("Add a new member to a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()

			CommitteeMemberCreateAttributes()

			dsl.Required("version", "uid", "email")
		})

		dsl.Result(CommitteeMemberFullWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Committee not found")
		dsl.Error("Conflict", ConflictError, "Member already exists")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/members")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// GET - Get single committee member
	dsl.Method("get-committee-member", func() {
		dsl.Description("Get a specific committee member by UID")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			MemberUIDAttribute()

			dsl.Required("version", "uid", "member_uid")
		})

		dsl.Result(func() {
			dsl.Attribute("member", CommitteeMemberFullWithReadonlyAttributes)
			ETagAttribute()
			dsl.Required("member")
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Member not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/members/{member_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("member_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("member")
				dsl.Header("etag:ETag")
			})
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// PUT - Replace committee member (complete resource replacement)
	// This endpoint follows PUT semantics: it replaces the entire member resource.
	// All required fields must be provided, even if unchanged.
	dsl.Method("update-committee-member", func() {
		dsl.Description("Replace an existing committee member (requires complete resource)")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()
			CommitteeUIDAttribute()
			MemberUIDAttribute()

			CommitteeMemberUpdateAttributes()

			dsl.Required("version", "uid", "member_uid", "email")
		})

		dsl.Result(CommitteeMemberFullWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Member not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.PUT("/committees/{uid}/members/{member_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("member_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// DELETE - Remove committee member
	dsl.Method("delete-committee-member", func() {
		dsl.Description("Remove a member from a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()
			CommitteeUIDAttribute()
			MemberUIDAttribute()

			dsl.Required("version", "uid", "member_uid")
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Member not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}/members/{member_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("member_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Serve the file gen/http/openapi3.json for requests sent to /openapi.json.
	dsl.Files("/openapi.json", "gen/http/openapi.json", func() {
		dsl.Meta("swagger:generate", "false")
	})
	dsl.Files("/openapi.yaml", "gen/http/openapi.yaml", func() {
		dsl.Meta("swagger:generate", "false")
	})
	dsl.Files("/openapi3.json", "gen/http/openapi3.json", func() {
		dsl.Meta("swagger:generate", "false")
	})
	dsl.Files("/openapi3.yaml", "gen/http/openapi3.yaml", func() {
		dsl.Meta("swagger:generate", "false")
	})
})
