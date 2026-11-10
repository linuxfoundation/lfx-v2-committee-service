// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

// MaxBulkMembers is the maximum number of rows a single bulk member create request may submit.
const MaxBulkMembers = 100

// BulkMemberStatus is the outcome of one row of a bulk member create request.
type BulkMemberStatus string

const (
	// BulkMemberAdded means the row created a new committee member.
	BulkMemberAdded BulkMemberStatus = "added"
	// BulkMemberSkipped means the row's email is already a member, or repeats an earlier row.
	BulkMemberSkipped BulkMemberStatus = "skipped"
	// BulkMemberFailed means the row was not added; Reason says whether retrying can help.
	BulkMemberFailed BulkMemberStatus = "failed"
)

// BulkMemberReason explains why a bulk row was skipped or failed.
type BulkMemberReason string

const (
	// BulkReasonAlreadyMember means the email is already a member of the committee.
	BulkReasonAlreadyMember BulkMemberReason = "already_member"
	// BulkReasonDuplicateInRequest means an earlier row in the same request has the same email.
	BulkReasonDuplicateInRequest BulkMemberReason = "duplicate_in_request"
	// BulkReasonInvalid means the row failed validation and will fail again unless it changes.
	BulkReasonInvalid BulkMemberReason = "invalid"
	// BulkReasonTemporary means a dependency failed for the row; resubmitting it later may succeed.
	BulkReasonTemporary BulkMemberReason = "temporary"
	// BulkReasonNotAttempted means the row was never started because the import halted.
	BulkReasonNotAttempted BulkMemberReason = "not_attempted"
)

// BulkMemberResult is the outcome of one submitted row of a bulk member create request.
type BulkMemberResult struct {
	// Index is the zero-based position of the row in the submitted list.
	Index int
	// Email is the email address submitted for the row.
	Email string
	// Status is the row outcome.
	Status BulkMemberStatus
	// Reason is empty for added rows.
	Reason BulkMemberReason
	// Retryable reports whether resubmitting the row later may succeed.
	Retryable bool
	// Message is a client-safe detail for skipped and failed rows.
	Message string
	// MemberUID is the created member's UID, or the existing member's UID when known.
	MemberUID string
	// Member is the created member; set only for added rows.
	Member *CommitteeMember
}
