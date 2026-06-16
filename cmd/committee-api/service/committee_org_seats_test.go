// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
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

// TestMain provisions a deterministic org-seat page-token signing key for the test binary. Production
// sources this only from ORG_SEAT_PAGE_TOKEN_HMAC_KEY (there is no in-repo fallback), so tests set it
// explicitly to exercise the real signed-cursor pagination paths.
func TestMain(m *testing.M) {
	seatCursorKey = []byte("test-org-seat-cursor-signing-key")
	os.Exit(m.Run())
}

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
		ProjectUID:        "11111111-1111-1111-1111-111111111111",
		ProjectSlug:       "test-project",
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
		Role:              model.CommitteeMemberRole{Name: "Lead"},
		AppointedBy:       "Community",
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
		require.NotNil(t, res)
		require.Len(t, res.Seats, 2)
		assert.Nil(t, res.PageToken, "single page → no next cursor")

		// Seats are sorted by UID: m-1 (editable) then m-2 (non-editable).
		assert.True(t, res.Seats[0].IsOrgEditable)
		assert.Nil(t, res.Seats[0].Reason)
		assert.Equal(t, "Voting Rep", res.Seats[0].VotingStatus)
		assert.Equal(t, "Director", res.Seats[0].RoleName)
		assert.Equal(t, testOrgSFID, res.Seats[0].OrganizationID)
		// Foundation (project) tags are surfaced per seat.
		require.NotNil(t, res.Seats[0].ProjectUID)
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", *res.Seats[0].ProjectUID)
		require.NotNil(t, res.Seats[0].ProjectSlug)
		assert.Equal(t, "test-project", *res.Seats[0].ProjectSlug)

		// Non-entitlement seat carries no project tags (fixture leaves them empty) → nil pointers.
		assert.Nil(t, res.Seats[1].ProjectUID)
		assert.Nil(t, res.Seats[1].ProjectSlug)

		// Non-entitlement seat is not editable and carries a reason.
		assert.False(t, res.Seats[1].IsOrgEditable)
		require.NotNil(t, res.Seats[1].Reason)
		assert.NotEmpty(t, *res.Seats[1].Reason)

		// Org + project family forwarded to the reader unchanged.
		assert.Equal(t, testOrgSFID, reader.gotOrg)
		assert.Equal(t, []string{"p-1", "p-2"}, reader.gotProjects)
	})

	t.Run("paginates with page_size and an opaque page_token", func(t *testing.T) {
		// 5 entitlement seats with sortable UIDs m-1..m-5.
		var seats []*model.CommitteeMember
		for _, id := range []string{"m-3", "m-1", "m-5", "m-2", "m-4"} {
			s := entitlementSeat()
			s.UID = id
			seats = append(seats, s)
		}
		reader := &stubOrgSeatReader{members: seats}
		svc := &committeeServicesrvc{orgSeatReader: reader}
		size := 2

		// Page 1.
		p1, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID, PageSize: &size})
		require.NoError(t, err)
		require.Len(t, p1.Seats, 2)
		require.NotNil(t, p1.PageToken)
		assert.Equal(t, "m-1", p1.Seats[0].UID)
		assert.Equal(t, "m-2", p1.Seats[1].UID)

		// Page 2 via the returned cursor.
		p2, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID, PageSize: &size, PageToken: p1.PageToken})
		require.NoError(t, err)
		require.Len(t, p2.Seats, 2)
		require.NotNil(t, p2.PageToken)
		assert.Equal(t, "m-3", p2.Seats[0].UID)
		assert.Equal(t, "m-4", p2.Seats[1].UID)

		// Page 3 — last seat, no further cursor.
		p3, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID, PageSize: &size, PageToken: p2.PageToken})
		require.NoError(t, err)
		require.Len(t, p3.Seats, 1)
		assert.Nil(t, p3.PageToken)
		assert.Equal(t, "m-5", p3.Seats[0].UID)
	})

	t.Run("page size is capped at the maximum", func(t *testing.T) {
		// More members than the cap so a requested page_size above maxOrgSeatPageSize is clamped.
		seats := make([]*model.CommitteeMember, 0, maxOrgSeatPageSize+1)
		for i := 0; i <= maxOrgSeatPageSize; i++ { // maxOrgSeatPageSize+1 members
			s := entitlementSeat()
			s.UID = fmt.Sprintf("m-%05d", i)
			seats = append(seats, s)
		}
		reader := &stubOrgSeatReader{members: seats}
		svc := &committeeServicesrvc{orgSeatReader: reader}

		over := maxOrgSeatPageSize + 1000 // request well above the cap
		p, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID, PageSize: &over})
		require.NoError(t, err)
		assert.Len(t, p.Seats, maxOrgSeatPageSize, "page must be clamped to maxOrgSeatPageSize")
		assert.NotNil(t, p.PageToken, "an extra member remains beyond the capped page → next cursor expected")
	})

	t.Run("malformed page_token is a bad request", func(t *testing.T) {
		reader := &stubOrgSeatReader{members: []*model.CommitteeMember{entitlementSeat()}}
		svc := &committeeServicesrvc{orgSeatReader: reader}
		bad := "!!!not-base64!!!"
		_, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID, PageToken: &bad})
		assertGoaErrContains(t, err, "page_token")
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

	t.Run("missing signing key degrades to service unavailable", func(t *testing.T) {
		// ORG_SEAT_PAGE_TOKEN_HMAC_KEY unset → empty key → the endpoint returns 503 (graceful
		// degradation) instead of crashing the pod or signing forgeable tokens.
		saved := seatCursorKey
		seatCursorKey = nil
		t.Cleanup(func() { seatCursorKey = saved })

		reader := &stubOrgSeatReader{members: []*model.CommitteeMember{entitlementSeat()}}
		svc := &committeeServicesrvc{orgSeatReader: reader}
		_, err := svc.GetOrgCommitteeSeats(context.Background(), &committeeservice.GetOrgCommitteeSeatsPayload{UID: testOrgSFID})
		assertGoaErrContains(t, err, "ORG_SEAT_PAGE_TOKEN_HMAC_KEY")
	})
}

