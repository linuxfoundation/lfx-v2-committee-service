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
)

// membersByCommitteeIndexSubcommand backfills the secondary index
// "lookup/committee-members-by-committee/<committeeUID>.<memberUID>"
// for all members (or a single committee) that predate the index.
type membersByCommitteeIndexSubcommand struct{}

func (s *membersByCommitteeIndexSubcommand) Name() string { return "members-by-committee-index" }

func (s *membersByCommitteeIndexSubcommand) Help() string {
	return "backfill the committee→member secondary index for existing members"
}

func (s *membersByCommitteeIndexSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("members-by-committee-index", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync members-by-committee-index [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit backfill to members of a single committee UID")
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

	// Read all members via the full-scan path so we do not depend on an
	// index that may not yet exist.
	members, err := rc.CommitteeReader.ListAllMembers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list all members: %w", err)
	}

	stats := commands.NewStats()
	stats.Total = len(members)
	stats.DryRun = rc.DryRun

	for _, member := range members {
		if *committeeUID != "" && member.CommitteeUID != *committeeUID {
			stats.Skipped++
			continue
		}

		if rc.DryRun {
			slog.InfoContext(ctx, "dry-run: would write member index",
				"committee_uid", member.CommitteeUID,
				"member_uid", member.UID,
			)
			stats.Updated++
			continue
		}

		_, errIdx := rc.CommitteeMemberWriter.IndexMemberByCommittee(ctx, member)
		if errIdx != nil {
			slog.WarnContext(ctx, "failed to index member by committee",
				"error", errIdx,
				"committee_uid", member.CommitteeUID,
				"member_uid", member.UID,
			)
			stats.Failed++
			continue
		}

		slog.DebugContext(ctx, "indexed member by committee",
			"committee_uid", member.CommitteeUID,
			"member_uid", member.UID,
		)
		stats.Updated++

		if *sleep > 0 {
			time.Sleep(*sleep)
		}
	}

	stats.Log(ctx, "sync members-by-committee-index")

	if stats.Failed > 0 {
		return fmt.Errorf("%d member(s) failed to index", stats.Failed)
	}
	return nil
}
