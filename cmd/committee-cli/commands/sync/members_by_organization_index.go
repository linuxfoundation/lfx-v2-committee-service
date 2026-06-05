// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// membersByOrganizationIndexSubcommand backfills the secondary index
// "lookup/committee-members-by-organization/<org_sfid>.<member_uid>" for all existing members that
// carry an organization.id (LFXV2-1865 — Org Lens Board & Committee read). Members without an
// organization.id are skipped (they are not an org's seat). Idempotent: re-running is safe.
type membersByOrganizationIndexSubcommand struct{}

func (s *membersByOrganizationIndexSubcommand) Name() string { return "members-by-organization-index" }

func (s *membersByOrganizationIndexSubcommand) Help() string {
	return "backfill the organization→member secondary index for existing members (Org Lens, LFXV2-1865)"
}

func (s *membersByOrganizationIndexSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("members-by-organization-index", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync members-by-organization-index [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	orgSFID := fs.String("org-sfid", "", "limit backfill to members of a single organization SFID")
	sleep := fs.Duration("sleep", 0, "wait between each member write (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "compute what would be written without actually writing")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun

	if rc.CommitteeReader == nil {
		return fmt.Errorf("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeMemberWriter == nil {
		return fmt.Errorf("CommitteeMemberWriter is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	// Read all members via the full-scan path so we do not depend on the index being backfilled.
	members, err := rc.CommitteeReader.ListAllMembers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list all members: %w", err)
	}

	stats := commands.NewStats()
	stats.Total = len(members)
	stats.DryRun = rc.DryRun

	for _, member := range members {
		// Only members with an organization.id are org-affiliated seats.
		if member.Organization.ID == "" {
			stats.Skipped++
			continue
		}
		// Normalize both sides to the 18-char canonical SFID so a 15-char stored organization.id still
		// matches an 18-char --org-sfid flag (same Salesforce record); IndexMemberByOrganization keys
		// on the normalized form, so the filter must too or eligible members are silently skipped.
		if *orgSFID != "" && utils.NormalizeAccountSFID(member.Organization.ID) != utils.NormalizeAccountSFID(*orgSFID) {
			stats.Skipped++
			continue
		}

		if rc.DryRun {
			slog.DebugContext(ctx, "dry-run: would write org member index",
				"organization_id", member.Organization.ID,
				"member_uid", member.UID,
			)
			stats.Updated++
			continue
		}

		key, errIdx := rc.CommitteeMemberWriter.IndexMemberByOrganization(ctx, member)
		if errIdx != nil {
			slog.WarnContext(ctx, "failed to index member by organization",
				"error", errIdx,
				"organization_id", member.Organization.ID,
				"member_uid", member.UID,
			)
			stats.Failed++
			continue
		}
		if key == "" {
			// No organization.id after normalization → nothing to write.
			stats.Skipped++
			continue
		}

		slog.DebugContext(ctx, "indexed member by organization",
			"organization_id", member.Organization.ID,
			"member_uid", member.UID,
		)
		stats.Updated++

		if *sleep > 0 {
			time.Sleep(*sleep)
		}
	}

	stats.Log(ctx, "sync members-by-organization-index")

	if stats.Failed > 0 {
		return fmt.Errorf("%d member(s) failed to index", stats.Failed)
	}
	return nil
}
