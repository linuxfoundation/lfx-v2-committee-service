// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// boardCategory is the CommitteeBase.Category value identifying a Board committee.
// Compared case-insensitively since category is a free-form string, not an enum
// (cmd/committee-api/design/type.go has no committee-category enum).
const boardCategory = "board"

// membershipComparisonUnavailable explains why this report cannot flag seats whose
// b2b_org no longer holds an active membership: lfx-v2-member-service has no API to
// list memberships by project/org (PCC-1610). Remove this note and wire the real
// comparison once that endpoint exists.
const membershipComparisonUnavailable = "unavailable: lfx-v2-member-service has no list-memberships-by-project/org endpoint yet (PCC-1610)"

// boardSeatsSubcommand reports every current seat on every Board-category committee,
// organized by org, with each seat's org-editability. It does not write anything —
// see PCC-1522. The companion check ("is the seat's org still an active member?")
// is stubbed pending PCC-1610 and reported as unavailable rather than silently
// omitted or guessed at.
type boardSeatsSubcommand struct{}

func (s *boardSeatsSubcommand) Name() string { return "board-seats" }

func (s *boardSeatsSubcommand) Help() string {
	return "report every seat on every Board-category committee, grouped by org (read-only; PCC-1522)"
}

// seatReport is one Board committee seat in the output.
type seatReport struct {
	MemberUID       string `json:"member_uid"`
	Username        string `json:"username,omitempty"`
	Email           string `json:"email,omitempty"`
	RoleName        string `json:"role_name,omitempty"`
	AppointedBy     string `json:"appointed_by,omitempty"`
	IsOrgEditable   bool   `json:"is_org_editable"`
	CommitteeUID    string `json:"committee_uid"`
	CommitteeName   string `json:"committee_name,omitempty"`
	ProjectUID      string `json:"project_uid,omitempty"`
	ProjectSlug     string `json:"project_slug,omitempty"`
	MembershipCheck string `json:"membership_check"`
}

// orgReport groups the seats held by a single org across Board committees.
type orgReport struct {
	OrgID      string       `json:"org_id,omitempty"`
	OrgName    string       `json:"org_name"`
	SeatsTotal int          `json:"seats_total"`
	Seats      []seatReport `json:"seats"`
}

// boardSeatsReport is the full report payload.
type boardSeatsReport struct {
	GeneratedBy          string      `json:"generated_by"`
	MembershipComparison string      `json:"membership_comparison"`
	OrgsTotal            int         `json:"orgs_total"`
	SeatsTotal           int         `json:"seats_total"`
	Orgs                 []orgReport `json:"orgs"`
}

func (s *boardSeatsSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet("board-seats", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli report board-seats [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	projectUID := fs.String("project-uid", "", "limit the report to Board committees belonging to this project UID")
	committeeUID := fs.String("committee-uid", "", "limit the report to a single committee UID (must be Board category)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if rc.CommitteeReader == nil {
		return fmt.Errorf("CommitteeReader is not wired in RunContext")
	}

	orgs := make(map[string]*orgReport)
	seatsTotal := 0

	errEach := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		if !strings.EqualFold(member.CommitteeCategory, boardCategory) {
			return nil
		}
		if *committeeUID != "" && member.CommitteeUID != *committeeUID {
			return nil
		}
		if *projectUID != "" && member.ProjectUID != *projectUID {
			return nil
		}

		orgKey := orgReportKey(member)
		org, ok := orgs[orgKey]
		if !ok {
			org = &orgReport{OrgID: member.Organization.ID, OrgName: member.Organization.Name}
			orgs[orgKey] = org
		}

		org.Seats = append(org.Seats, seatReport{
			MemberUID:       member.UID,
			Username:        member.Username,
			Email:           member.Email,
			RoleName:        member.Role.Name,
			AppointedBy:     member.AppointedBy,
			IsOrgEditable:   isMembershipEntitlement(member.AppointedBy),
			CommitteeUID:    member.CommitteeUID,
			CommitteeName:   member.CommitteeName,
			ProjectUID:      member.ProjectUID,
			ProjectSlug:     member.ProjectSlug,
			MembershipCheck: membershipComparisonUnavailable,
		})
		org.SeatsTotal++
		seatsTotal++
		return nil
	})
	if errEach != nil {
		return fmt.Errorf("failed to stream members: %w", errEach)
	}

	report := boardSeatsReport{
		GeneratedBy:          "committee-cli report board-seats",
		MembershipComparison: membershipComparisonUnavailable,
		OrgsTotal:            len(orgs),
		SeatsTotal:           seatsTotal,
		Orgs:                 sortedOrgs(orgs),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// orgReportKey groups seats by org ID when present, falling back to the (lowercased) org
// name for members whose org has no ID — this matches the only identity the data gives us.
func orgReportKey(member *model.CommitteeMember) string {
	if member.Organization.ID != "" {
		return "id:" + member.Organization.ID
	}
	return "name:" + strings.ToLower(strings.TrimSpace(member.Organization.Name))
}

// sortedOrgs returns the orgs sorted by name (then ID) for a stable, diffable report output,
// with each org's seats sorted by committee name then member UID.
func sortedOrgs(orgs map[string]*orgReport) []orgReport {
	result := make([]orgReport, 0, len(orgs))
	for _, org := range orgs {
		seats := org.Seats
		sort.Slice(seats, func(i, j int) bool {
			if seats[i].CommitteeName != seats[j].CommitteeName {
				return seats[i].CommitteeName < seats[j].CommitteeName
			}
			return seats[i].MemberUID < seats[j].MemberUID
		})
		result = append(result, *org)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].OrgName != result[j].OrgName {
			return result[i].OrgName < result[j].OrgName
		}
		return result[i].OrgID < result[j].OrgID
	})
	return result
}

// isMembershipEntitlement reports whether a seat is org-reassignable — appointment type
// "Membership Entitlement" (case-insensitive). Mirrors
// cmd/committee-api/service/committee_service.go's isMembershipEntitlement; duplicated
// here rather than exported across the API/CLI boundary since it is a one-line,
// stable derivation rule on a plain string field.
func isMembershipEntitlement(appointedBy string) bool {
	return strings.EqualFold(strings.TrimSpace(appointedBy), "Membership Entitlement")
}
