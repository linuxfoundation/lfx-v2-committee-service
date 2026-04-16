// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

const (
	// RelationParent is the relation name for the parent of an object.
	RelationParent = "parent"
	// RelationProject is the relation name for the project of an object.
	RelationProject = "project"
	// RelationWriter is the relation name for the writer of an object.
	RelationWriter = "writer"
	// RelationAuditor is the relation name for the auditor of an object.
	RelationAuditor = "auditor"
	// RelationMember is the relation name for a member of an object.
	RelationMember = "member"
	// RelationRosterViewer is the relation for viewing committee member names & roles.
	RelationRosterViewer = "roster_viewer"
	// RelationEmailViewer is the relation for viewing committee member email addresses.
	RelationEmailViewer = "email_viewer"
	// RelationCommitteeForMemberRosterAccess is the self-referential relation that enables
	// member access to the roster and emails when set to the committee itself.
	RelationCommitteeForMemberRosterAccess = "committee_for_member_roster_access"

	// MemberVisibilityBasicProfile is the member_visibility value that enables members
	// to see each other's names, roles, and emails.
	MemberVisibilityBasicProfile = "basic_profile"
	// MemberVisibilityHidden is the member_visibility value that hides member profiles
	// from other members (default).
	MemberVisibilityHidden = "hidden"
)
