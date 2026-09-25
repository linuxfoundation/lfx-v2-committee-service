// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	stderrors "errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type memberAvatarAttributeSubcommand struct{}

func (s *memberAvatarAttributeSubcommand) Name() string { return "member-avatar-attribute" }

func (s *memberAvatarAttributeSubcommand) Help() string {
	return "backfill/refresh committee member avatar from auth-service user_metadata.picture (rate-limited; idempotent)"
}

func (s *memberAvatarAttributeSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("member-avatar-attribute", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync member-avatar-attribute [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit the backfill to members of a single committee UID")
	missingOnly := fs.Bool("missing-only", false, "only enrich members whose avatar is currently empty")
	sleep := fs.Duration("sleep", 0, "wait between each auth-service lookup (e.g. 200ms, 1s) to respect Auth0 rate limits")
	dryRun := fs.Bool("dry-run", true, "compute what would be written without writing (pass --dry-run=false to write)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if rc.CommitteeReader == nil {
		return errors.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeMemberWriter == nil {
		return errors.NewUnexpected("CommitteeMemberWriter is not wired in RunContext")
	}
	if rc.UserReader == nil {
		return errors.NewUnexpected("UserReader is not wired in RunContext")
	}

	rc.DryRun = *dryRun
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	stats := commands.NewStats()
	stats.DryRun = rc.DryRun

	errEach := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++

		if *committeeUID != "" && member.CommitteeUID != *committeeUID {
			stats.Skipped++
			return nil
		}
		if *missingOnly && member.Avatar != "" {
			stats.Skipped++
			return nil
		}

		want, resolved, attempted := s.resolveAvatar(ctx, rc, member)
		// Rate-limit only when an auth-service call actually happened.
		if attempted && *sleep > 0 {
			if err := sleepWithCtx(ctx, *sleep); err != nil {
				return err
			}
		}
		// A resolution miss leaves the existing value untouched so a transient outage never wipes a good avatar.
		if !resolved {
			stats.Skipped++
			return nil
		}

		if member.Avatar == want {
			stats.Skipped++
			return nil
		}

		slog.InfoContext(ctx, "member avatar drift detected",
			"member_uid", member.UID,
			"committee_uid", member.CommitteeUID,
			"had_avatar", member.Avatar != "",
			"now_has_avatar", want != "",
			"dry_run", rc.DryRun,
		)

		if rc.DryRun {
			stats.Updated++
			return nil
		}

		// Re-read with revision and apply only the avatar so a concurrent change to other fields isn't
		// clobbered; the revision-checked CAS in UpdateMember guards the read→write gap.
		fresh, revision, errGet := rc.CommitteeReader.GetMember(ctx, member.UID)
		if errGet != nil || fresh == nil {
			slog.WarnContext(ctx, "failed to re-read member before avatar backfill", "member_uid", member.UID, "error", errGet)
			stats.Failed++
			return nil
		}
		if fresh.Avatar == want {
			stats.Skipped++
			return nil
		}
		fresh.Avatar = want

		if _, errUpdate := rc.CommitteeMemberWriter.UpdateMember(ctx, fresh, revision); errUpdate != nil {
			slog.WarnContext(ctx, "failed to update member avatar",
				"member_uid", member.UID, "committee_uid", member.CommitteeUID, "error", errUpdate)
			stats.Failed++
			return nil
		}
		stats.Updated++
		return nil
	})
	if errEach != nil {
		return errors.NewUnexpected("failed to stream members", errEach)
	}

	stats.Log(ctx, "sync member-avatar-attribute")

	if stats.Failed > 0 {
		return errors.NewUnexpected(fmt.Sprintf("%d member(s) failed to backfill", stats.Failed))
	}
	return nil
}

// resolveAvatar returns the member's current picture from auth-service. resolved=false (missing
// principal, NotFound, or transport error) means the caller must leave the stored avatar unchanged.
// attempted reports whether an auth-service call was made (so the caller only rate-limits real calls).
func (s *memberAvatarAttributeSubcommand) resolveAvatar(ctx context.Context, rc commands.RunContext, member *model.CommitteeMember) (want string, resolved, attempted bool) {
	principal := strings.TrimSpace(member.Username)
	if principal == "" {
		email := strings.ToLower(strings.TrimSpace(member.Email))
		if email == "" {
			return "", false, false
		}
		username, err := rc.UserReader.UsernameByEmail(ctx, email)
		if err != nil || username == "" {
			return "", false, true
		}
		principal = username
	}

	meta, err := rc.UserReader.UserMetadataByPrincipal(ctx, principal)
	if err != nil {
		var notFound errors.NotFound
		if !stderrors.As(err, &notFound) {
			slog.WarnContext(ctx, "user metadata lookup failed during avatar backfill",
				"member_uid", member.UID, "error", err)
		}
		return "", false, true
	}
	if meta == nil {
		return "", false, true
	}
	return meta.Picture, true, true
}

func sleepWithCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
