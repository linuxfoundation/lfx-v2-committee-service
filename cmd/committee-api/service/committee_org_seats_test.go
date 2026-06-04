// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const testOrgSFID = "001B000000IqhSLIAZ"

// stubOrgSeatReader is a configurable port.OrgCommitteeSeatReader for the org-seat read tests.
type stubOrgSeatReader struct {
	members     []*model.CommitteeMember
	err         error
	gotOrg      string
	gotProjects []string
}

func (s *stubOrgSeatReader) ListOrgCommitteeSeats(_ context.Context, orgSFID string, projectUIDs []string) ([]*model.CommitteeMember, error) {
	s.gotOrg = orgSFID
	s.gotProjects = projectUIDs
	return s.members, s.err
}

var _ port.OrgCommitteeSeatReader = (*stubOrgSeatReader)(nil)

// reassignReaderStub embeds the package stubCommitteeReader (whose methods panic if hit
// unexpectedly) and overrides GetMember with a configurable response for the reassign path.
type reassignReaderStub struct {
	*stubCommitteeReader
	member *model.CommitteeMember
	rev    uint64
	err    error
}

func (r *reassignReaderStub) GetMember(_ context.Context, _, _ string) (*model.CommitteeMember, uint64, error) {
	return r.member, r.rev, r.err
}

func assertGoaErrContains(t *testing.T, err error, want string) {
	t.Helper()
	require.Error(t, err)
	switch e := err.(type) {
	case *committeeservice.BadRequestError:
		assert.Contains(t, e.Message, want)
	case *committeeservice.NotFoundError:
		assert.Contains(t, e.Message, want)
	case *committeeservice.ConflictError:
		assert.Contains(t, e.Message, want)
	case *committeeservice.ForbiddenError:
		assert.Contains(t, e.Message, want)
	case *committeeservice.ServiceUnavailableError:
		assert.Contains(t, e.Message, want)
	case *committeeservice.InternalServerError:
		assert.Contains(t, e.Message, want)
	default:
		assert.Contains(t, err.Error(), want)
	}
}

func entitlementSeat() *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:               "m-1",
		CommitteeUID:      "c-1",
		CommitteeName:     "Governing Board",
		CommitteeCategory: "Board",
		FirstName:         "Ann",
		LastName:          "Lee",
		Email:             "ann@example.com",
		JobTitle:          "VP",
		LinkedInProfile:   "https://linkedin.com/in/ann",
		Role:              model.CommitteeMemberRole{Name: "Director"},
		AppointedBy:       "Membership Entitlement",
		Voting:            model.CommitteeMemberVotingInfo{Status: "Voting Rep"},
		Organization:      model.CommitteeMemberOrganization{ID: testOrgSFID, Name: "Acme"},
		Invite:            &model.InviteInfo{},
		CreatedAt:         time.Now().Add(-72 * time.Hour),
		UpdatedAt:         time.Now().Add(-48 * time.Hour),
	}}
}

func TestGetOrgCommitteeSeats(t *testing.T) {
	editable := entitlementSeat()
	nonEditable := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:               "m-2",
		CommitteeUID:      "c-1",
		CommitteeName:     "Governing Board",
		CommitteeCategory: "Board",
		FirstName:         "Bob",
		LastName:          "Kim",
		Email:             "bob@example.com",
		Role:              model.CommitteeMemberRole{Name: "Member"},
		AppointedBy:       "Foundation Election",
		Organization:      model.CommitteeMemberOrganization{ID: testOrgSFID},
	}}

	t.Run("maps members, computes editability, passes scope through", func(t *testing.T) {
		reader := &stubOrgSeatReader{members: []*model.CommitteeMember{editable, nonEditable}}
		svc := &committeeServicesrvc{orgSeatReader: reader}

		res, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{
			UID:         testOrgSFID,
			ProjectUids: []string{"p-1", "p-2"},
		})

		require.NoError(t, err)
		require.Len(t, res, 2)

		// Membership-Entitlement seat is editable with no reason.
		assert.True(t, res[0].IsOrgEditable)
		assert.Nil(t, res[0].Reason)
		assert.Equal(t, "Voting Rep", res[0].VotingStatus)
		assert.Equal(t, "Director", res[0].RoleName)
		assert.Equal(t, testOrgSFID, res[0].OrganizationID)

		// Non-entitlement seat is not editable and carries a reason.
		assert.False(t, res[1].IsOrgEditable)
		require.NotNil(t, res[1].Reason)
		assert.NotEmpty(t, *res[1].Reason)

		// Org + project family forwarded to the reader unchanged.
		assert.Equal(t, testOrgSFID, reader.gotOrg)
		assert.Equal(t, []string{"p-1", "p-2"}, reader.gotProjects)
	})

	t.Run("reader not configured returns service unavailable", func(t *testing.T) {
		svc := &committeeServicesrvc{orgSeatReader: nil}
		_, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID})
		assertGoaErrContains(t, err, "not configured")
	})

	t.Run("reader error is wrapped", func(t *testing.T) {
		reader := &stubOrgSeatReader{err: errs.NewUnexpected("boom")}
		svc := &committeeServicesrvc{orgSeatReader: reader}
		_, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID})
		require.Error(t, err)
	})
}

