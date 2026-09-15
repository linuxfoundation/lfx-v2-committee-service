// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package api contains the public contract types that other LFX services use to
// interact with the committee service. These are the only exported types intended
// for inter-service use; all other types remain internal.
//
// NATS subject constants live in pkg/constants (e.g. constants.CommitteeGetProjectSubject)
// per repo convention. Import both packages when making requests.
package api

// GetCommitteeProjectRequest is the payload sent by consumers on constants.CommitteeGetProjectSubject.
type GetCommitteeProjectRequest struct {
	// CommitteeUID is the v2 UUID of the committee whose owning project is being queried.
	CommitteeUID string `json:"committee_uid"`
}

// GetCommitteeProjectResponse is the reply payload for GetCommitteeProjectSubject.
// On success ProjectUID is set and Error is empty.
// On failure (e.g. not found) only Error is set.
type GetCommitteeProjectResponse struct {
	// ProjectUID is the v2 UUID of the project that owns the committee.
	ProjectUID string `json:"project_uid,omitempty"`
	// Error describes the failure reason when the lookup was unsuccessful.
	Error string `json:"error,omitempty"`
}

// CommitteeExistsRequest is the payload sent by consumers on constants.CommitteeExistsSubject
// to check whether a live committee with the given name already exists within a project.
type CommitteeExistsRequest struct {
	// ProjectUID is the v2 UUID of the project to search within.
	ProjectUID string `json:"project_uid"`
	// Name is the committee name to look up. Matched exactly against the committee's
	// stored name (the same value enforced unique per-project by the committee create path).
	Name string `json:"name"`
}

// CommitteeExistsResponse is the reply payload for CommitteeExistsSubject.
// On a match, Exists is true and CommitteeUID is set. When no live committee matches,
// Exists is false and CommitteeUID is empty; this is not an error condition.
// Error is set only when the request itself could not be processed (e.g. invalid payload).
type CommitteeExistsResponse struct {
	// Exists indicates whether a live committee with the given project UID + name was found.
	Exists bool `json:"exists"`
	// CommitteeUID is the v2 UUID of the matching committee. Set only when Exists is true.
	CommitteeUID string `json:"committee_uid,omitempty"`
	// Error describes the failure reason when the lookup itself could not be performed.
	Error string `json:"error,omitempty"`
}
