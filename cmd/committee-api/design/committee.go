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

			dsl.Required("name", "category")
		})

		dsl.Result(CommitteeBaseWithReadonlyAttributes)

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
			ETagAttribute()

			CommitteeUIDAttribute()
			CommitteeBaseAttributes()

			dsl.Required("name", "category")
		})

		dsl.Result(CommitteeBaseWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.PUT("/committees/{uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("etag:ETag")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
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
			ETagAttribute()
			CommitteeUIDAttribute()
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("etag:ETag")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
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
			ETagAttribute()

			CommitteeUIDAttribute()
			CommitteeSettingsAttributes()

			WritersAttribute()
			AuditorsAttribute()

			dsl.Required("business_email_required")
		})

		dsl.Result(CommitteeSettingsWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.PUT("/committees/{uid}/settings")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("etag:ETag")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Health check endpoints
	dsl.Method("readyz", func() {
		dsl.Description("Check if the service is able to take inbound requests.")
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

	// Serve the file gen/http/openapi3.json for requests sent to /openapi.json.
	dsl.Files("/openapi.json", "gen/http/openapi3.json")
})
