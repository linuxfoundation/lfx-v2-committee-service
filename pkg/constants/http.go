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
	// EmailContextID is reserved for a future JWT email claim; currently unused (email is resolved via auth-service).
	EmailContextID
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
