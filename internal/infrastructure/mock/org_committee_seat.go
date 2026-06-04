// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// MockOrgCommitteeSeatReader returns reshaped dev (AGL) committee members for the Org Lens Board &
// Committee tab when REPOSITORY_SOURCE=mock (local dev without query-service / M2M). The holding
// organization is stamped to the requested SFID so the seats match whatever org the BFF asks for.
type MockOrgCommitteeSeatReader struct{}

// NewMockOrgCommitteeSeatReader constructs the mock org-committee-seat reader.
func NewMockOrgCommitteeSeatReader() port.OrgCommitteeSeatReader {
	return &MockOrgCommitteeSeatReader{}
}

// ListOrgCommitteeSeats returns the dev AGL seats with organization stamped to orgSFID. projectUIDs
// is accepted but not filtered on in the mock (the live source scopes by project_uid).
func (m *MockOrgCommitteeSeatReader) ListOrgCommitteeSeats(_ context.Context, orgSFID string, _ []string) ([]*model.CommitteeMember, error) {
	org := model.CommitteeMemberOrganization{ID: orgSFID, Name: "Example Corp"}
	return []*model.CommitteeMember{
		{CommitteeMemberBase: model.CommitteeMemberBase{
			UID: "11111111-1111-4111-8111-000000000001", CommitteeUID: "aaaaaaaa-0000-4000-8000-00000000b0a4",
			CommitteeName: "Governing Board", CommitteeCategory: "Board",
			FirstName: "Alex", LastName: "Rivera", Email: "alex.rivera@example.com", JobTitle: "Principal Engineer",
			Role: model.CommitteeMemberRole{Name: "Director"}, AppointedBy: "Membership Entitlement",
			Voting: model.CommitteeMemberVotingInfo{Status: "Voting Rep"}, Organization: org,
		}},
		{CommitteeMemberBase: model.CommitteeMemberBase{
			UID: "11111111-1111-4111-8111-000000000011", CommitteeUID: "aaaaaaaa-0000-4000-8000-00000000c001",
			CommitteeName: "Technical Steering Committee", CommitteeCategory: "Technical Steering Committee",
			FirstName: "Alex", LastName: "Rivera", Email: "alex.rivera@example.com", JobTitle: "Principal Engineer",
			Role: model.CommitteeMemberRole{Name: "Chair"}, AppointedBy: "Vote of TSC Committee",
			Voting: model.CommitteeMemberVotingInfo{Status: "Voting Rep"}, Organization: org,
		}},
		{CommitteeMemberBase: model.CommitteeMemberBase{
			UID: "11111111-1111-4111-8111-000000000012", CommitteeUID: "aaaaaaaa-0000-4000-8000-00000000c002",
			CommitteeName: "Marketing Committee", CommitteeCategory: "Marketing Committee/Sub Committee",
			FirstName: "Jordan", LastName: "Kim", Email: "jordan.kim@example.com", JobTitle: "Engineer",
			Role: model.CommitteeMemberRole{Name: "None"}, AppointedBy: "Membership Entitlement",
			Voting: model.CommitteeMemberVotingInfo{Status: "Voting Rep"}, Organization: org,
		}},
		{CommitteeMemberBase: model.CommitteeMemberBase{
			UID: "11111111-1111-4111-8111-000000000014", CommitteeUID: "aaaaaaaa-0000-4000-8000-00000000c004",
			CommitteeName: "Steering Committee", CommitteeCategory: "Working Group",
			FirstName: "Taylor", LastName: "Morgan", Email: "taylor.morgan@example.com", JobTitle: "Distinguished Engineer",
			Role: model.CommitteeMemberRole{Name: "None"}, AppointedBy: "Membership Entitlement",
			Voting: model.CommitteeMemberVotingInfo{Status: "Observer"}, Organization: org,
		}},
	}, nil
}
