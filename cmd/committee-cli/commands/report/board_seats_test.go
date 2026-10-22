// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// mockReader implements port.CommitteeReader with just EachMember exercised; every other
// method is a stub since board-seats only ever calls EachMember.
type mockReader struct {
	members    []*model.CommitteeMember
	eachErr    error
	bases      map[string]*model.CommitteeBase
	getBaseErr error
}

func (r *mockReader) EachMember(_ context.Context, fn func(*model.CommitteeMember) error) error {
	if r.eachErr != nil {
		return r.eachErr
	}
	for _, m := range r.members {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

func (r *mockReader) GetBase(_ context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	if r.getBaseErr != nil {
		return nil, 0, r.getBaseErr
	}
	base, ok := r.bases[uid]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return base, 0, nil
}
func (r *mockReader) GetRevision(_ context.Context, _ string) (uint64, error) { return 0, nil }
func (r *mockReader) ListAllUIDs(_ context.Context) ([]string, error)         { return nil, nil }
func (r *mockReader) FindUIDByProjectAndName(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not implemented for this test")
}
func (r *mockReader) GetSettings(_ context.Context, _ string) (*model.CommitteeSettings, uint64, error) {
	return nil, 0, nil
}
func (r *mockReader) GetMember(_ context.Context, _ string) (*model.CommitteeMember, uint64, error) {
	return nil, 0, errors.New("not implemented for this test")
}
func (r *mockReader) GetMemberRevision(_ context.Context, _ string) (uint64, error) { return 0, nil }
func (r *mockReader) ListMembersByCommittee(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return nil, nil
}
func (r *mockReader) ListMembersByOrganization(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return nil, nil
}
func (r *mockReader) ListAllMembers(_ context.Context) ([]*model.CommitteeMember, error) {
	return r.members, nil
}
func (r *mockReader) ListMembersByEmail(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return nil, nil
}
func (r *mockReader) ListMembersByUsername(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return nil, nil
}
func (r *mockReader) GetInvite(_ context.Context, _ string) (*model.CommitteeInvite, uint64, error) {
	return nil, 0, nil
}
func (r *mockReader) ListInvites(_ context.Context, _ string) ([]*model.CommitteeInvite, error) {
	return nil, nil
}
func (r *mockReader) ListAllInvites(_ context.Context) ([]*model.CommitteeInvite, error) {
	return nil, nil
}
func (r *mockReader) GetApplication(_ context.Context, _ string) (*model.CommitteeApplication, uint64, error) {
	return nil, 0, nil
}
func (r *mockReader) ListApplications(_ context.Context, _ string) ([]*model.CommitteeApplication, error) {
	return nil, nil
}
func (r *mockReader) ListAllApplications(_ context.Context) ([]*model.CommitteeApplication, error) {
	return nil, nil
}

func boardMember(uid, committeeUID, committeeName, category, orgID, orgName, appointedBy string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:               uid,
		CommitteeUID:      committeeUID,
		CommitteeName:     committeeName,
		CommitteeCategory: category,
		AppointedBy:       appointedBy,
		Organization:      model.CommitteeMemberOrganization{ID: orgID, Name: orgName},
	}}
}

// runBoardSeats runs the subcommand and decodes its stdout JSON report.
func runBoardSeats(t *testing.T, rc commands.RunContext) boardSeatsReport {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	runErr := (&boardSeatsSubcommand{}).Run(context.Background(), rc)

	require.NoError(t, w.Close())
	os.Stdout = old
	require.NoError(t, runErr)

	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)

	var report boardSeatsReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	return report
}

func TestBoardSeats_FiltersToBoardCategoryOnly(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		boardMember("m2", "c2", "TAC", "Technical", "org-1", "Acme Corp", "Membership Entitlement"),
	}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Equal(t, 1, report.SeatsTotal)
	require.Len(t, report.Orgs, 1)
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_CategoryMatchIsCaseInsensitive(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "BOARD", "org-1", "Acme Corp", "Membership Entitlement"),
	}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Equal(t, 1, report.SeatsTotal)
}

func TestBoardSeats_GroupsByOrgID(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		boardMember("m2", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		boardMember("m3", "c1", "Board", "Board", "org-2", "Globex", "Membership Entitlement"),
	}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Len(t, report.Orgs, 2)
	assert.Equal(t, 3, report.SeatsTotal)

	byName := map[string]orgReport{}
	for _, o := range report.Orgs {
		byName[o.OrgName] = o
	}
	assert.Len(t, byName["Acme Corp"].Seats, 2)
	assert.Len(t, byName["Globex"].Seats, 1)
}

