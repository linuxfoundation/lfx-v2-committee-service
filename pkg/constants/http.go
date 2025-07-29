// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

type requestIDHeaderType string

type contextID int

const (
	// RequestIDHeader is the header name for the request ID
	RequestIDHeader requestIDHeaderType = "X-REQUEST-ID"
	// PrincipalContextID
	PrincipalContextID contextID = iota
)
