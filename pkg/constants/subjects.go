// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

const (
	// CommitteeAPIQueue is the queue for the committee API.
	// The queue is of the form: lfx.committee-api.queue
	CommitteeAPIQueue = "lfx.committee-api.queue"

	// CommitteeGetNameSubject is the subject for the committee get name.
	// The subject is of the form: lfx.committee-api.get_name
	CommitteeGetNameSubject = "lfx.committee-api.get_name"

	// CommitteeListMembersSubject is the subject for listing committee members.
	// The subject is of the form: lfx.committee-api.list_members
	CommitteeListMembersSubject = "lfx.committee-api.list_members"

	// ProjectGetNameSubject is the subject for the project get name.
	// The subject is of the form: lfx.projects-api.get_name
	ProjectGetNameSubject = "lfx.projects-api.get_name"
	// ProjectGetSlugSubject is the subject for the project get slug.
	// The subject is of the form: lfx.projects-api.get_slug
	ProjectGetSlugSubject = "lfx.projects-api.get_slug"

	// IndexCommitteeSubject is the subject for the committee index.
	// The subject is of the form: lfx.index.committee
	IndexCommitteeSubject = "lfx.index.committee"

	// IndexCommitteeSettingsSubject is the subject for the committee settings index.
	// The subject is of the form: lfx.index.committee.committee_settings
	IndexCommitteeSettingsSubject = "lfx.index.committee_settings"

	// IndexCommitteeMemberSubject is the subject for the committee member index.
	// The subject is of the form: lfx.index.committee_member
	IndexCommitteeMemberSubject = "lfx.index.committee_member"

	// UpdateAccessCommitteeSubject is the subject for the committee access control updates.
	// The subject is of the form: lfx.update_access.committee
	UpdateAccessCommitteeSubject = "lfx.update_access.committee"

	// DeleteAllAccessCommitteeSubject is the  subject for the committee access control deletion.
	// The subject is of the form: lfx.delete_all_access.committee
	DeleteAllAccessCommitteeSubject = "lfx.delete_all_access.committee"
)

// Event subjects emitted by the committee service for general consumption by any service
const (
	// CommitteeMemberCreatedSubject is the subject for committee member creation events.
	// The subject is of the form: lfx.committee-api.member_created
	CommitteeMemberCreatedSubject = "lfx.committee-api.committee_member.created"

	// CommitteeMemberDeletedSubject is the subject for committee member deletion events.
	// The subject is of the form: lfx.committee-api.committee_member.deleted
	CommitteeMemberDeletedSubject = "lfx.committee-api.committee_member.deleted"

	// CommitteeMemberUpdatedSubject is the subject for committee member update events.
	// The subject is of the form: lfx.committee-api.committee_member.updated
	CommitteeMemberUpdatedSubject = "lfx.committee-api.committee_member.updated"
)
