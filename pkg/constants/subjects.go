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

	// CommitteeGetProjectSubject is the subject for resolving a committee UID to its owning project UID.
	// The subject is of the form: lfx.committee-api.get_project
	// Request/response types: pkg/api.GetCommitteeProjectRequest / pkg/api.GetCommitteeProjectResponse
	CommitteeGetProjectSubject = "lfx.committee-api.get_project"

	// ProjectGetNameSubject is the subject for the project get name.
	// The subject is of the form: lfx.projects-api.get_name
	ProjectGetNameSubject = "lfx.projects-api.get_name"

	// ProjectGetSlugSubject is the subject for the project get slug.
	// The subject is of the form: lfx.projects-api.get_slug
	ProjectGetSlugSubject = "lfx.projects-api.get_slug"

	// ProjectGetWritersSubject is the subject for getting the writers list from project settings.
	// Request: plain-text project UID. Reply: JSON-encoded []model.CommitteeUser (empty array when no writers).
	// The subject is of the form: lfx.projects-api.get_writers
	ProjectGetWritersSubject = "lfx.projects-api.get_writers"

	// AuthEmailToUsernameLookupSubject resolves a registered LFID username by primary email.
	// Request: plain-text email. Reply: plain-text username on success, JSON error envelope on miss.
	AuthEmailToUsernameLookupSubject = "lfx.auth-service.email_to_username"

	// AuthUserEmailsReadSubject is the subject for looking up a user's email addresses by principal.
	// The subject is of the form: lfx.auth-service.user_emails.read
	AuthUserEmailsReadSubject = "lfx.auth-service.user_emails.read"

	// AuthUserMetadataReadSubject is the subject for looking up a user's profile metadata by principal.
	// The subject is of the form: lfx.auth-service.user_metadata.read
	AuthUserMetadataReadSubject = "lfx.auth-service.user_metadata.read"

	// IndexCommitteeSubject is the subject for the committee index.
	// The subject is of the form: lfx.index.committee
	IndexCommitteeSubject = "lfx.index.committee"

	// IndexCommitteeSettingsSubject is the subject for the committee settings index.
	// The subject is of the form: lfx.index.committee.committee_settings
	IndexCommitteeSettingsSubject = "lfx.index.committee_settings"

	// IndexCommitteeMemberSubject is the subject for the committee member index.
	// The subject is of the form: lfx.index.committee_member
	IndexCommitteeMemberSubject = "lfx.index.committee_member"

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

// Subjects consumed by the committee service from other services
const (
	// MailingListCommitteeChangedSubject is consumed from mailing-list-api when
	// committee-related mailing list state changes (e.g. has_mailing_list flag).
	MailingListCommitteeChangedSubject = "lfx.mailing-list-api.committee_mailing_list.changed"
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

	// CommitteeUpdatedSubject is emitted after a successful committee update.
	// The payload is a CommitteeEvent wrapping CommitteeUpdateEventData (old + new image).
	CommitteeUpdatedSubject = "lfx.committee-api.committee.updated"

	// CommitteeSettingsUpdatedSubject is emitted after a successful committee settings update.
	// The payload is a CommitteeEvent wrapping CommitteeSettingsUpdateEventData (old + new image).
	CommitteeSettingsUpdatedSubject = "lfx.committee-api.committee_settings.updated"

	// CommitteeDocumentCreatedSubject is emitted after a file document is successfully uploaded to a committee.
	// The payload is a CommitteeEvent wrapping *model.CommitteeDocument.
	CommitteeDocumentCreatedSubject = "lfx.committee-api.committee_document.created"

	// CommitteeLinkCreatedSubject is emitted after a link is successfully added to a committee.
	// The payload is a CommitteeEvent wrapping *model.CommitteeLink.
	CommitteeLinkCreatedSubject = "lfx.committee-api.committee_link.created"

	// CommitteeApplicationSubmittedSubject is emitted after an application is submitted (or reinstated).
	// The payload is a CommitteeEvent wrapping *model.CommitteeApplication.
	// Consumed by the notification handler to fan-out to LFID committee writers.
	CommitteeApplicationSubmittedSubject = "lfx.committee-api.committee_application.submitted"

	// CommitteeApplicationUpdatedSubject is emitted after an application is approved or rejected.
	// The payload is a CommitteeEvent wrapping *model.CommitteeApplication.
	// Consumed by the notification handler to notify the applicant of the decision.
	CommitteeApplicationUpdatedSubject = "lfx.committee-api.committee_application.updated"
	// GenerateWeeklyBriefRequestedSubject is emitted by POST /weekly-briefs/generate
	// after the brief is claimed (persisted in the "generating" state). The
	// weekly-brief-events stream captures it and the durable generate consumer runs
	// the source gather + LLM + finalize asynchronously.
	GenerateWeeklyBriefRequestedSubject = "lfx.committee-api.weekly_brief.generate_requested"
)
