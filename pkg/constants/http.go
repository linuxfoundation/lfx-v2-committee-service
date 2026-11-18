// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

type requestIDHeaderType string

// RequestIDHeader is the header name for the request ID
const RequestIDHeader requestIDHeaderType = "X-REQUEST-ID"

type contextID int

const (
	// PrincipalContextID is the context ID for the principal (LFX username) from JWT claims
	PrincipalContextID contextID = iota
	// EmailContextID is the context ID for the email claim from the Heimdall JWT, populated by JWTAuth.
	// Semantically equivalent to meeting-service's "email" context key; this service uses a private typed key.
	EmailContextID
	// AuditAttributionContextID carries server-attested immutable audit attribution.
	// It must never be populated directly from request headers or payloads.
	AuditAttributionContextID
	// ReadOnlyRequestContextID avoids directory/profile capture on HTTP reads.
	ReadOnlyRequestContextID
	// AuditProjectionContextID is selected only by trusted derived-data writers.
	AuditProjectionContextID
)

type contextPrincipal string

// AuthorizationHeader is the header name for the authorization
const AuthorizationHeader string = "authorization"

type contextAuthorization string

// XOnBehalfOfHeader is the header name for the on behalf of principal
const XOnBehalfOfHeader string = "x-on-behalf-of"

// AuthorizationContextID is the context ID for the authorization
const AuthorizationContextID contextAuthorization = "authorization"

// OnBehalfContextID is the context ID for the principal
const OnBehalfContextID contextPrincipal = "x-on-behalf-of"
