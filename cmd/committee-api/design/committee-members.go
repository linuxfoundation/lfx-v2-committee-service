// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"goa.design/goa/v3/dsl"
)

// Service describes the committee members service
var _ = dsl.Service("committee-members-service", func() {
	dsl.Description("Committee members management service")

	// POST - Create committee member
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

	// PUT - Update committee member
	dsl.Method("update-committee-member", func() {
		dsl.Description("Update an existing committee member")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()
			CommitteeUIDAttribute()
			MemberUIDAttribute()

			CommitteeMemberUpdateAttributes()

			dsl.Required("version", "uid", "member_uid")
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
})
