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

	// AuthEmailToSubLookupSubject is the subject for the email to sub lookup.
	// The subject is of the form: lfx.auth-service.email_to_sub
	AuthEmailToSubLookupSubject = "lfx.auth-service.email_to_sub"

	// AuthUserEmailsReadSubject is the subject for looking up a user's email addresses by principal.
	// The subject is of the form: lfx.auth-service.user_emails.read
	AuthUserEmailsReadSubject = "lfx.auth-service.user_emails.read"

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

	// PutMemberCommitteeSubject is the subject for the committee member access control updates.
	// The subject is of the form: lfx.put_member.committee
	PutMemberCommitteeSubject = "lfx.put_member.committee"

	// RemoveMemberCommitteeSubject is the subject for the committee member access control deletion.
	// The subject is of the form: lfx.remove_member.committee
	RemoveMemberCommitteeSubject = "lfx.remove_member.committee"

	// IndexCommitteeInviteSubject is the subject for the committee invite index.
	// The subject is of the form: lfx.index.committee_invite
	IndexCommitteeInviteSubject = "lfx.index.committee_invite"

	// IndexCommitteeApplicationSubject is the subject for the committee application index.
	// The subject is of the form: lfx.index.committee_application
	IndexCommitteeApplicationSubject = "lfx.index.committee_application"

	// IndexCommitteeLinkSubject is the subject for the committee link index.
	// The subject is of the form: lfx.index.committee_link
	IndexCommitteeLinkSubject = "lfx.index.committee_link"

	// IndexCommitteeLinkFolderSubject is the subject for the committee link folder index.
	// The subject is of the form: lfx.index.committee_link_folder
	IndexCommitteeLinkFolderSubject = "lfx.index.committee_link_folder"

	// IndexCommitteeDocumentSubject is the subject for the committee document index.
	// The subject is of the form: lfx.index.committee_document
	IndexCommitteeDocumentSubject = "lfx.index.committee_document"
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
