// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package errors

import "errors"

// Validation represents a validation error in the application.
type Validation struct {
	base
}

// Error returns the error message for Validation.
func (v Validation) Error() string {
	return v.error()
}

// NewValidation creates a new Validation error with the provided message.
func NewValidation(message string, err ...error) Validation {
	return Validation{
		base: base{
			message: message,
			err:     errors.Join(err...),
		},
	}
}

// NotFound represents a not found error in the application.
type NotFound struct {
	base
}

// Error returns the error message for NotFound.
func (v NotFound) Error() string {
	return v.error()
}

// NewNotFound creates a new NotFound error with the provided message.
func NewNotFound(message string, err ...error) NotFound {
	return NotFound{
		base: base{
			message: message,
			err:     errors.Join(err...),
		},
	}
}

// Conflict represents a conflict error in the application.
type Conflict struct {
	base
}

// Error returns the error message for Conflict.
func (c Conflict) Error() string {
	return c.error()
}

// NewConflict creates a new Conflict error with the provided message.
func NewConflict(message string, err ...error) Conflict {
	return Conflict{
		base: base{
			message: message,
			err:     errors.Join(err...),
		},
	}
}

// Forbidden represents a forbidden error in the application.
type Forbidden struct {
	base
}

// Error returns the error message for Forbidden.
func (f Forbidden) Error() string {
	return f.error()
}

// NewForbidden creates a new Forbidden error with the provided message.
func NewForbidden(message string, errs ...error) Forbidden {
	return Forbidden{
		base: base{
			message: message,
			err:     errors.Join(errs...),
		},
	}
}

// TooManyRequests is a 429 — used by weekly-brief throttle enforcement to
// carry the per-window throttle counters back to the HTTP layer without
// stringifying them. Handlers extract the typed value via errors.As.
type TooManyRequests struct {
	base
	// GeneratesUsed is the count consumed for fresh generations in this window.
	GeneratesUsed int
	// GeneratesLimit is the cap on fresh generations.
	GeneratesLimit int
	// RegenerationsUsed is the count consumed for regenerations.
	RegenerationsUsed int
	// RegenerationsLimit is the cap on regenerations.
	RegenerationsLimit int
	// WindowResetsAt is the timestamp at which the window resets.
	WindowResetsAt string
}

// Error returns the error message for TooManyRequests.
func (t TooManyRequests) Error() string {
	return t.error()
}

// NewTooManyRequests creates a TooManyRequests error.
func NewTooManyRequests(message string, generatesUsed, generatesLimit, regenerationsUsed, regenerationsLimit int, windowResetsAt string) TooManyRequests {
	return TooManyRequests{
		base:               base{message: message},
		GeneratesUsed:      generatesUsed,
		GeneratesLimit:     generatesLimit,
		RegenerationsUsed:  regenerationsUsed,
		RegenerationsLimit: regenerationsLimit,
		WindowResetsAt:     windowResetsAt,
	}
}

// EditedBriefExists is a 409 specific to the weekly-brief generate flow —
// distinguished from generic Conflict so the handler can attach the current
// brief revision to the response body.
type EditedBriefExists struct {
	base
	// Revision is the current revision of the edited brief.
	Revision uint64
}

// Error returns the error message for EditedBriefExists.
func (e EditedBriefExists) Error() string {
	return e.error()
}

// NewEditedBriefExists creates an EditedBriefExists conflict error.
func NewEditedBriefExists(revision uint64) EditedBriefExists {
	return EditedBriefExists{
		base:     base{message: "an edited brief already exists for this window"},
		Revision: revision,
	}
}

// RevisionMismatch is a 409 specific to the weekly-brief edit/save flow —
// returned when the caller's optimistic-concurrency token does not match the
// brief's current revision (someone else edited in the meantime). Distinguished
// from generic Conflict so the handler can attach the current revision to the
// response body, letting the client refetch and retry.
type RevisionMismatch struct {
	base
	// Revision is the brief's current (server-side) revision.
	Revision uint64
}

// Error returns the error message for RevisionMismatch.
func (e RevisionMismatch) Error() string {
	return e.error()
}

// NewRevisionMismatch creates a RevisionMismatch conflict error carrying the
// brief's current revision.
func NewRevisionMismatch(revision uint64) RevisionMismatch {
	return RevisionMismatch{
		base:     base{message: "brief was modified by someone else; refresh and retry"},
		Revision: revision,
	}
}
