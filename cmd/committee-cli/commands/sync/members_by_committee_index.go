// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/concurrent"
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
	sleep := fs.Duration("sleep", 0, "wait between each member write; ignored when --workers > 1 (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "compute what would be written without actually writing")
	workers := fs.Int("workers", 1, "number of concurrent index writes (default 1; set higher, e.g. 50, for large buckets)")
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

	slog.InfoContext(ctx, "listing all members via WatchAll stream")

	// Read all members via the full-scan path so we do not depend on an
	// index that may not yet exist. Uses WatchAll internally (one streaming
	// round trip instead of N individual GETs).
	members, err := rc.CommitteeReader.ListAllMembers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list all members: %w", err)
	}

	// Filter to the requested committee if --committee-uid was given.
	filtered := members[:0]
	skipped := 0
	for _, m := range members {
		if *committeeUID != "" && m.CommitteeUID != *committeeUID {
			skipped++
			continue
		}
		filtered = append(filtered, m)
	}

	stats := commands.NewStats()
	stats.Total = len(members)
	stats.Skipped = skipped
	stats.DryRun = rc.DryRun

	slog.InfoContext(ctx, "starting index backfill",
		"total_members", len(members),
		"to_index", len(filtered),
		"skipped", skipped,
		"workers", *workers,
		"dry_run", rc.DryRun,
	)

	if rc.DryRun {
		slog.DebugContext(ctx, "dry-run: would write member index entries", "count", len(filtered))
		stats.Updated = len(filtered)
		stats.Log(ctx, "sync members-by-committee-index")
		return nil
	}

	// Build one job per member and run them through the worker pool.
	// Each job returns nil so the pool never short-circuits on a single
	// failure — errors are tracked via atomics and logged per-member.
	var (
		updated atomic.Int64
		failed  atomic.Int64
	)

	jobs := make([]func() error, len(filtered))
	for i, m := range filtered {
		m := m
		jobs[i] = func() error {
			_, errIdx := rc.CommitteeMemberWriter.IndexMemberByCommittee(ctx, m)
			if errIdx != nil {
				slog.WarnContext(ctx, "failed to index member by committee",
					"error", errIdx,
					"committee_uid", m.CommitteeUID,
					"member_uid", m.UID,
				)
				failed.Add(1)
				return nil
			}
			slog.DebugContext(ctx, "indexed member by committee",
				"committee_uid", m.CommitteeUID,
				"member_uid", m.UID,
			)
			updated.Add(1)

			// --sleep only applies in single-worker mode to avoid
			// interleaved sleeps defeating the throttle intent.
			if *workers == 1 && *sleep > 0 {
				time.Sleep(*sleep)
			}
			return nil
		}
	}

	if err := concurrent.NewWorkerPool(*workers).Run(ctx, jobs...); err != nil {
		return fmt.Errorf("worker pool error: %w", err)
	}

	stats.Updated = int(updated.Load())
	stats.Failed = int(failed.Load())
	stats.Log(ctx, "sync members-by-committee-index")

	if stats.Failed > 0 {
		return fmt.Errorf("%d member(s) failed to index", stats.Failed)
	}
	return nil
}
