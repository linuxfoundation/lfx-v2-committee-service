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
)

// totalMembersAttributeSubcommand reconciles CommitteeBase.TotalMembers against actual member counts.
type totalMembersAttributeSubcommand struct{}

func (s *totalMembersAttributeSubcommand) Name() string { return "total-members-attribute" }

func (s *totalMembersAttributeSubcommand) Help() string {
	return "reconcile CommitteeBase.TotalMembers against the actual member count in the KV store"
}

func (s *totalMembersAttributeSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("total-members-attribute", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: committee-cli sync total-members-attribute [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit sync to a single committee UID")
	projectUID := fs.String("project-uid", "", "limit sync to committees belonging to a project")
	sleep := fs.Duration("sleep", 0, "wait between each committee update (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "compute diffs without writing")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if *committeeUID != "" && *projectUID != "" {
		return fmt.Errorf("--committee-uid and --project-uid are mutually exclusive")
	}

	rc.DryRun = *dryRun

	uids, err := s.resolveUIDs(ctx, rc, *committeeUID)
	if err != nil {
		return err
	}

	stats := commands.NewStats()
	stats.Total = len(uids)
	stats.DryRun = rc.DryRun

	for _, uid := range uids {
		if err := s.syncOne(ctx, rc, uid, *projectUID, *sleep, stats); err != nil {
			// syncOne already logged and incremented stats.Failed; keep going
			continue
		}
	}

	stats.Log(ctx, "sync total-members-attribute")

	if stats.Failed > 0 {
		return fmt.Errorf("%d committee(s) failed to sync", stats.Failed)
	}
	return nil
}

func (s *totalMembersAttributeSubcommand) resolveUIDs(ctx context.Context, rc commands.RunContext, committeeUID string) ([]string, error) {
	if committeeUID != "" {
		slog.DebugContext(ctx, "syncing single committee", "committee_uid", committeeUID)
		return []string{committeeUID}, nil
	}
	slog.DebugContext(ctx, "no committee-uid provided, listing all committees")
	uids, err := rc.CommitteeReader.ListAllUIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list committee UIDs: %w", err)
	}
	slog.DebugContext(ctx, "committees to sync", "total", len(uids))
	return uids, nil
}

func (s *totalMembersAttributeSubcommand) syncOne(ctx context.Context, rc commands.RunContext, uid, projectUID string, sleep time.Duration, stats *commands.Stats) error {
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	base, revision, err := rc.CommitteeReader.GetBase(ctx, uid)
	if err != nil {
		stats.Failed++
		slog.WarnContext(ctx, "failed to get committee base", "committee_uid", uid, "error", err)
		return err
	}

	if projectUID != "" && base.ProjectUID != projectUID {
		slog.DebugContext(ctx, "skipping committee, project does not match",
			"committee_uid", uid,
			"committee_project_uid", base.ProjectUID,
			"filter_project_uid", projectUID,
		)
		stats.Skipped++
		return nil
	}

	members, err := rc.CommitteeReader.ListMembers(ctx, uid)
	if err != nil {
		stats.Failed++
		slog.WarnContext(ctx, "failed to list members", "committee_uid", uid, "error", err)
		return err
	}
	actual := len(members)

	if base.TotalMembers == actual {
		slog.DebugContext(ctx, "skipping committee, total_members already correct",
			"committee_uid", uid,
			"total_members", actual,
		)
		stats.Skipped++
		return nil
	}

	slog.InfoContext(ctx, "total_members drift detected",
		"committee_uid", uid,
		"was", base.TotalMembers,
		"now", actual,
		"dry_run", rc.DryRun,
	)

	if rc.DryRun {
		stats.Updated++
		return nil
	}

	committee := &model.Committee{CommitteeBase: *base}
	committee.TotalMembers = actual
	if _, err := rc.CommitteeWriterOrchestrator.Update(ctx, committee, revision, false); err != nil {
		stats.Failed++
		slog.WarnContext(ctx, "failed to update committee total_members",
			"committee_uid", uid,
			"error", err,
		)
		return err
	}

	stats.Updated++
	if sleep > 0 {
		time.Sleep(sleep)
	}
	return nil
}
