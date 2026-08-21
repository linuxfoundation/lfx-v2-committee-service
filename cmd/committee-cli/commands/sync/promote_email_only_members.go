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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

// promoteEmailOnlyMembersSubcommand scans all committee member seats, finds those with an email but
// no username (email-only), resolves the LFID username via the auth service once per unique email,
// and promotes each resolvable seat via UpdateMember. Safe to re-run: already-promoted seats are
// skipped. Intended to catch up seats that were synced before their user had an LFID (LFXV2-2521).
type promoteEmailOnlyMembersSubcommand struct{}

func (s *promoteEmailOnlyMembersSubcommand) Name() string { return "promote-email-only-members" }

func (s *promoteEmailOnlyMembersSubcommand) Help() string {
	return "promote email-only committee member seats to LFID usernames (LFXV2-2521)"
}

func (s *promoteEmailOnlyMembersSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("promote-email-only-members", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync promote-email-only-members [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	sleep := fs.Duration("sleep", 0, "wait between each auth service call (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", true, "resolve usernames and log what would be promoted without writing (default true; pass --dry-run=false to write)")
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
	if rc.CommitteeWriterOrchestrator == nil {
		return errors.NewUnexpected("CommitteeWriterOrchestrator is not wired in RunContext")
	}
	if rc.UserReader == nil {
		return errors.NewUnexpected("UserReader is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	stats := commands.NewStats()
	stats.DryRun = rc.DryRun

	// Pass 1: stream all members and collect email-only seats grouped by normalized email.
	// Grouping avoids duplicate auth service calls for users who hold seats in multiple committees.
	emailToSeats := make(map[string][]*model.CommitteeMember)
	if err := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++
		if member.Email == "" {
			stats.Skipped++
			return nil
		}
		if member.Username != "" {
			stats.Skipped++
			return nil
		}
		normalizedEmail := strings.ToLower(strings.TrimSpace(member.Email))
		if normalizedEmail == "" {
			stats.Skipped++
			return nil
		}
		emailToSeats[normalizedEmail] = append(emailToSeats[normalizedEmail], member)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to stream members: %w", err)
	}

	// Pass 2: for each unique email, resolve username once and promote all matching seats.
	for email, seats := range emailToSeats {
		username, err := rc.UserReader.UsernameByEmail(ctx, email)
		if err != nil {
			var notFound errors.NotFound
			if stderrors.As(err, &notFound) {
				slog.DebugContext(ctx, "no LFID for email yet — skipping seats",
					"email", redaction.RedactEmail(email),
					"seats", len(seats),
				)
				stats.Skipped += len(seats)
			} else {
				slog.WarnContext(ctx, "failed to resolve username by email — skipping seats",
					"email", redaction.RedactEmail(email),
					"seats", len(seats),
					"error", err,
				)
				stats.Failed += len(seats)
			}
			continue
		}

		if username == "" {
			slog.DebugContext(ctx, "email not resolvable to an LFID yet — skipping seats",
				"email", redaction.RedactEmail(email),
				"seats", len(seats),
			)
			stats.Skipped += len(seats)
			continue
		}

		for _, seat := range seats {
			if rc.DryRun {
				slog.DebugContext(ctx, "dry-run: would promote email-only member to LFID",
					"committee_uid", seat.CommitteeUID,
					"member_uid", seat.UID,
					"username", redaction.Redact(username),
				)
				stats.Updated++
				continue
			}

			if err := promoteEmailOnlyMember(ctx, rc, seat.CommitteeUID, seat.UID, username, email); err != nil {
				stats.Failed++
			} else {
				stats.Updated++
			}
		}

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
	}

	stats.Log(ctx, "sync promote-email-only-members")

	if stats.Failed > 0 {
		return fmt.Errorf("%d seat(s) failed to promote", stats.Failed)
	}
	return nil
}

// promoteEmailOnlyMember sets the username on a single email-only committee member seat.
// It retries up to maxRetries times on revision conflicts, and skips seats that are already
// promoted or whose email no longer matches (index lag between pass 1 and pass 2).
func promoteEmailOnlyMember(ctx context.Context, rc commands.RunContext, committeeUID, memberUID, username, email string) error {
	const maxRetries = 3
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))

	for attempt := 0; attempt < maxRetries; attempt++ {
		member, revision, err := rc.CommitteeReader.GetMember(ctx, memberUID)
		if err != nil {
			slog.WarnContext(ctx, "failed to get member for username promotion",
				"error", err,
				"committee_uid", committeeUID,
				"member_uid", memberUID,
			)
			return err
		}

		// Skip if already promoted or if the email no longer matches (member may have been updated
		// between pass 1 and pass 2).
		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != normalizedEmail {
			return nil
		}

		member.Username = username

		updated, writeErr := rc.CommitteeWriterOrchestrator.UpdateMember(ctx, member, revision, false, true)
		if writeErr != nil {
			var conflictErr errors.Conflict
			if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
				slog.DebugContext(ctx, "revision conflict promoting member username — retrying",
					"attempt", attempt+1,
					"committee_uid", committeeUID,
					"member_uid", memberUID,
				)
				continue
			}
			slog.ErrorContext(ctx, "failed to promote email-only member to LFID",
				"error", writeErr,
				"committee_uid", committeeUID,
				"member_uid", memberUID,
			)
			return writeErr
		}

		persistedUsername := username
		if updated != nil {
			persistedUsername = updated.Username
		}
		slog.DebugContext(ctx, "promoted email-only member to LFID",
			"committee_uid", committeeUID,
			"member_uid", memberUID,
			"username", redaction.Redact(persistedUsername),
		)
		return nil
	}

	// Unreachable: the final iteration always returns above. Required by the compiler.
	return nil
}
