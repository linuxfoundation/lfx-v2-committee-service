// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package errors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ────────────────────────────────────────────────────────────────────

// errSentinel is a simple error used to test wrapping behaviour.
var errSentinel = errors.New("underlying cause")

// ── Validation ─────────────────────────────────────────────────────────────────

func TestValidation_Error(t *testing.T) {
	err := NewValidation("invalid input")
	assert.Equal(t, "invalid input", err.Error())
}

func TestValidation_ErrorWithWrapped(t *testing.T) {
	err := NewValidation("invalid input", errSentinel)
	assert.Equal(t, fmt.Sprintf("invalid input: %v", errSentinel), err.Error())
	assert.True(t, errors.Is(err, errSentinel), "errors.Is must reach the wrapped cause")
}

func TestValidation_ErrorsAs(t *testing.T) {
	err := NewValidation("bad field")
	var target Validation
	require.True(t, errors.As(err, &target))
	assert.Equal(t, "bad field", target.Error())
}

func TestValidation_MultipleWrapped(t *testing.T) {
	second := errors.New("second cause")
	err := NewValidation("multi", errSentinel, second)
	assert.Contains(t, err.Error(), "underlying cause")
	assert.Contains(t, err.Error(), "second cause")
	// errors.Join wraps both; errors.Is must find either cause through Unwrap.
	assert.True(t, errors.Is(err, errSentinel), "errors.Is must reach the first joined cause")
	assert.True(t, errors.Is(err, second), "errors.Is must reach the second joined cause")
}

func TestValidation_NoWrap_ReturnsNonNilError(t *testing.T) {
	err := NewValidation("no wrap")
	require.NotNil(t, err)
	assert.Equal(t, "no wrap", err.Error())
}

// ── NotFound ───────────────────────────────────────────────────────────────────

func TestNotFound_Error(t *testing.T) {
	err := NewNotFound("resource not found")
	assert.Equal(t, "resource not found", err.Error())
}

func TestNotFound_ErrorWithWrapped(t *testing.T) {
	err := NewNotFound("resource not found", errSentinel)
	assert.Equal(t, fmt.Sprintf("resource not found: %v", errSentinel), err.Error())
}

func TestNotFound_ErrorsAs(t *testing.T) {
	err := NewNotFound("missing record")
	var target NotFound
	require.True(t, errors.As(err, &target))
}

// ── Conflict ──────────────────────────────────────────────────────────────────

func TestConflict_Error(t *testing.T) {
	err := NewConflict("already exists")
	assert.Equal(t, "already exists", err.Error())
}

func TestConflict_ErrorsAs(t *testing.T) {
	err := NewConflict("duplicate name")
	var target Conflict
	require.True(t, errors.As(err, &target))
}

// ── Forbidden ─────────────────────────────────────────────────────────────────

func TestForbidden_Error(t *testing.T) {
	err := NewForbidden("access denied")
	assert.Equal(t, "access denied", err.Error())
}

func TestForbidden_ErrorsAs(t *testing.T) {
	err := NewForbidden("not allowed")
	var target Forbidden
	require.True(t, errors.As(err, &target))
}

func TestForbidden_WrapsMultipleErrors(t *testing.T) {
	second := errors.New("second")
	err := NewForbidden("multi", errSentinel, second)
	assert.Contains(t, err.Error(), "underlying cause")
	assert.Contains(t, err.Error(), "second")
}

// ── TooManyRequests ───────────────────────────────────────────────────────────

func TestTooManyRequests_Error(t *testing.T) {
	err := NewTooManyRequests("rate limit exceeded", 2, 5, 1, 3, "2026-10-01T00:00:00Z")
	assert.Equal(t, "rate limit exceeded", err.Error())
}

func TestTooManyRequests_Fields(t *testing.T) {
	err := NewTooManyRequests("limit", 2, 5, 1, 3, "2026-10-01T00:00:00Z")
	assert.Equal(t, 2, err.GeneratesUsed)
	assert.Equal(t, 5, err.GeneratesLimit)
	assert.Equal(t, 1, err.RegenerationsUsed)
	assert.Equal(t, 3, err.RegenerationsLimit)
	assert.Equal(t, "2026-10-01T00:00:00Z", err.WindowResetsAt)
}

func TestTooManyRequests_ErrorsAs(t *testing.T) {
	err := NewTooManyRequests("limit", 2, 5, 1, 3, "2026-10-01T00:00:00Z")
	var target TooManyRequests
	require.True(t, errors.As(err, &target))
	// Fields are preserved through the As extraction.
	assert.Equal(t, 2, target.GeneratesUsed)
	assert.Equal(t, 5, target.GeneratesLimit)
	assert.Equal(t, "2026-10-01T00:00:00Z", target.WindowResetsAt)
}

