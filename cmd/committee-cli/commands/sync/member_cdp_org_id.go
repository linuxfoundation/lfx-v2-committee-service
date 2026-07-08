// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/m2m"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/orgid"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

type memberCDPOrgIDStats struct {
	commands.Stats
	CDPUUIDFound int
	Resolved     int
	Cleared      int
	Unresolved   int
}

type memberCDPOrgIDSubcommand struct{}

func (s *memberCDPOrgIDSubcommand) Name() string { return "member-cdp-org-id" }

func (s *memberCDPOrgIDSubcommand) Help() string {
	return "repair committee members storing a CDP org UUID in organization.id by resolving the b2b_org Salesforce SFID (LFXV2-2647)"
}

func (s *memberCDPOrgIDSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("member-cdp-org-id", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync member-cdp-org-id [flags]\n\nflags:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "environment:")
		_, _ = fmt.Fprintln(fs.Output(), "  QUERY_SERVICE_URL  query-service base URL (required for SFID resolution)")
		_, _ = fmt.Fprintln(fs.Output(), "  AUTH_TOKEN         bearer token for query-service (required unless M2M is wired)")
	}
	committeeUID := fs.String("committee-uid", "", "limit repair to members of a single committee UID")
	memberUID := fs.String("member-uid", "", "limit repair to a single committee member UID")
	sleep := fs.Duration("sleep", 0, "wait between each member write (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", true, "compute what would be written without writing (pass --dry-run=false to write)")
	clearUnresolved := fs.Bool("clear-unresolved", false, "when SFID cannot be resolved, clear organization.id (keep name/website)")
	queryURL := fs.String("query-service-url", strings.TrimSpace(os.Getenv("QUERY_SERVICE_URL")), "override QUERY_SERVICE_URL")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if rc.CommitteeReader == nil {
		return errors.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeWriterOrchestrator == nil {
		return errors.NewUnexpected("CommitteeWriterOrchestrator is not wired in RunContext")
	}

	rc.DryRun = *dryRun
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	resolver := rc.B2BOrgSFIDResolver
	if resolver == nil {
		resolver = m2m.NewB2BOrgResolver(m2m.B2BOrgResolverConfig{
			BaseURL: strings.TrimSpace(*queryURL),
			Token:   strings.TrimSpace(os.Getenv("AUTH_TOKEN")),
		}, rc.QueryHTTPClient)
	}

	stats := memberCDPOrgIDStats{Stats: *commands.NewStats()}
	stats.DryRun = rc.DryRun

	errEach := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++

		if *committeeUID != "" && member.CommitteeUID != *committeeUID {
			stats.Skipped++
			return nil
		}
		if *memberUID != "" && member.UID != *memberUID {
			stats.Skipped++
			return nil
		}

		orgID := strings.TrimSpace(member.Organization.ID)
		if !orgid.IsCDPUUID(orgID) {
			stats.Skipped++
			return nil
		}
		stats.CDPUUIDFound++

		sfid, resolved, err := resolver.ResolveSFID(ctx, member.Organization.Name, member.Organization.Website)
		if err != nil {
			slog.WarnContext(ctx, "failed to resolve b2b org SFID for member",
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"cdp_org_id", orgID,
				"organization_name", member.Organization.Name,
				"error", err,
			)
			stats.Failed++
			return nil
		}

		var wantID string
		switch {
		case resolved:
			wantID = utils.NormalizeAccountSFID(sfid)
		case *clearUnresolved:
			wantID = ""
		default:
			stats.Unresolved++
			slog.InfoContext(ctx, "CDP org id unresolved (no write)",
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"cdp_org_id", orgID,
				"organization_name", member.Organization.Name,
				"organization_website", member.Organization.Website,
			)
			return nil
		}

		if wantID == orgID {
			stats.Skipped++
			return nil
		}

		action := "resolved_sfid"
		if wantID == "" {
			action = "cleared_id"
		}
		slog.InfoContext(ctx, "committee member CDP org id drift detected",
			"member_uid", member.UID,
			"committee_uid", member.CommitteeUID,
			"committee_name", member.CommitteeName,
			"action", action,
			"was_org_id", orgID,
			"now_org_id", wantID,
			"dry_run", rc.DryRun,
		)

		if rc.DryRun {
			if wantID != "" {
				stats.Resolved++
			} else {
				stats.Cleared++
			}
			stats.Updated++
			return nil
		}

		fresh, revision, errGet := rc.CommitteeReader.GetMember(ctx, member.UID)
		if errGet != nil || fresh == nil {
			slog.WarnContext(ctx, "failed to re-read member before org id repair", "member_uid", member.UID, "error", errGet)
			stats.Failed++
			return nil
		}
		if !orgid.IsCDPUUID(strings.TrimSpace(fresh.Organization.ID)) {
			stats.Skipped++
			return nil
		}
		if utils.NormalizeAccountSFID(fresh.Organization.ID) == wantID {
			stats.Skipped++
			return nil
		}

		fresh.Organization.ID = wantID

		if _, errUpdate := rc.CommitteeWriterOrchestrator.UpdateMember(ctx, fresh, revision, true, false); errUpdate != nil {
			slog.WarnContext(ctx, "failed to update member organization id",
				"member_uid", member.UID, "committee_uid", member.CommitteeUID, "error", errUpdate)
			stats.Failed++
			return nil
		}

		if wantID != "" {
			stats.Resolved++
		} else {
			stats.Cleared++
		}
		stats.Updated++

		if *sleep > 0 {
			if err := sleepWithCtx(ctx, *sleep); err != nil {
				return err
			}
		}
		return nil
	})
	if errEach != nil {
		return errors.NewUnexpected("failed to stream members", errEach)
	}

	s.logSummary(ctx, &stats)

	if stats.Failed > 0 {
		return errors.NewUnexpected(fmt.Sprintf("%d member(s) failed to repair", stats.Failed))
	}
	return nil
}

func (s *memberCDPOrgIDSubcommand) logSummary(ctx context.Context, stats *memberCDPOrgIDStats) {
	stats.Log(ctx, "sync member-cdp-org-id")
	slog.InfoContext(ctx, "SUMMARY",
		"total", stats.Total,
		"cdp_uuid_found", stats.CDPUUIDFound,
		"resolved_sfid", stats.Resolved,
		"cleared_id", stats.Cleared,
		"unresolved", stats.Unresolved,
		"updated", stats.Updated,
		"skipped", stats.Skipped,
		"failed", stats.Failed,
		"dry_run", stats.DryRun,
	)
}
