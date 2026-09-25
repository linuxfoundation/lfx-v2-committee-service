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
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
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
	dryRun := fs.Bool("dry-run", true, "compute what would be written without actually writing (default true; pass --dry-run=false to write)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun
	normalizedFilterSFID := utils.NormalizeAccountSFID(*orgSFID)

	if rc.CommitteeReader == nil {
		return fmt.Errorf("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeMemberWriter == nil {
		return fmt.Errorf("CommitteeMemberWriter is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	stats := commands.NewStats()
	stats.DryRun = rc.DryRun

	// Stream members one at a time (no full in-memory load) and index each as it is read, so the
	// backfill scales to large buckets without holding the whole member set in memory.
	errEach := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++

		normalizedOrgSFID := utils.NormalizeAccountSFID(member.Organization.ID)
		// Only members with a non-empty normalized organization.id are org-affiliated seats.
		if normalizedOrgSFID == "" {
			stats.Skipped++
			return nil
		}
		if normalizedFilterSFID != "" && normalizedOrgSFID != normalizedFilterSFID {
			stats.Skipped++
			return nil
		}

		if rc.DryRun {
			slog.DebugContext(ctx, "dry-run: would write org member index",
				"organization_id", normalizedOrgSFID,
				"member_uid", member.UID,
			)
			stats.Updated++
			return nil
		}

		key, errIdx := rc.CommitteeMemberWriter.IndexMemberByOrganization(ctx, member)
		if errIdx != nil {
			slog.WarnContext(ctx, "failed to index member by organization",
				"error", errIdx,
				"organization_id", normalizedOrgSFID,
				"member_uid", member.UID,
			)
			stats.Failed++
			return nil
		}
		if key == "" {
			// No organization.id after normalization → nothing to write.
			stats.Skipped++
			return nil
		}

		slog.DebugContext(ctx, "indexed member by organization",
			"organization_id", normalizedOrgSFID,
			"member_uid", member.UID,
		)
		stats.Updated++

		if *sleep > 0 {
			timer := time.NewTimer(*sleep)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}
		return nil
	})
	if errEach != nil {
		return fmt.Errorf("failed to stream members: %w", errEach)
	}

	stats.Log(ctx, "sync members-by-organization-index")

	if stats.Failed > 0 {
		return fmt.Errorf("%d member(s) failed to index", stats.Failed)
	}
	return nil
}