func TestTooManyRequests_ZeroValues(t *testing.T) {
	err := NewTooManyRequests("zero", 0, 0, 0, 0, "")
	assert.Equal(t, 0, err.GeneratesUsed)
	assert.Equal(t, "", err.WindowResetsAt)
}

// ── EditedBriefExists ─────────────────────────────────────────────────────────

func TestEditedBriefExists_Error(t *testing.T) {
	err := NewEditedBriefExists(7)
	assert.Equal(t, "an edited brief already exists for this window", err.Error())
}

func TestEditedBriefExists_Revision(t *testing.T) {
	err := NewEditedBriefExists(42)
	assert.Equal(t, uint64(42), err.Revision)
}

func TestEditedBriefExists_ErrorsAs(t *testing.T) {
	err := NewEditedBriefExists(3)
	var target EditedBriefExists
	require.True(t, errors.As(err, &target))
	assert.Equal(t, uint64(3), target.Revision)
}

func TestEditedBriefExists_ZeroRevision(t *testing.T) {
	err := NewEditedBriefExists(0)
	assert.Equal(t, uint64(0), err.Revision)
}

// ── NoChatWebhook ─────────────────────────────────────────────────────────────

func TestNoChatWebhook_Error(t *testing.T) {
	err := NewNoChatWebhook()
	assert.Equal(t, "no chat webhook URL is configured for this committee", err.Error())
}

func TestNoChatWebhook_ErrorsAs(t *testing.T) {
	err := NewNoChatWebhook()
	var target NoChatWebhook
	require.True(t, errors.As(err, &target))
}

// ── RevisionMismatch ──────────────────────────────────────────────────────────

func TestRevisionMismatch_Error(t *testing.T) {
	err := NewRevisionMismatch(99)
	assert.Equal(t, "brief was modified by someone else; refresh and retry", err.Error())
}

func TestRevisionMismatch_Revision(t *testing.T) {
	err := NewRevisionMismatch(99)
	assert.Equal(t, uint64(99), err.Revision)
}

func TestRevisionMismatch_ErrorsAs(t *testing.T) {
	err := NewRevisionMismatch(12)
	var target RevisionMismatch
	require.True(t, errors.As(err, &target))
	assert.Equal(t, uint64(12), target.Revision)
}

func TestRevisionMismatch_ZeroRevision(t *testing.T) {
	err := NewRevisionMismatch(0)
	assert.Equal(t, uint64(0), err.Revision)
}

// ── Unexpected ────────────────────────────────────────────────────────────────

func TestUnexpected_Error(t *testing.T) {
	err := NewUnexpected("something went wrong")
	assert.Equal(t, "something went wrong", err.Error())
}

func TestUnexpected_ErrorWithWrapped(t *testing.T) {
	err := NewUnexpected("something went wrong", errSentinel)
	assert.Contains(t, err.Error(), "something went wrong")
	assert.Contains(t, err.Error(), "underlying cause")
	assert.True(t, errors.Is(err, errSentinel), "errors.Is must reach the wrapped cause")
}

func TestUnexpected_ErrorsAs(t *testing.T) {
	err := NewUnexpected("oops")
	var target Unexpected
	require.True(t, errors.As(err, &target))
}

// ── ServiceUnavailable ────────────────────────────────────────────────────────

func TestServiceUnavailable_Error(t *testing.T) {
	err := NewServiceUnavailable("dependency down")
	assert.Equal(t, "dependency down", err.Error())
}

func TestServiceUnavailable_ErrorsAs(t *testing.T) {
	err := NewServiceUnavailable("nats unavailable")
	var target ServiceUnavailable
	require.True(t, errors.As(err, &target))
}

// ── base wrapping behaviour ───────────────────────────────────────────────────

// TestBase_ErrorFormat verifies that a wrapped cause is appended after ": "
// so callers can read the full chain from the Error() string alone.
func TestBase_ErrorFormat(t *testing.T) {
	errCause := errors.New("root cause")
	err := NewValidation("outer message", errCause)
	assert.Equal(t, "outer message: root cause", err.Error())
}

// TestBase_NoWrap_NoColon verifies that the ": <nil>" suffix does not appear
// when no underlying error is supplied.
func TestBase_NoWrap_NoColon(t *testing.T) {
	err := NewValidation("just a message")
	assert.NotContains(t, err.Error(), ":")
}

// TestAllTypes_ErrorsAs_DoNotCrossMatch verifies that errors.As does not
// accidentally match the wrong concrete type.
func TestAllTypes_ErrorsAs_DoNotCrossMatch(t *testing.T) {
	validationErr := NewValidation("v")
	var notFound NotFound
	assert.False(t, errors.As(validationErr, &notFound), "Validation must not match NotFound")

	notFoundErr := NewNotFound("nf")
	var conflict Conflict
	assert.False(t, errors.As(notFoundErr, &conflict), "NotFound must not match Conflict")
}
