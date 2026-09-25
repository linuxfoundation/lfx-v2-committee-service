// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

// CommitteeMailingListChangedEvent represents an event from mailing-list-api
// indicating a change in committee-related mailing list state.
// Additional fields can be added here as more committee attributes become
// driven by mailing list operations.
type CommitteeMailingListChangedEvent struct {
	CommitteeUID   string `json:"committee_uid"`
	HasMailingList bool   `json:"has_mailing_list"`
}
