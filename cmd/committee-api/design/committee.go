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
			XSyncAttribute()

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
			dsl.Header("x_sync:X-Sync")
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
			XSyncAttribute()

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
			dsl.Header("x_sync:X-Sync")
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
			XSyncAttribute()
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
			dsl.Header("x_sync:X-Sync")
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
			XSyncAttribute()

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
			dsl.Header("x_sync:X-Sync")
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
			XSyncAttribute()
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
			dsl.Header("x_sync:X-Sync")
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
			XSyncAttribute()
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
			dsl.Header("x_sync:X-Sync")
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
			XSyncAttribute()
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
			dsl.Header("x_sync:X-Sync")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Committee invite endpoints
	dsl.Method("get-invite", func() {
		dsl.Description("Get a single invite by UID")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			InviteUIDAttribute()

			dsl.Required("version", "uid", "invite_uid")
		})

		dsl.Result(CommitteeInviteWithReadonlyAttributes)

		dsl.Error("NotFound", NotFoundError, "Invite not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/invites/{invite_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("invite_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("create-invite", func() {
		dsl.Description("Create an invite for a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			XSyncAttribute()
			CommitteeUIDAttribute()

			dsl.Attribute("invitee_email", dsl.String, "Email of the person to invite", func() {
				dsl.Format(dsl.FormatEmail)
				dsl.Example("invitee@example.com")
			})
			dsl.Attribute("role", dsl.String, "Suggested role for the invitee", func() {
				dsl.Example("None")
			})

			dsl.Required("version", "uid", "invitee_email")
		})

		dsl.Result(CommitteeInviteWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Committee not found")
		dsl.Error("Conflict", ConflictError, "Invite already exists")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/invites")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("x_sync:X-Sync")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("revoke-invite", func() {
		dsl.Description("Revoke a pending invite")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			InviteUIDAttribute()

			dsl.Required("version", "uid", "invite_uid")
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Invite not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}/invites/{invite_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("invite_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("accept-invite", func() {
		dsl.Description("Accept a pending invite")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			InviteUIDAttribute()

			dsl.Required("version", "uid", "invite_uid")
		})

		dsl.Result(CommitteeMemberFullWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Forbidden", ForbiddenError, "You are not the invitee for this invite")
		dsl.Error("NotFound", NotFoundError, "Invite not found")
		dsl.Error("Conflict", ConflictError, "Invite already processed")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/invites/{invite_uid}/accept")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("invite_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Forbidden", dsl.StatusForbidden)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("decline-invite", func() {
		dsl.Description("Decline a pending invite")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			InviteUIDAttribute()

			dsl.Required("version", "uid", "invite_uid")
		})

		dsl.Result(CommitteeInviteWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Forbidden", ForbiddenError, "You are not the invitee for this invite")
		dsl.Error("NotFound", NotFoundError, "Invite not found")
		dsl.Error("Conflict", ConflictError, "Invite already processed")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/invites/{invite_uid}/decline")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("invite_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Forbidden", dsl.StatusForbidden)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Committee application endpoints
	dsl.Method("get-application", func() {
		dsl.Description("Get a single application by UID")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			ApplicationUIDAttribute()

			dsl.Required("version", "uid", "application_uid")
		})

		dsl.Result(CommitteeApplicationWithReadonlyAttributes)

		dsl.Error("NotFound", NotFoundError, "Application not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/applications/{application_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("application_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("submit-application", func() {
		dsl.Description("Submit an application to join a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			XSyncAttribute()
			CommitteeUIDAttribute()

			dsl.Attribute("message", dsl.String, "Application message", func() {
				dsl.MaxLength(2000)
				dsl.Example("I would like to join the TSC to contribute my expertise.")
			})

			dsl.Required("version", "uid")
		})

		dsl.Result(CommitteeApplicationWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Forbidden", ForbiddenError, "Committee does not accept applications")
		dsl.Error("NotFound", NotFoundError, "Committee not found")
		dsl.Error("Conflict", ConflictError, "Application already exists")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/applications")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("x_sync:X-Sync")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Forbidden", dsl.StatusForbidden)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("approve-application", func() {
		dsl.Description("Approve a pending application")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			ApplicationUIDAttribute()

			dsl.Attribute("reviewer_notes", dsl.String, "Notes from the reviewer", func() {
				dsl.MaxLength(2000)
				dsl.Example("Approved based on contribution history.")
			})

			dsl.Required("version", "uid", "application_uid")
		})

		dsl.Result(CommitteeMemberFullWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Application not found")
		dsl.Error("Conflict", ConflictError, "Application already processed")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/applications/{application_uid}/approve")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("application_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("reject-application", func() {
		dsl.Description("Reject a pending application")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			ApplicationUIDAttribute()

			dsl.Attribute("reviewer_notes", dsl.String, "Notes from the reviewer", func() {
				dsl.MaxLength(2000)
				dsl.Example("Does not meet current requirements.")
			})

			dsl.Required("version", "uid", "application_uid")
		})

		dsl.Result(CommitteeApplicationWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Application not found")
		dsl.Error("Conflict", ConflictError, "Application already processed")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/applications/{application_uid}/reject")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("application_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// Self-join and leave endpoints
	dsl.Method("join-committee", func() {
		dsl.Description("Self-join a committee (only works when join_mode is open)")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			XSyncAttribute()
			CommitteeUIDAttribute()

			dsl.Required("version", "uid")
		})

		dsl.Result(CommitteeMemberFullWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Forbidden", ForbiddenError, "Committee join_mode is not open")
		dsl.Error("NotFound", NotFoundError, "Committee not found")
		dsl.Error("Conflict", ConflictError, "Already a member")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/join")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("x_sync:X-Sync")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Forbidden", dsl.StatusForbidden)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("leave-committee", func() {
		dsl.Description("Leave a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			XSyncAttribute()
			CommitteeUIDAttribute()

			dsl.Required("version", "uid")
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Not a member of this committee")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}/leave")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("x_sync:X-Sync")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// ─── Committee Link endpoints ───

	dsl.Method("get-committee-link", func() {
		dsl.Description("Get a single link for a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			LinkUIDAttribute()
		})

		dsl.Result(func() {
			dsl.Attribute("committee-link", CommitteeLinkWithReadonlyAttributes)
			ETagAttribute()
			dsl.Required("committee-link")
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/links/{link_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("link_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("committee-link")
				dsl.Header("etag:ETag")
			})
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("list-committee-links", func() {
		dsl.Description("List links for a committee, optionally filtered by folder")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			dsl.Attribute("folder_uid", dsl.String, "Filter links to those inside a specific folder; omit to return all links", func() {
				dsl.Format(dsl.FormatUUID)
			})
		})

		dsl.Result(dsl.ArrayOf(CommitteeLinkWithReadonlyAttributes))

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/links")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("folder_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("create-committee-link", func() {
		dsl.Description("Add a URL link to a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()

			dsl.Attribute("name", dsl.String, "Display name for the link", func() {
				dsl.MaxLength(500)
				dsl.Example("Technical Architecture Decision Records")
			})
			dsl.Attribute("url", dsl.String, "The URL this link points to", func() {
				dsl.MaxLength(2048)
				dsl.Example("https://confluence.example.com/architecture-decisions")
			})
			dsl.Attribute("description", dsl.String, "Optional description", func() {
				dsl.MaxLength(2000)
			})
			dsl.Attribute("folder_uid", dsl.String, "Optional folder UID to place this link in", func() {
				dsl.Format(dsl.FormatUUID)
			})
			dsl.Attribute("created_by_name", dsl.String, "Display name of the creator (client-provided from user session)", func() {
				dsl.MaxLength(200)
				dsl.Example("Alex Lee")
			})

			dsl.Required("name", "url")
		})

		dsl.Result(CommitteeLinkWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/links")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("delete-committee-link", func() {
		dsl.Description("Delete a link from a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()
			CommitteeUIDAttribute()
			LinkUIDAttribute()
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}/links/{link_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("link_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// ─── Committee Folder endpoints ───

	dsl.Method("get-committee-link-folder", func() {
		dsl.Description("Get a single folder for a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			FolderUIDAttribute()
		})

		dsl.Result(func() {
			dsl.Attribute("committee-link-folder", CommitteeLinkFolderWithReadonlyAttributes)
			ETagAttribute()
			dsl.Required("committee-link-folder")
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/folders/{folder_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("folder_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("committee-link-folder")
				dsl.Header("etag:ETag")
			})
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("list-committee-link-folders", func() {
		dsl.Description("List all folders for a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
		})

		dsl.Result(dsl.ArrayOf(CommitteeLinkFolderWithReadonlyAttributes))

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/folders")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("create-committee-link-folder", func() {
		dsl.Description("Create a folder to organize committee links")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			dsl.Attribute("name", dsl.String, "Folder name", func() {
				dsl.MaxLength(200)
				dsl.Example("Meeting Notes")
			})
			dsl.Attribute("created_by_name", dsl.String, "Display name of the creator (client-provided from user session)", func() {
				dsl.MaxLength(200)
				dsl.Example("Alex Lee")
			})
			dsl.Required("name")
		})

		dsl.Result(CommitteeLinkFolderWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Conflict", ConflictError, "Folder name already exists")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/folders")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("delete-committee-link-folder", func() {
		dsl.Description("Delete a folder from a committee. Returns BadRequest if the folder contains links.")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			IfMatchAttribute()
			CommitteeUIDAttribute()
			FolderUIDAttribute()
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}/folders/{folder_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("folder_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Header("if_match:If-Match")
			dsl.Response(dsl.StatusNoContent)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	// ─── Committee Document Endpoints ───

	dsl.Method("upload-committee-document", func() {
		dsl.Description("Upload a file document to a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()

			dsl.Attribute("name", dsl.String, "Display name for the document", func() {
				dsl.MaxLength(500)
				dsl.Example("Architecture Decision Record")
			})
			dsl.Attribute("description", dsl.String, "Optional description", func() {
				dsl.MaxLength(2000)
			})
			dsl.Attribute("uploaded_by_name", dsl.String, "Display name of the uploader (client-provided from user session)", func() {
				dsl.MaxLength(200)
				dsl.Example("Alex Lee")
			})
			// File fields populated by the multipart decoder
			dsl.Attribute("file_name", dsl.String, "Original file name (from the uploaded file part)")
			dsl.Attribute("content_type", dsl.String, "MIME type of the uploaded file")
			dsl.Attribute("file", dsl.Bytes, "File content")

			dsl.Required("name", "uid")
		})

		dsl.Result(CommitteeDocumentWithReadonlyAttributes)

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("Conflict", ConflictError, "Document name already exists")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.POST("/committees/{uid}/documents")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.MultipartRequest()
			dsl.Response(dsl.StatusCreated)
			dsl.Response("BadRequest", dsl.StatusBadRequest)
			dsl.Response("Conflict", dsl.StatusConflict)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("list-committee-documents", func() {
		dsl.Description("List all documents for a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
		})

		dsl.Result(dsl.ArrayOf(CommitteeDocumentWithReadonlyAttributes))

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/documents")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("get-committee-document", func() {
		dsl.Description("Get metadata for a single committee document")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			DocumentUIDAttribute()
		})

		dsl.Result(func() {
			dsl.Attribute("committee-document", CommitteeDocumentWithReadonlyAttributes)
			ETagAttribute()
			dsl.Required("committee-document")
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/documents/{document_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("document_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.Response(dsl.StatusOK, func() {
				dsl.Body("committee-document")
				dsl.Header("etag:ETag")
			})
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("download-committee-document", func() {
		dsl.Description("Download the file for a committee document")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			DocumentUIDAttribute()
		})

		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.GET("/committees/{uid}/documents/{document_uid}/download")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("document_uid")
			dsl.Header("bearer_token:Authorization")
			dsl.SkipResponseBodyEncodeDecode()
			dsl.Response(dsl.StatusOK)
			dsl.Response("NotFound", dsl.StatusNotFound)
			dsl.Response("InternalServerError", dsl.StatusInternalServerError)
			dsl.Response("ServiceUnavailable", dsl.StatusServiceUnavailable)
		})
	})

	dsl.Method("delete-committee-document", func() {
		dsl.Description("Delete a document from a committee")

		dsl.Security(JWTAuth)

		dsl.Payload(func() {
			BearerTokenAttribute()
			VersionAttribute()
			CommitteeUIDAttribute()
			DocumentUIDAttribute()
			IfMatchAttribute()

			dsl.Required("uid", "document_uid", "if_match")
		})

		dsl.Error("BadRequest", BadRequestError, "Bad request")
		dsl.Error("NotFound", NotFoundError, "Resource not found")
		dsl.Error("Conflict", ConflictError, "Conflict")
		dsl.Error("InternalServerError", InternalServerError, "Internal server error")
		dsl.Error("ServiceUnavailable", ServiceUnavailableError, "Service unavailable")

		dsl.HTTP(func() {
			dsl.DELETE("/committees/{uid}/documents/{document_uid}")
			dsl.Param("version:v")
			dsl.Param("uid")
			dsl.Param("document_uid")
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
	dsl.Files("/_committees/openapi.json", "gen/http/openapi.json", func() {
		dsl.Meta("swagger:generate", "false")
	})
	dsl.Files("/_committees/openapi.yaml", "gen/http/openapi.yaml", func() {
		dsl.Meta("swagger:generate", "false")
	})
	dsl.Files("/_committees/openapi3.json", "gen/http/openapi3.json", func() {
		dsl.Meta("swagger:generate", "false")
	})
	dsl.Files("/_committees/openapi3.yaml", "gen/http/openapi3.yaml", func() {
		dsl.Meta("swagger:generate", "false")
	})
})
