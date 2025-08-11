// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

const (
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

	// UpdateAccessCommitteeSubject is the subject for the committee access control updates.
	// The subject is of the form: lfx.update_access.committee
	UpdateAccessCommitteeSubject = "lfx.update_access.committee"

	// DeleteAllAccessCommitteeSubject is the  subject for the committee access control deletion.
	// The subject is of the form: lfx.delete_all_access.committee
	DeleteAllAccessCommitteeSubject = "lfx.delete_all_access.committee"
)