func TestBoardSeats_GroupsByNameWhenOrgIDMissing(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "", "No ID Org", "Membership Entitlement"),
		boardMember("m2", "c1", "Board", "Board", "", "no id org", "Membership Entitlement"),
	}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Len(t, report.Orgs, 1, "org name match must be case-insensitive when there is no org ID")
	assert.Len(t, report.Orgs[0].Seats, 2)
}

func TestBoardSeats_IsOrgEditableDerivation(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		boardMember("m2", "c1", "Board", "Board", "org-1", "Acme Corp", "  membership entitlement  "),
		boardMember("m3", "c1", "Board", "Board", "org-1", "Acme Corp", "Direct Appointment"),
		boardMember("m4", "c1", "Board", "Board", "org-1", "Acme Corp", ""),
	}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Len(t, report.Orgs, 1)
	editable := map[string]bool{}
	for _, seat := range report.Orgs[0].Seats {
		editable[seat.MemberUID] = seat.IsOrgEditable
	}
	assert.True(t, editable["m1"])
	assert.True(t, editable["m2"], "appointed_by match must be case/whitespace-insensitive")
	assert.False(t, editable["m3"])
	assert.False(t, editable["m4"])
}

func TestBoardSeats_FiltersByCommitteeUID(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board A", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
			boardMember("m2", "c2", "Board B", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r, Args: []string{"--committee-uid=c1"}})
	require.Equal(t, 1, report.SeatsTotal)
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_CommitteeUIDNotFound_ReturnsError(t *testing.T) {
	r := &mockReader{bases: map[string]*model.CommitteeBase{}}
	err := (&boardSeatsSubcommand{}).Run(context.Background(), commands.RunContext{CommitteeReader: r, Args: []string{"--committee-uid=missing"}})
	require.Error(t, err)
}

func TestBoardSeats_CommitteeUIDNotBoardCategory_ReturnsError(t *testing.T) {
	r := &mockReader{bases: map[string]*model.CommitteeBase{
		"c1": {Category: "Technical"},
	}}
	err := (&boardSeatsSubcommand{}).Run(context.Background(), commands.RunContext{CommitteeReader: r, Args: []string{"--committee-uid=c1"}})
	require.Error(t, err)
}

func TestBoardSeats_RedactsEmailAndUsername(t *testing.T) {
	m := boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	m.Username = "first-last"
	m.Email = "first.last@example.com"
	r := &mockReader{members: []*model.CommitteeMember{m}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	seat := report.Orgs[0].Seats[0]
	assert.NotEqual(t, "first-last", seat.Username)
	assert.NotEqual(t, "first.last@example.com", seat.Email)
	assert.Contains(t, seat.Email, "@example.com")
}

func TestBoardSeats_OutputFlag_WritesToFile(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
	}}
	path := t.TempDir() + "/report.json"

	err := (&boardSeatsSubcommand{}).Run(context.Background(), commands.RunContext{CommitteeReader: r, Args: []string{"--output=" + path}})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var report boardSeatsReport
	require.NoError(t, json.Unmarshal(data, &report))
	assert.Equal(t, 1, report.SeatsTotal)
}

func TestBoardSeats_FiltersByProjectUID(t *testing.T) {
	m1 := boardMember("m1", "c1", "Board A", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	m1.ProjectUID = "p1"
	m2 := boardMember("m2", "c2", "Board B", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	m2.ProjectUID = "p2"
	r := &mockReader{members: []*model.CommitteeMember{m1, m2}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r, Args: []string{"--project-uid=p1"}})
	require.Equal(t, 1, report.SeatsTotal)
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_EveryStoreCarriesMembershipComparisonStub(t *testing.T) {
	r := &mockReader{members: []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
	}}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	assert.Equal(t, membershipComparisonUnavailable, report.MembershipComparison)
	require.Len(t, report.Orgs, 1)
	require.Len(t, report.Orgs[0].Seats, 1)
	assert.Equal(t, membershipComparisonUnavailable, report.Orgs[0].Seats[0].MembershipCheck)
}

func TestBoardSeats_NoCommitteeReader_ReturnsError(t *testing.T) {
	err := (&boardSeatsSubcommand{}).Run(context.Background(), commands.RunContext{})
	require.Error(t, err)
}

func TestBoardSeats_EachMemberError_Propagates(t *testing.T) {
	r := &mockReader{eachErr: errors.New("kv scan failed")}
	err := (&boardSeatsSubcommand{}).Run(context.Background(), commands.RunContext{CommitteeReader: r})
	require.Error(t, err)
}

func TestBoardSeats_EmptyResult_NoOrgs(t *testing.T) {
	r := &mockReader{}
	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	assert.Equal(t, 0, report.SeatsTotal)
	assert.Equal(t, 0, report.OrgsTotal)
	assert.Empty(t, report.Orgs)
}
