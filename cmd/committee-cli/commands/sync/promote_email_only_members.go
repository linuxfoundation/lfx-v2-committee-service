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

// emailLookup caches the result of a single UsernameByEmail call.
type emailLookup struct {
	username string // empty when not yet linked to an LFID
	failed   bool   // true when a non-NotFound error occurred
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

	// emailCache deduplicates UsernameByEmail calls: for users who hold seats in multiple
	// committees, we only call the auth service once per unique normalized email.
	// Accumulating only string→string pairs keeps memory bounded regardless of bucket size,
	// in keeping with EachMember's bounded-memory contract.
	emailCache := make(map[string]*emailLookup)

	if err := rc.CommitteeReader.EachMember(ctx, func(member *model.CommitteeMember) error {
		stats.Total++
		if member.Email == "" || member.Username != "" {
			stats.Skipped++
			return nil
		}
		normalizedEmail := strings.ToLower(strings.TrimSpace(member.Email))
		if normalizedEmail == "" {
			stats.Skipped++
			return nil
		}

		// Resolve username, calling the auth service only on the first encounter of this email.
		lookup, seen := emailCache[normalizedEmail]
		if !seen {
			lookup = &emailLookup{}
			username, err := rc.UserReader.UsernameByEmail(ctx, normalizedEmail)
			if err != nil {
				var notFound errors.NotFound
				if !stderrors.As(err, &notFound) {
					slog.WarnContext(ctx, "failed to resolve username by email",
						"email", redaction.RedactEmail(normalizedEmail),
						"error", err,
					)
					lookup.failed = true
				}
				// NotFound: lookup.username stays "" and lookup.failed stays false — skip silently.
			} else {
				lookup.username = username
			}
			emailCache[normalizedEmail] = lookup

			// Throttle only on new auth-service calls, not on cache hits.
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

		if lookup.failed {
			stats.Failed++
			return nil
		}
		if lookup.username == "" {
			stats.Skipped++
			return nil
		}

		if rc.DryRun {
			slog.DebugContext(ctx, "dry-run: would promote email-only member to LFID",
				"committee_uid", member.CommitteeUID,
				"member_uid", member.UID,
				"username", redaction.Redact(lookup.username),
			)
			stats.Updated++
			return nil
		}

		wrote, err := promoteEmailOnlyMember(ctx, rc, member.CommitteeUID, member.UID, lookup.username, normalizedEmail)
		if err != nil {
			stats.Failed++
		} else if wrote {
			stats.Updated++
		} else {
			// Seat was already promoted or email changed between stream and write — not a failure.
			stats.Skipped++
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to stream members: %w", err)
	}

	stats.Log(ctx, "sync promote-email-only-members")

	if stats.Failed > 0 {
		return fmt.Errorf("%d seat(s) failed to promote", stats.Failed)
	}
	return nil
}

// promoteEmailOnlyMember sets the username on a single email-only committee member seat.
// Returns (true, nil) when the write succeeded, (false, nil) when the seat was skipped
// (already promoted or email changed between stream and write), and (false, err) on failure.
// Retries up to maxRetries times on revision conflicts.
func promoteEmailOnlyMember(ctx context.Context, rc commands.RunContext, committeeUID, memberUID, username, email string) (bool, error) {
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
			return false, err
		}

		// Skip if already promoted or if the email no longer matches (member may have been
		// updated between stream and write).
		if member.Username != "" || strings.ToLower(strings.TrimSpace(member.Email)) != normalizedEmail {
			return false, nil
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
			return false, writeErr
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
		return true, nil
	}

	// Unreachable: the final iteration always returns above. Required by the compiler.
	return false, nil
}
