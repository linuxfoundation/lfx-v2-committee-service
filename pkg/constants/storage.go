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

	// KVLookupMembersByCommitteePrefix is the secondary index that maps a committee UID to each
	// of its members. Key pattern: "lookup/committee-members-by-committee/<committee_uid>.<member_uid>",
	// Value: <member_uid>. The dot-separated tokens allow server-side filtered scans via
	// ListKeysFiltered with a "<committee_uid>.*" subject wildcard without fetching unrelated members.
	KVLookupMembersByCommitteePrefix = "lookup/committee-members-by-committee/%s.%s"

	// KVLookupMembersByCommitteeFilter is the ListKeysFiltered subject filter for all members of
	// one committee: "lookup/committee-members-by-committee/<committee_uid>.*"
	KVLookupMembersByCommitteeFilter = "lookup/committee-members-by-committee/%s.*"

	// KVLookupMembersByOrganizationPrefix is the secondary index that maps a holding organization
	// (the 18-char Salesforce Account SFID stored on committee_member.organization.id) to each of
	// its members. Key pattern: "lookup/committee-members-by-organization/<org_sfid>.<member_uid>",
	// Value: <member_uid>. It powers the Org Lens Board & Committee read (LFXV2-1865) so a company's
	// committee seats can be listed via a server-side filtered scan rather than a full bucket scan,
	// without any cross-service query-service call. The org SFID and member UID never contain dots,
	// so the "<org_sfid>.*" subject wildcard matches exactly one org's members.
	KVLookupMembersByOrganizationPrefix = "lookup/committee-members-by-organization/%s.%s"

	// KVLookupMembersByOrganizationFilter is the ListKeysFiltered subject filter for all members of
	// one organization: "lookup/committee-members-by-organization/<org_sfid>.*"
	KVLookupMembersByOrganizationFilter = "lookup/committee-members-by-organization/%s.*"

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
	// regeneration throttle counts. Phase 1 creates the bucket and reads it
	// best-effort (returning the throttle alongside the brief when an entry
	// exists) but never writes it; Phase 2 owns the throttle write/update logic.
	KVBucketNameGroupWeeklyBriefThrottle = "group-weekly-brief-throttle"
)
