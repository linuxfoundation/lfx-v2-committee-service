// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// inactiveStatus is the model.CommitteeMemberBase.Status value (see StatusAttribute in
// cmd/committee-api/design/type.go) marking a member as no longer a current seat holder.
const inactiveStatus = "Inactive"

// boardCategory is the value of the member's denormalized committee_category field
// (model.CommitteeMemberBase.CommitteeCategory) identifying a Board committee. Compared
// case-insensitively because that field is validated only by CommitteeCategoryMemberAttribute's
// MaxLength in cmd/committee-api/design/type.go (unlike the committee-side CategoryAttribute,
// which does enforce a dsl.Enum("Board", ...)) — the member copy is a denormalized snapshot that
// can drift from the committee's actual category (see model.CommitteeMember.NeedsSyncWith).
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
	Email           string `json:"email,omitempty"` // redacted via redaction.RedactEmail; see populate below
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
	output := fs.String("output", "", "write the JSON report to this file instead of stdout (recommended: stdout is shared with the structured log stream, so a run with any warnings interleaves log lines with the report)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if rc.CommitteeReader == nil {
		return fmt.Errorf("CommitteeReader is not wired in RunContext")
	}

	infoCache := make(map[string]committeeInfo)

	if *committeeUID != "" {
		base, _, err := rc.CommitteeReader.GetBase(ctx, *committeeUID)
		if err != nil {
			return fmt.Errorf("failed to look up --committee-uid %q: %w", *committeeUID, err)
		}
		if !strings.EqualFold(base.Category, boardCategory) {
			return fmt.Errorf("--committee-uid %q is not a Board committee (category: %q)", *committeeUID, base.Category)
		}
		// Seed the cache so EachMemberStrict below reuses this lookup instead of re-fetching it.
		infoCache[*committeeUID] = committeeInfo{name: base.Name, category: base.Category, projectUID: base.ProjectUID, projectSlug: base.ProjectSlug, ok: true}
	}

	orgs := make(map[string]*orgReport)
	seatsTotal := 0

	errEach := rc.CommitteeReader.EachMemberStrict(ctx, func(member *model.CommitteeMember) error {
		if *committeeUID != "" && member.CommitteeUID != *committeeUID {
			return nil
		}

		// Resolve committee_category/project_uid/project_slug/name from the parent committee (cached
		// per UID) rather than trusting the member's denormalized snapshot of each, which can be stale
		// or empty for members created before LFXV2-1442 (see model.CommitteeMember.NeedsSyncWith). A
		// failed lookup excludes the seat rather than guessing, but is logged so operators can
		// investigate. For a --committee-uid run this is a cache hit seeded above, not a new read.
		info, ok := resolveCommitteeInfo(ctx, rc, member.CommitteeUID, infoCache)
		if !ok {
			slog.WarnContext(ctx, "skipping member: could not resolve parent committee",
				"member_uid", member.UID, "committee_uid", member.CommitteeUID)
			return nil
		}
		if !strings.EqualFold(info.category, boardCategory) {
			return nil
		}
		if strings.EqualFold(strings.TrimSpace(member.Status), inactiveStatus) {
			return nil
		}
		if *projectUID != "" && info.projectUID != *projectUID {
			return nil
		}

		orgID := utils.NormalizeAccountSFID(member.Organization.ID)
		orgKey := orgReportKey(orgID, member.Organization.Name)
		org, ok := orgs[orgKey]
		if !ok {
			org = &orgReport{OrgID: orgID, OrgName: member.Organization.Name}
			orgs[orgKey] = org
		} else if name := member.Organization.Name; name != "" && (org.OrgName == "" || name < org.OrgName) {
			// EachMemberStrict scans the KV bucket in no guaranteed order. Members sharing one org
			// can carry differing Organization.Name snapshots, so picking "whichever arrives
			// first" would make the chosen name (and therefore this org's row position) vary
			// from run to run. Deterministically keep the lexicographically smallest NON-EMPTY name
			// instead, so unchanged seats never produce a noisy diff between daily runs and a stale,
			// empty Organization.Name snapshot never blanks out a known name.
			org.OrgName = name
		}

		org.Seats = append(org.Seats, seatReport{
			MemberUID:       member.UID,
			Username:        redaction.Redact(member.Username),
			Email:           redaction.RedactEmail(member.Email),
			RoleName:        member.Role.Name,
			AppointedBy:     member.AppointedBy,
			IsOrgEditable:   isMembershipEntitlement(member.AppointedBy),
			CommitteeUID:    member.CommitteeUID,
			CommitteeName:   info.name,
			ProjectUID:      info.projectUID,
			ProjectSlug:     info.projectSlug,
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

	if *output == "" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	f, err := os.Create(*output)
	if err != nil {
		return fmt.Errorf("failed to create --output file %q: %w", *output, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write --output file %q: %w", *output, err)
	}
	// A failed Close can still mean a truncated file (e.g. a flush error on a full disk) even
	// though Encode above reported success, so it must not be swallowed.
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close --output file %q: %w", *output, err)
	}
	return nil
}

// committeeInfo is the authoritative subset of a committee's attributes needed by this report,
// resolved via CommitteeReader.GetBase rather than trusting a member's denormalized snapshot of
// the same fields (model.CommitteeMember.NeedsSyncWith lists CommitteeName, CommitteeCategory,
// ProjectUID, and ProjectSlug as one denormalization family — all four can be stale or empty for
// members created before LFXV2-1442). ok is false when the lookup failed; negative results are
// cached too, since a failing committee UID would otherwise be re-queried for every one of its
// members.
type committeeInfo struct {
	name        string
	category    string
	projectUID  string
	projectSlug string
	ok          bool
}

// resolveCommitteeInfo looks up committeeUID's parent committee, caching the result (including
// failures) so a run with many members on few distinct committees issues at most one GetBase call
// per committee UID rather than one per member.
func resolveCommitteeInfo(ctx context.Context, rc commands.RunContext, committeeUID string, cache map[string]committeeInfo) (committeeInfo, bool) {
	if info, ok := cache[committeeUID]; ok {
		return info, info.ok
	}
	base, _, err := rc.CommitteeReader.GetBase(ctx, committeeUID)
	if err != nil {
		cache[committeeUID] = committeeInfo{ok: false}
		return committeeInfo{}, false
	}
	info := committeeInfo{name: base.Name, category: base.Category, projectUID: base.ProjectUID, projectSlug: base.ProjectSlug, ok: true}
	cache[committeeUID] = info
	return info, true
}

// orgReportKey groups seats by normalized org ID when present, falling back to the (lowercased)
// org name for members whose org has no ID — this matches the only identity the data gives us.
// orgID must already be normalized via utils.NormalizeAccountSFID so that equivalent 15- and
// 18-character Salesforce Account IDs for the same org land in the same group.
func orgReportKey(orgID, orgName string) string {
	if orgID != "" {
		return "id:" + orgID
	}
	return "name:" + strings.ToLower(strings.TrimSpace(orgName))
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
