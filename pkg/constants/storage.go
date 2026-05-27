// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

// NATS Key-Value store bucket names.
const (
	// KVBucketNameCommittees is the name of the KV bucket for committees.
	KVBucketNameCommittees = "committees"

	// KVBucketNameCommitteeSettings is the name of the KV bucket for committee settings.
	KVBucketNameCommitteeSettings = "committee-settings"

	// KVBucketNameCommitteeMembers is the name of the KV bucket for committee members.
	KVBucketNameCommitteeMembers = "committee-members"

	// KVBucketNameCommitteeInvites is the name of the KV bucket for committee invites.
	KVBucketNameCommitteeInvites = "committee-invites"

	// KVBucketNameCommitteeApplications is the name of the KV bucket for committee applications.
	KVBucketNameCommitteeApplications = "committee-applications"

	// KVLookupPrefix is the prefix for lookup keys in the KV store.
	KVLookupPrefix = "lookup/committees/%s"

	// KVLookupMemberPrefix is the prefix for member lookup keys in the KV store.
	KVLookupMemberPrefix = "lookup/committee-members/%s"

	// KVLookupInvitePrefix is the prefix for invite lookup keys in the KV store.
	KVLookupInvitePrefix = "lookup/committee-invites/%s"

	// KVLookupSettingsInvitePrefix is the secondary index that maps an invite UID to the
	// committee settings UID that contains that invite, stored in the committee-settings bucket.
	// Key: "lookup/committee-settings-invite/<invite_uid>", Value: <committee_uid>
	KVLookupSettingsInvitePrefix = "lookup/committee-settings-invite/%s"

	// KVLookupApplicationPrefix is the prefix for application lookup keys in the KV store.
	KVLookupApplicationPrefix = "lookup/committee-applications/%s"

	// KVLookupSSOGroupNamePrefix is the prefix for SSO group name lookup keys in the KV store.
	KVLookupSSOGroupNamePrefix = "lookup/committee-sso-groups/%s"

	KVSlugPrefix = "slug/"

	// KVBucketNameCommitteeLinks is the name of the KV bucket for committee links.
	KVBucketNameCommitteeLinks = "committee-links"

	// KVBucketNameCommitteeFolders is the name of the KV bucket for committee folders.
	KVBucketNameCommitteeFolders = "committee-folders"

	// KVLookupFolderPrefix is the prefix for folder lookup keys in the KV store.
	KVLookupFolderPrefix = "lookup/committee-folders/%s"

	// KVBucketNameCommitteeDocuments is the name of the KV bucket for committee document metadata.
	KVBucketNameCommitteeDocuments = "committee-documents-metadata"

	// KVLookupDocumentPrefix is the prefix for document name lookup keys in the KV store.
	KVLookupDocumentPrefix = "lookup/committee-documents/%s"

	// ObjectStoreNameCommitteeDocuments is the name of the Object Store for committee document files.
	ObjectStoreNameCommitteeDocuments = "committee-documents"

	// StreamNameCommitteeMemberEvents is the JetStream stream that captures committee member domain events.
	StreamNameCommitteeMemberEvents = "committee-member-events"

	// ConsumerNameTotalMembersSync is the durable JetStream consumer for keeping total_members accurate.
	ConsumerNameTotalMembersSync = "committee-service-total-members"

	// StreamNameWeeklyBriefEvents is the JetStream stream that captures weekly-brief
	// generation events (the durable async generate workflow).
	StreamNameWeeklyBriefEvents = "weekly-brief-events"

	// ConsumerNameWeeklyBriefGenerate is the durable JetStream consumer that runs
	// the async weekly-brief generation (source gather → LLM → finalize).
	ConsumerNameWeeklyBriefGenerate = "committee-service-weekly-brief-generate"

	// KVBucketNameGroupWeeklyBriefs is the KV bucket for working-group weekly briefs.
	// Key: brief UID; Value: full brief JSON.
	KVBucketNameGroupWeeklyBriefs = "group-weekly-briefs"

	// KVBucketNameGroupWeeklyBriefUIDIndex is the KV bucket mapping
	// {committee_uid}.{window_yyyymmdd} → brief UID.
	KVBucketNameGroupWeeklyBriefUIDIndex = "group-weekly-brief-uid-index"

	// KVBucketNameGroupWeeklyBriefThrottle is the KV bucket holding per-window
	// regeneration throttle counts. Phase 1 creates the bucket but does not
	// read or write it; Phase 2 owns the throttle logic.
	KVBucketNameGroupWeeklyBriefThrottle = "group-weekly-brief-throttle"
)
