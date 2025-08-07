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
var _ = dsl.Service("committee", func() {
	dsl.Description("Committee management service")

	dsl.Method("create-committee", func() {
		dsl.Description("Create Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			dsl.Token("bearer_token", dsl.String, func() {
				dsl.Description("JWT token issued by Heimdall")
				dsl.Example("eyJhbGci...")
			})
			VersionAttribute()

			NameAttribute()
			CategoryAttribute()
			DescriptionAttribute()
			WebsiteAttribute()
			EnableVotingAttribute()
			BusinessEmailRequiredAttribute()
			SSOGroupEnabledAttribute()
			SSOGroupNameAttribute()
			IsAuditEnabledAttribute()
			PublicAttribute()
			PublicNameAttribute()
			ParentCommitteeIDAttribute()
			StatusAttribute()
			WritersAttribute()

			dsl.Required("name", "category")
		})

		dsl.Result(Committee)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees")
			dsl.Param("version:v")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)

		})
	})

	dsl.Method("get-committee", func() {
		dsl.Description("Get Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			dsl.Token("bearer_token", dsl.String, func() {
				dsl.Description("JWT token issued by Heimdall")
				dsl.Example("eyJhbGci...")
			})
			VersionAttribute()
			CommitteeIDAttribute()
		})

		dsl.Result(func() {
			dsl.Attribute("committee", Committee)
			dsl.Attribute("etag", dsl.String, "ETag header value")
			dsl.Required("committee")
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{id}")
			dsl.Param("version:v")
			dsl.Param("id")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("committee")
				dsl.Header("etag:ETag")
			})
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("update-committee", func() {
		dsl.Description("Update Committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			dsl.Token("bearer_token", dsl.String, func() {
				dsl.Description("JWT token issued by Heimdall")
				dsl.Example("eyJhbGci...")
			})
			VersionAttribute()
			ETagAttribute()

			CommitteeIDAttribute()
			ProjectIDAttribute()
			NameAttribute()
			CategoryAttribute()
			DescriptionAttribute()
			WebsiteAttribute()
			EnableVotingAttribute()
			BusinessEmailRequiredAttribute()
			SSOGroupEnabledAttribute()
			SSOGroupNameAttribute()
			IsAuditEnabledAttribute()
			PublicAttribute()
			PublicNameAttribute()
			ParentCommitteeIDAttribute()
			StatusAttribute()
			WritersAttribute()

			dsl.Required("name", "category")
		})

		dsl.Result(Committee)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.PUT("/committees/{id}")
			dsl.Param("version:v")
			dsl.Param("id")
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
			dsl.Token("bearer_token", dsl.String, func() {
				dsl.Description("JWT token issued by Heimdall")
				dsl.Example("eyJhbGci...")
			})
			VersionAttribute()
			ETagAttribute()
			CommitteeIDAttribute()
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{id}")
			dsl.Param("version:v")
			dsl.Param("id")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("etag:ETag")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

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