func TestReassignOrgCommitteeSeat(t *testing.T) {
	newHolder := func() *committeeservice.ReassignOrgCommitteeSeatPayload {
		return &committeeservice.ReassignOrgCommitteeSeatPayload{
			UID:          testOrgSFID,
			MemberUID:    "m-1",
			CommitteeUID: "c-1",
			FirstName:    "Carol",
			LastName:     "Ng",
			Email:        "carol@example.com",
		}
	}

	t.Run("happy path preserves seat fields, clears holder fields, deletes old", func(t *testing.T) {
		created := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "m-new",
			CommitteeUID: "c-1",
			FirstName:    "Carol",
			LastName:     "Ng",
			Email:        "carol@example.com",
			Role:         model.CommitteeMemberRole{Name: "Director"},
			AppointedBy:  "Membership Entitlement",
			Voting:       model.CommitteeMemberVotingInfo{Status: "Voting Rep"},
			Organization: model.CommitteeMemberOrganization{ID: testOrgSFID},
		}}
		writer := &mockCommitteeWriterOrchestrator{createMember: created}
		reader := &reassignReaderStub{stubCommitteeReader: &stubCommitteeReader{}, member: entitlementSeat(), rev: 7}
		svc := &committeeServicesrvc{committeeWriterOrchestrator: writer, committeeReaderOrchestrator: reader}

		res, err := svc.ReassignOrgCommitteeSeat(context.Background(), newHolder())
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "carol@example.com", res.Email)
		assert.True(t, res.IsOrgEditable)

		// New member created with holder identity swapped and seat fields preserved.
		require.Len(t, writer.createMemberCalls, 1)
		nm := writer.createMemberCalls[0]
		assert.Empty(t, nm.UID)
		assert.Empty(t, nm.Username)
		assert.Equal(t, "Carol", nm.FirstName)
		assert.Equal(t, "carol@example.com", nm.Email)
		// Holder-specific fields cleared (regression guard for the carry-over fix).
		assert.Empty(t, nm.JobTitle)
		assert.Empty(t, nm.LinkedInProfile)
		assert.Nil(t, nm.Invite)
		assert.True(t, nm.CreatedAt.IsZero())
		assert.True(t, nm.UpdatedAt.IsZero())
		// Seat-defining fields preserved.
		assert.Equal(t, "Director", nm.Role.Name)
		assert.Equal(t, "Voting Rep", nm.Voting.Status)
		assert.Equal(t, "Membership Entitlement", nm.AppointedBy)
		assert.Equal(t, testOrgSFID, nm.Organization.ID)

		// Old member deleted exactly once at the read revision.
		require.Len(t, writer.deleteCalls, 1)
		assert.Equal(t, "m-1", writer.deleteCalls[0].uid)
		assert.Equal(t, uint64(7), writer.deleteCalls[0].revision)
	})

	t.Run("org mismatch returns not found and never mutates", func(t *testing.T) {
		foreign := entitlementSeat()
		foreign.Organization.ID = "001XXXXXXXXXXXXXXX" // different org
		writer := &mockCommitteeWriterOrchestrator{}
		reader := &reassignReaderStub{stubCommitteeReader: &stubCommitteeReader{}, member: foreign, rev: 1}
		svc := &committeeServicesrvc{committeeWriterOrchestrator: writer, committeeReaderOrchestrator: reader}

		_, err := svc.ReassignOrgCommitteeSeat(context.Background(), newHolder())
		assertGoaErrContains(t, err, "not found")
		assert.Empty(t, writer.createMemberCalls)
		assert.Empty(t, writer.deleteCalls)
	})

	t.Run("non-entitlement seat returns forbidden and never mutates", func(t *testing.T) {
		seat := entitlementSeat()
		seat.AppointedBy = "Foundation Election"
		writer := &mockCommitteeWriterOrchestrator{}
		reader := &reassignReaderStub{stubCommitteeReader: &stubCommitteeReader{}, member: seat, rev: 1}
		svc := &committeeServicesrvc{committeeWriterOrchestrator: writer, committeeReaderOrchestrator: reader}

		_, err := svc.ReassignOrgCommitteeSeat(context.Background(), newHolder())
		assertGoaErrContains(t, err, "org-editable")
		assert.Empty(t, writer.createMemberCalls)
		assert.Empty(t, writer.deleteCalls)
	})

	t.Run("get member error is wrapped", func(t *testing.T) {
		writer := &mockCommitteeWriterOrchestrator{}
		reader := &reassignReaderStub{stubCommitteeReader: &stubCommitteeReader{}, err: errs.NewNotFound("member not found")}
		svc := &committeeServicesrvc{committeeWriterOrchestrator: writer, committeeReaderOrchestrator: reader}

		_, err := svc.ReassignOrgCommitteeSeat(context.Background(), newHolder())
		assertGoaErrContains(t, err, "member not found")
		assert.Empty(t, writer.createMemberCalls)
	})

	t.Run("delete failure attempts rollback and returns error", func(t *testing.T) {
		created := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "m-new", CommitteeUID: "c-1"}}
		writer := &mockCommitteeWriterOrchestrator{createMember: created, deleteError: errs.NewConflict("seat changed")}
		reader := &reassignReaderStub{stubCommitteeReader: &stubCommitteeReader{}, member: entitlementSeat(), rev: 7}
		svc := &committeeServicesrvc{committeeWriterOrchestrator: writer, committeeReaderOrchestrator: reader}

		_, err := svc.ReassignOrgCommitteeSeat(context.Background(), newHolder())
		assertGoaErrContains(t, err, "seat changed")

		// Created the new member, then attempted both the original delete and the rollback delete.
		require.Len(t, writer.createMemberCalls, 1)
		require.Len(t, writer.deleteCalls, 2)
		assert.Equal(t, "m-1", writer.deleteCalls[0].uid)   // old member
		assert.Equal(t, "m-new", writer.deleteCalls[1].uid) // rollback of created member
	})
}
