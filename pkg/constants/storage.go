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

	// KVLookupApplicationPrefix is the prefix for application lookup keys in the KV store.
	KVLookupApplicationPrefix = "lookup/committee-applications/%s"

	// KVLookupSSOGroupNamePrefix is the prefix for SSO group name lookup keys in the KV store.
	KVLookupSSOGroupNamePrefix = "lookup/committee-sso-groups/%s"

	KVSlugPrefix = "slug/"

	// KVBucketNameCommitteeLinks is the name of the KV bucket for committee links.
	KVBucketNameCommitteeLinks = "committee-links"

	// KVBucketNameCommitteeFolders is the name of the KV bucket for committee folders.
	KVBucketNameCommitteeFolders = "committee-folders"

	// KVLookupLinkPrefix is the prefix for link lookup keys in the KV store.
	KVLookupLinkPrefix = "lookup/committee-links/%s"

	// KVLookupFolderPrefix is the prefix for folder lookup keys in the KV store.
	KVLookupFolderPrefix = "lookup/committee-folders/%s"
)