func TestDecodeSeatCursor(t *testing.T) {
	t.Run("round-trips a signed cursor", func(t *testing.T) {
		tok := encodeSeatCursor("m-42")
		got, err := decodeSeatCursor(&tok)
		require.NoError(t, err)
		assert.Equal(t, "m-42", got)
	})

	t.Run("empty/nil token is the first page", func(t *testing.T) {
		got, err := decodeSeatCursor(nil)
		require.NoError(t, err)
		assert.Empty(t, got)

		empty := ""
		got, err = decodeSeatCursor(&empty)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("tampered signature is rejected", func(t *testing.T) {
		// Take a structurally valid, correctly-signed cursor and flip a signature byte so the token is
		// still valid base64 of the right length but the HMAC no longer matches — this exercises the
		// !hmac.Equal branch (distinct from the malformed-base64 case).
		tok := encodeSeatCursor("m-1")
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		require.NoError(t, err)
		require.Greater(t, len(raw), sha256.Size)
		raw[0] ^= 0xFF // corrupt the first signature byte
		tampered := base64.RawURLEncoding.EncodeToString(raw)

		_, err = decodeSeatCursor(&tampered)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature")
	})

	t.Run("signature from a different key is rejected", func(t *testing.T) {
		// A token signed with a different key must not verify under seatCursorKey.
		saved := seatCursorKey
		seatCursorKey = []byte("a-totally-different-key")
		foreign := encodeSeatCursor("m-1")
		seatCursorKey = saved

		_, err := decodeSeatCursor(&foreign)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature")
	})
}

func TestIsMembershipEntitlement(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Membership Entitlement", true},
		{"membership entitlement", true},        // case-insensitive
		{"MEMBERSHIP ENTITLEMENT", true},        // case-insensitive
		{"  Membership Entitlement  ", true},    // surrounding whitespace trimmed
		{"\tMembership Entitlement\n", true},    // other whitespace trimmed
		{"", false},                             // empty
		{"   ", false},                          // whitespace-only
		{"Community", false},                    // other appointment type
		{"Membership", false},                   // partial match must not pass
		{"Membership Entitlement Extra", false}, // superset must not pass
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isMembershipEntitlement(c.in), "input=%q", c.in)
	}
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
		assert.True(t, nm.CreatedAt.IsZero())
		assert.True(t, nm.UpdatedAt.IsZero())
		// Seat-defining fields preserved.
		assert.Equal(t, "Director", nm.Role.Name)
		assert.Equal(t, "Voting Rep", nm.Voting.Status)
		assert.Equal(t, "Membership Entitlement", nm.AppointedBy)
		assert.Equal(t, testOrgSFID, nm.Organization.ID)
		// The seat's foundation (project) tags must survive the reassign allowlist copy.
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", nm.ProjectUID)
		assert.Equal(t, "test-project", nm.ProjectSlug)

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
		seat.AppointedBy = "Community"
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
