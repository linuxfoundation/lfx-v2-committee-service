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
// list-members queries and reacting to committee-change events that require member re-sync.
type CommitteeMemberHandler interface {
	// HandleCommitteeListMembers handles committee list members messages
	HandleCommitteeListMembers(ctx context.Context, msg TransportMessenger) ([]byte, error)
	// HandleCommitteeUpdated handles committee updated events and re-syncs denormalized member data
	HandleCommitteeUpdated(ctx context.Context, msg TransportMessenger) ([]byte, error)
}

// CommitteeMailingListHandler handles events from mailing-list-api.
type CommitteeMailingListHandler interface {
	// HandleCommitteeMailingListChanged handles mailing list status change events from mailing-list-api
	HandleCommitteeMailingListChanged(ctx context.Context, msg TransportMessenger) ([]byte, error)
}

// MessageHandler is the aggregate interface for all inbound NATS message handlers.
type MessageHandler interface {
	CommitteeAttributeHandler
	CommitteeMemberHandler
	CommitteeMailingListHandler
}
