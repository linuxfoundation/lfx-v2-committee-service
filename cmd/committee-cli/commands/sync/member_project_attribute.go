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

// memberProjectAttributeSubcommand reconciles a committee member's denormalized ProjectUID/ProjectSlug
// against its parent committee. Members created before the denormalization landed (LFXV2-1442) carry an
// EMPTY project_uid in KV truth, which the Org Lens by-organization read silently drops from the project
// family filter. This repair re-derives both fields from the committee base and re-saves the member.
// Idempotent: members already matching their committee are skipped; re-running is safe.
type memberProjectAttributeSubcommand struct{}

func (s *memberProjectAttributeSubcommand) Name() string { return "member-project-attribute" }

func (s *memberProjectAttributeSubcommand) Help() string {
	return "reconcile committee member project_uid/project_slug against the parent committee (repairs records created before LFXV2-1442)"
}

// committeeProject is the cached (project_uid, project_slug) for a committee, plus whether the lookup
// succeeded. A failed lookup is negative-cached so a broken/missing committee isn't re-fetched per member.
type committeeProject struct {
	projectUID  string
	projectSlug string
	ok          bool
}

func (s *memberProjectAttributeSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("member-project-attribute", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync member-project-attribute [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit repair to members of a single committee UID")
	projectUID := fs.String("project-uid", "", "limit repair to members whose committee belongs to this exact project UID")
	sleep := fs.Duration("sleep", 0, "wait between each member write (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", true, "compute what would be written without actually writing (default true; pass --dry-run=false to write)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if *committeeUID != "" && *projectUID != "" {
		return fmt.Errorf("--committee-uid and --project-uid are mutually exclusive")
	}
	if rc.CommitteeReader == nil {
		return fmt.Errorf("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeMemberWriter == nil {
		return fmt.Errorf("CommitteeMemberWriter is not wired in RunContext")
	}

	rc.DryRun = *dryRun
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	stats := commands.NewStats()
	stats.DryRun = rc.DryRun

	// Cache committee base lookups for the lifetime of the run — an org/foundation touches few distinct
	// committees relative to its member count, so this keeps the repair to ~one GetBase per committee.
	cache := make(map[string]committeeProject)

	// Stream members one at a time (no full in-memory load) so the repair scales to large buckets.
	errEach := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++

		if *committeeUID != "" && member.CommitteeUID != *committeeUID {
			stats.Skipped++
			return nil
		}

		want, ok := s.resolveCommitteeProject(ctx, rc, member.CommitteeUID, cache)
		if !ok {
			// The parent committee could not be read — this member can't be repaired. Counting it failed
			// surfaces a non-zero exit so the operator notices, without re-fetching the committee.
			stats.Failed++
			return nil
		}

		// A committee with no project_uid (unexpected) gives us nothing authoritative to write.
		if want.projectUID == "" {
			slog.WarnContext(ctx, "committee has no project_uid; cannot repair member",
				"committee_uid", member.CommitteeUID, "member_uid", member.UID)
			stats.Skipped++
			return nil
		}

		if *projectUID != "" && want.projectUID != *projectUID {
			stats.Skipped++
			return nil
		}

		// Idempotent: skip members whose denormalized fields already match the committee.
		if member.ProjectUID == want.projectUID && member.ProjectSlug == want.projectSlug {
			stats.Skipped++
			return nil
		}

		slog.InfoContext(ctx, "member project attribute drift detected",
			"member_uid", member.UID,
			"committee_uid", member.CommitteeUID,
			"was_project_uid", member.ProjectUID,
			"now_project_uid", want.projectUID,
			"dry_run", rc.DryRun,
		)

		if rc.DryRun {
			stats.Updated++
			return nil
		}

		// TOCTOU: the member fields were captured by the EachMember snapshot; the revision is read here,
		// separately. A concurrent write in this window means the CAS below succeeds against the newer
		// revision but the snapshot fields overwrite it (lost update). The window is narrow and this is an
		// operator-run, dry-run-by-default repair — run it during low-traffic periods to avoid clobbering
		// concurrent writes.
		revision, errRev := rc.CommitteeReader.GetMemberRevision(ctx, member.UID)
		if errRev != nil {
			slog.WarnContext(ctx, "failed to get member revision", "member_uid", member.UID, "error", errRev)
			stats.Failed++
			return nil
		}

		member.ProjectUID = want.projectUID
		member.ProjectSlug = want.projectSlug

		if _, errUpdate := rc.CommitteeMemberWriter.UpdateMember(ctx, member, revision); errUpdate != nil {
			slog.WarnContext(ctx, "failed to update member project attributes",
				"member_uid", member.UID, "committee_uid", member.CommitteeUID, "error", errUpdate)
			stats.Failed++
			return nil
		}
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

	stats.Log(ctx, "sync member-project-attribute")

	if stats.Failed > 0 {
		return fmt.Errorf("%d member(s) failed to repair", stats.Failed)
	}
	return nil
}

// resolveCommitteeProject returns the (project_uid, project_slug) for committeeUID, reading the committee
// base from KV (cached for the run). The bool reports whether the committee was readable; a lookup failure
// is negative-cached so a broken committee isn't re-fetched once per member.
func (s *memberProjectAttributeSubcommand) resolveCommitteeProject(ctx context.Context, rc commands.RunContext, committeeUID string, cache map[string]committeeProject) (committeeProject, bool) {
	if committeeUID == "" {
		return committeeProject{}, false
	}
	if cached, ok := cache[committeeUID]; ok {
		return cached, cached.ok
	}
	base, _, err := rc.CommitteeReader.GetBase(ctx, committeeUID)
	if err != nil || base == nil {
		slog.WarnContext(ctx, "failed to read committee base for member repair",
			"committee_uid", committeeUID, "error", err)
		cache[committeeUID] = committeeProject{ok: false}
		return committeeProject{}, false
	}
	result := committeeProject{projectUID: base.ProjectUID, projectSlug: base.ProjectSlug, ok: true}
	cache[committeeUID] = result
	return result, true
}
