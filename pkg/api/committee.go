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

// CommitteeNameToUIDRequest is the payload sent by consumers on
// constants.CommitteeNameToUIDSubject to resolve a committee's UID from its project UID + name.
type CommitteeNameToUIDRequest struct {
	// ProjectUID is the v2 UUID of the project to search within.
	ProjectUID string `json:"project_uid"`
	// Name is the committee name to look up. Matched exactly against the committee's
	// stored name (the same value enforced unique per-project by the committee create path).
	Name string `json:"name"`
}

// CommitteeNameToUIDResponse is the reply payload for CommitteeNameToUIDSubject, mirroring
// GetCommitteeProjectResponse's shape: on a match CommitteeUID is set and Error is empty; when
// no live committee matches the project UID + name pair, both are empty and this is not an
// error condition. Error is set only when the request itself could not be processed.
type CommitteeNameToUIDResponse struct {
	// CommitteeUID is the v2 UUID of the matching committee. Empty when no live committee
	// matches the given project UID + name pair.
	CommitteeUID string `json:"committee_uid,omitempty"`
	// Error describes the failure reason when the lookup itself could not be performed.
	Error string `json:"error,omitempty"`
}
