// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// mockReader implements port.CommitteeReader with just EachMemberStrict exercised (board-seats uses
// the strict variant so a partial scan is never mistaken for a complete one); every other method is a
// stub.
type mockReader struct {
	members    []*model.CommitteeMember
	eachErr    error
	bases      map[string]*model.CommitteeBase
	getBaseErr error
}

func (r *mockReader) EachMemberStrict(_ context.Context, fn func(*model.CommitteeMember) error) error {
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

func (r *mockReader) EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	return r.EachMemberStrict(ctx, fn)
}

func (r *mockReader) GetBase(_ context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	if r.getBaseErr != nil {
		return nil, 0, r.getBaseErr
	}
	base, ok := r.bases[uid]
	if !ok {
		return nil, 0, errs.NewNotFound(fmt.Sprintf("committee %q not found", uid))
	}
	return base, 0, nil
}
func (r *mockReader) ListAllUIDs(_ context.Context) ([]string, error) { return nil, nil }
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
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
			boardMember("m2", "c2", "TAC", "Technical", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
			"c2": {Name: "TAC", Category: "Technical"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Equal(t, 1, report.SeatsTotal)
	require.Len(t, report.Orgs, 1)
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_CategoryMatchIsCaseInsensitive(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "BOARD", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "BOARD"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Equal(t, 1, report.SeatsTotal)
}

func TestBoardSeats_GroupsByOrgID(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
			boardMember("m2", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
			boardMember("m3", "c1", "Board", "Board", "org-2", "Globex", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

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
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "", "No ID Org", "Membership Entitlement"),
			boardMember("m2", "c1", "Board", "Board", "", "no id org", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Len(t, report.Orgs, 1, "org name match must be case-insensitive when there is no org ID")
	assert.Len(t, report.Orgs[0].Seats, 2)
}

func TestBoardSeats_IsOrgEditableDerivation(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
			boardMember("m2", "c1", "Board", "Board", "org-1", "Acme Corp", "  membership entitlement  "),
			boardMember("m3", "c1", "Board", "Board", "org-1", "Acme Corp", "Direct Appointment"),
			boardMember("m4", "c1", "Board", "Board", "org-1", "Acme Corp", ""),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

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

func TestBoardSeats_CommitteeUIDScoped_CountsMemberWithStaleCategory(t *testing.T) {
	// GetBase already confirmed c1 is Board category below, so a member whose denormalized
	// CommitteeCategory snapshot is stale/empty (pre-LFXV2-1442 data) must still be counted — the
	// per-member category check only applies to unscoped runs.
	stale := boardMember("m1", "c1", "Board A", "", "org-1", "Acme Corp", "Membership Entitlement")
	r := &mockReader{
		members: []*model.CommitteeMember{stale},
		bases: map[string]*model.CommitteeBase{
			"c1": {Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r, Args: []string{"--committee-uid=c1"}})
	require.Equal(t, 1, report.SeatsTotal, "a stale/empty member.CommitteeCategory must not drop a seat once --committee-uid has confirmed the committee is Board")
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_UnscopedRun_ResolvesCategoryFromParentCommittee(t *testing.T) {
	// member.CommitteeCategory is stale/empty (pre-LFXV2-1442 data), but the parent committee (looked
	// up via GetBase, cached per UID) is authoritatively Board — the seat must still be counted on an
	// unscoped run, not just a --committee-uid-scoped one.
	stale := boardMember("m1", "c1", "Board", "", "org-1", "Acme Corp", "Membership Entitlement")
	r := &mockReader{
		members: []*model.CommitteeMember{stale},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Equal(t, 1, report.SeatsTotal, "a stale/empty member.CommitteeCategory must not drop a seat when the parent committee resolves to Board")
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_UnscopedRun_SkipsMemberWhenParentCommitteeLookupFails(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	assert.Equal(t, 0, report.SeatsTotal, "a seat whose parent committee cannot be resolved must be excluded rather than guessed at")
}

func TestBoardSeats_UnscopedRun_AbortsOnTransientParentCommitteeLookupFailure(t *testing.T) {
	// Unlike a genuine errs.NotFound (the committee no longer exists, safe to skip and cache), a
	// transient GetBase failure must abort the whole run rather than silently drop the committee's
	// seats and still exit 0 — that would defeat the "strict scan" guarantee EachMemberStrict exists
	// to provide.
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		getBaseErr: errs.NewUnexpected("nats read timeout"),
	}

	err := (&boardSeatsSubcommand{}).Run(context.Background(), commands.RunContext{CommitteeReader: r})
	require.Error(t, err, "a non-NotFound GetBase failure must fail the run, not silently produce an incomplete report")
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
	r := &mockReader{
		members: []*model.CommitteeMember{m},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	seat := report.Orgs[0].Seats[0]
	assert.NotEqual(t, "first-last", seat.Username)
	assert.NotEqual(t, "first.last@example.com", seat.Email)
	assert.Contains(t, seat.Email, "@example.com")
}

func TestBoardSeats_OutputFlag_WritesToFile(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}
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
	m2 := boardMember("m2", "c2", "Board B", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	// project_uid is resolved authoritatively from the parent committee (same denormalization family
	// as committee_category), not trusted from the member's own (possibly stale) snapshot.
	r := &mockReader{
		members: []*model.CommitteeMember{m1, m2},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board A", Category: "Board", ProjectUID: "p1"},
			"c2": {Name: "Board B", Category: "Board", ProjectUID: "p2"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r, Args: []string{"--project-uid=p1"}})
	require.Equal(t, 1, report.SeatsTotal)
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
}

func TestBoardSeats_EverySeatCarriesMembershipComparisonStub(t *testing.T) {
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	assert.Equal(t, membershipComparisonUnavailable, report.MembershipComparison)
	require.Len(t, report.Orgs, 1)
	require.Len(t, report.Orgs[0].Seats, 1)
	assert.Equal(t, membershipComparisonUnavailable, report.Orgs[0].Seats[0].MembershipCheck)
}

func TestBoardSeats_GroupsEquivalentSFIDForms(t *testing.T) {
	// A 15-char Salesforce Account ID and its 18-char canonical form (computed via the same
	// NormalizeAccountSFID the report uses, mirroring utils.TestNormalizeAccountSFID_15and18Converge)
	// are equivalent forms for the same org; the report should collapse them to one group instead of
	// splitting one org's seats across two rows.
	fifteen := "001Rm000003oVr9"
	eighteen := utils.NormalizeAccountSFID(fifteen)
	r := &mockReader{
		members: []*model.CommitteeMember{
			boardMember("m1", "c1", "Board", "Board", eighteen, "Acme Corp", "Membership Entitlement"),
			boardMember("m2", "c1", "Board", "Board", fifteen, "Acme Corp", "Membership Entitlement"),
		},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Len(t, report.Orgs, 1, "equivalent 15- and 18-char Salesforce Account IDs must collapse to one org")
	assert.Len(t, report.Orgs[0].Seats, 2)
}

func TestBoardSeats_OrgNamePickIsDeterministicRegardlessOfScanOrder(t *testing.T) {
	membersAZ := []*model.CommitteeMember{
		boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement"),
		boardMember("m2", "c1", "Board", "Board", "org-1", "Acme Corporation", "Membership Entitlement"),
	}
	membersZA := []*model.CommitteeMember{membersAZ[1], membersAZ[0]}
	bases := map[string]*model.CommitteeBase{"c1": {Name: "Board", Category: "Board"}}

	reportAZ := runBoardSeats(t, commands.RunContext{CommitteeReader: &mockReader{members: membersAZ, bases: bases}})
	reportZA := runBoardSeats(t, commands.RunContext{CommitteeReader: &mockReader{members: membersZA, bases: bases}})

	require.Len(t, reportAZ.Orgs, 1)
	require.Len(t, reportZA.Orgs, 1)
	assert.Equal(t, "Acme Corp", reportAZ.Orgs[0].OrgName, "lexicographically smallest name wins regardless of scan order")
	assert.Equal(t, reportAZ.Orgs[0].OrgName, reportZA.Orgs[0].OrgName)
}

func TestBoardSeats_OrgNamePickIgnoresEmptyNameRegardlessOfScanOrder(t *testing.T) {
	// A stale/empty Organization.Name snapshot must never blank out a known good name, regardless of
	// which member the scan (which has no guaranteed order) happens to visit first.
	named := boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	empty := boardMember("m2", "c1", "Board", "Board", "org-1", "", "Membership Entitlement")
	bases := map[string]*model.CommitteeBase{"c1": {Name: "Board", Category: "Board"}}

	reportNamedFirst := runBoardSeats(t, commands.RunContext{CommitteeReader: &mockReader{members: []*model.CommitteeMember{named, empty}, bases: bases}})
	reportEmptyFirst := runBoardSeats(t, commands.RunContext{CommitteeReader: &mockReader{members: []*model.CommitteeMember{empty, named}, bases: bases}})

	require.Len(t, reportNamedFirst.Orgs, 1)
	require.Len(t, reportEmptyFirst.Orgs, 1)
	assert.Equal(t, "Acme Corp", reportNamedFirst.Orgs[0].OrgName)
	assert.Equal(t, "Acme Corp", reportEmptyFirst.Orgs[0].OrgName, "an empty Organization.Name snapshot must not overwrite a known name regardless of scan order")
}

func TestBoardSeats_ExcludesInactiveMembers(t *testing.T) {
	active := boardMember("m1", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	inactive := boardMember("m2", "c1", "Board", "Board", "org-1", "Acme Corp", "Membership Entitlement")
	inactive.Status = "Inactive"
	r := &mockReader{
		members: []*model.CommitteeMember{active, inactive},
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "Board", Category: "Board"},
		},
	}

	report := runBoardSeats(t, commands.RunContext{CommitteeReader: r})
	require.Equal(t, 1, report.SeatsTotal)
	assert.Equal(t, "m1", report.Orgs[0].Seats[0].MemberUID)
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
