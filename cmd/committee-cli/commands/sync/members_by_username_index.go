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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// membersByUsernameIndexSubcommand backfills the secondary index
// "lookup/committee-members-by-username/<username_hash>.<member_uid>" for all existing members
// that carry a username. Members without a username are skipped. Idempotent: re-running is safe.
type membersByUsernameIndexSubcommand struct{}

func (s *membersByUsernameIndexSubcommand) Name() string { return "members-by-username-index" }

func (s *membersByUsernameIndexSubcommand) Help() string {
	return "backfill the username→member secondary index for existing members (LFXV2-2645)"
}

func (s *membersByUsernameIndexSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("members-by-username-index", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync members-by-username-index [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	sleep := fs.Duration("sleep", 0, "wait between each member write (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", true, "compute what would be written without actually writing (default true; pass --dry-run=false to write)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun

	if rc.CommitteeReader == nil {
		return errors.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeMemberWriter == nil {
		return errors.NewUnexpected("CommitteeMemberWriter is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	stats := commands.NewStats()
	stats.DryRun = rc.DryRun

	errEach := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++

		if member.BuildUsernameIndexKey(ctx) == "" {
			stats.Skipped++
			return nil
		}

		if rc.DryRun {
			slog.DebugContext(ctx, "dry-run: would write username member index",
				"member_uid", member.UID,
			)
			stats.Updated++
			return nil
		}

		key, errIdx := rc.CommitteeMemberWriter.IndexMemberByUsername(ctx, member)
		if errIdx != nil {
			slog.WarnContext(ctx, "failed to index member by username",
				"error", errIdx,
				"member_uid", member.UID,
			)
			stats.Failed++
			return nil
		}
		if key == "" {
			stats.Skipped++
			return nil
		}

		slog.DebugContext(ctx, "indexed member by username",
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

	stats.Log(ctx, "sync members-by-username-index")

	if stats.Failed > 0 {
		return fmt.Errorf("%d member(s) failed to index", stats.Failed)
	}
	return nil
}
