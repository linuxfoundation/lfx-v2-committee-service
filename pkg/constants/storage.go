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

	// KVLookupPrefix is the prefix for lookup keys in the KV store.
	KVLookupPrefix = "lookup/committees/%s"

	// KVLookupSSOGroupNamePrefix is the prefix for SSO group name lookup keys in the KV store.
	KVLookupSSOGroupNamePrefix = "lookup/committee-sso-groups/%s"

	KVSlugPrefix = "slug/"
)
