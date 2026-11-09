// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// MemberCreateIntent is the minimal original intent retained while a bulk create
// is pending. It is not member data and must not enter ordinary reads or exports.
type MemberCreateIntent struct {
	SkipNotification bool   `json:"skip_notification"`
	CreatedBy        string `json:"created_by,omitempty"`
	AccountAbsent    bool   `json:"account_absent"`
}

// MemberCreateResult identifies the stored member, never the retry's replacement
// payload. Added elects exactly one publication attempt, not guaranteed delivery.
type MemberCreateResult struct {
	Member *model.CommitteeMember
	Added  bool
	Intent MemberCreateIntent
}

// MemberCreatePreparation selects the original operation before current validation.
// Pending=false identifies a committed membership; it must not be published again.
type MemberCreatePreparation struct {
	Member  *model.CommitteeMember
	Intent  MemberCreateIntent
	Pending bool
}

// RecoverableCommitteeMemberWriter supports forward-only bulk creation. A
// reservation is not proof of membership: uncertain state stays retryable, and
// recovery must fence old writers before reclaiming a reservation. Completed
// retries return Added=false and never elect another notification attempt.
type RecoverableCommitteeMemberWriter interface {
	// SupportsMemberCreateRecovery distinguishes KV-primary recovery from transactional stores.
	SupportsMemberCreateRecovery() bool
	// AbortMemberCreatesForIdentity clears matching pending snapshots, not committed members.
	// An empty username matches by email; a supplied username and email must both match.
	AbortMemberCreatesForIdentity(context.Context, string, string) error
	FindMemberCreate(context.Context, string, string) (*MemberCreatePreparation, error)
	AbortMemberCreate(context.Context, *model.CommitteeMember) error
	CreateMemberRecoverable(context.Context, *model.CommitteeMember, MemberCreateIntent) (*MemberCreateResult, error)
}
