// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// CommitteeAttributeHandler handles request/reply messages from other services
// querying committee attribute data.
type CommitteeAttributeHandler interface {
	// HandleCommitteeGetAttribute handles committee get attribute messages
	HandleCommitteeGetAttribute(ctx context.Context, msg TransportMessenger, attribute string) ([]byte, error)
}

// CommitteeMemberHandler handles member-related messages: responding to external
// list-members queries, reacting to committee-change events that require member re-sync,
// and keeping the total_members counter accurate via durable stream events.
type CommitteeMemberHandler interface {
	// HandleCommitteeListMembers handles committee list members messages
	HandleCommitteeListMembers(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeUpdated handles committee updated events and re-syncs denormalized member data
	HandleCommitteeUpdated(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeTotalMembersSync reacts to committee_member.created and committee_member.deleted
	// stream events and updates the total_members counter on the committee record.
	HandleCommitteeTotalMembersSync(ctx context.Context, msg StreamMessenger) error
}

// CommitteeMailingListHandler handles events from mailing-list-api.
type CommitteeMailingListHandler interface {
	// HandleCommitteeMailingListChanged handles mailing list status change events from mailing-list-api
	HandleCommitteeMailingListChanged(ctx context.Context, msg TransportMessenger) ([]byte, error)
}

// CommitteeNotificationHandler handles events that trigger notification emails to committee members.
type CommitteeNotificationHandler interface {
	// HandleCommitteeMemberCreated sends a notification email when a member is added to a committee.
	HandleCommitteeMemberCreated(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeMemberDeleted sends a removal notification email when an LF committee
	// member is deleted. Non-LF members receive no notification.
	HandleCommitteeMemberDeleted(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeSettingsUpdated sends notification emails when Writers/Auditors are added,
	// have their role-set changed, or are fully removed from the committee.
	HandleCommitteeSettingsUpdated(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleInviteAccepted processes an invite acceptance event from the invite service.
	// It locates the settings record that owns the invite, promotes the user from non-LFID
	// (email-only) to LFID (username set, invite cleared), and fires FGA + indexer messages.
	HandleInviteAccepted(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeDocumentCreated sends notification emails to all LFID members, writers, and
	// auditors of the committee when a new file document is uploaded. Best-effort: send errors
	// are logged but not returned.
	HandleCommitteeDocumentCreated(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeLinkCreated sends notification emails to all LFID members, writers, and
	// auditors of the committee when a new link is added. Best-effort: send errors are logged
	// but not returned.
	HandleCommitteeLinkCreated(ctx context.Context, msg TransportMessenger) ([]byte, error)
}

// WeeklyBriefGenerateHandler handles the durable async weekly-brief generation
// workflow, reacting to generate-requested stream events.
type WeeklyBriefGenerateHandler interface {
	// HandleGenerateWeeklyBriefRequested runs the async Fulfill phase (source
	// gather → LLM → finalize) for a claimed weekly brief.
	HandleGenerateWeeklyBriefRequested(ctx context.Context, msg StreamMessenger) error
}

// MessageHandler is the aggregate interface for all inbound NATS message handlers.
type MessageHandler interface {
	CommitteeAttributeHandler
	CommitteeMemberHandler
	CommitteeMailingListHandler
	CommitteeNotificationHandler
	WeeklyBriefGenerateHandler
}
