// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

// CommitteeRemovedChildren lists what a cascading committee delete removed along with the committee, so
// the caller can clean up what lives outside the store: the search index entries, the access tuples and,
// for documents, the stored files. Applications come back whole because their index delete message
// carries their tags; every other child comes back as its uid.
type CommitteeRemovedChildren struct {
	MemberUIDs   []string
	InviteUIDs   []string
	LinkUIDs     []string
	FolderUIDs   []string
	DocumentUIDs []string
	BriefUIDs    []string
	Applications []*CommitteeApplication
}
