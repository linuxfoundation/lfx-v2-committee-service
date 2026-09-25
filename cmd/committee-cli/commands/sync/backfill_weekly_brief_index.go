// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// backfillWeeklyBriefIndexSubcommand walks every key in the
// group-weekly-brief-uid-index KV bucket and emits an indexer message for each
// brief, regardless of state. Use this after wiring the live indexer emission
// to catch all briefs that pre-date the emission.
type backfillWeeklyBriefIndexSubcommand struct{}

func (s *backfillWeeklyBriefIndexSubcommand) Name() string { return "backfill-weekly-brief-index" }

func (s *backfillWeeklyBriefIndexSubcommand) Help() string {
	return "emit indexer messages for all group weekly briefs in NATS KV (backfill for briefs pre-dating live emission)"
}

func (s *backfillWeeklyBriefIndexSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("backfill-weekly-brief-index", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync backfill-weekly-brief-index [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit backfill to briefs of a single committee UID")
	sleep := fs.Duration("sleep", 0, "wait between each publish (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "log what would be published without actually publishing")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun

	if rc.GroupWeeklyBriefReader == nil {
		return errs.NewUnexpected("GroupWeeklyBriefReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return errs.NewUnexpected("publisher is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	keys, err := rc.GroupWeeklyBriefReader.ListGroupWeeklyBriefIndexKeys(ctx)
	if err != nil {
		return errs.NewUnexpected("failed to list weekly-brief index keys", err)
	}

	// Filter to a single committee when --committee-uid is set. Each key is
	// "{committee_uid}.{yyyymmdd}"; a matching prefix means the key belongs to
	// the requested committee.
	if *committeeUID != "" {
		prefix := *committeeUID + "."
		filtered := keys[:0]
		for _, k := range keys {
			if strings.HasPrefix(k, prefix) {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	stats := commands.NewStats()
	stats.Total = len(keys)
	stats.DryRun = rc.DryRun

	for _, key := range keys {
		committeeUIDFromKey, windowStart, parseErr := parseIndexKey(key)
		if parseErr != nil {
			slog.WarnContext(ctx, "backfill-weekly-brief-index: skipping malformed index key",
				"key", key, "error", parseErr)
			stats.Skipped++
			continue
		}

		brief, _, fetchErr := rc.GroupWeeklyBriefReader.GetGroupWeeklyBriefForWindow(ctx,
			committeeUIDFromKey,
			model.GroupWeeklyBrief{WindowStart: windowStart},
		)
		if fetchErr != nil {
			slog.WarnContext(ctx, "backfill-weekly-brief-index: failed to fetch brief",
				"key", key,
				"committee_uid", committeeUIDFromKey,
				"error", fetchErr,
			)
			stats.Failed++
			continue
		}
		if brief == nil {
			// Index key points to a missing brief — stale index entry; skip.
			slog.WarnContext(ctx, "backfill-weekly-brief-index: index key has no matching brief; skipping",
				"key", key,
				"committee_uid", committeeUIDFromKey,
			)
			stats.Skipped++
			continue
		}

		if rc.DryRun {
			slog.InfoContext(ctx, "dry-run: would index weekly brief",
				"brief_uid", brief.UID,
				"committee_uid", brief.CommitteeUID,
				"state", brief.State,
				"window_start", brief.WindowStart.Format(time.RFC3339),
			)
			stats.Updated++
			continue
		}

		if err := publishWeeklyBriefIndexMessage(ctx, rc, brief); err != nil {
			slog.WarnContext(ctx, "backfill-weekly-brief-index: failed to publish indexer message",
				"brief_uid", brief.UID,
				"committee_uid", brief.CommitteeUID,
				"state", brief.State,
				"error", err,
			)
			stats.Failed++
		} else {
			slog.DebugContext(ctx, "backfill-weekly-brief-index: indexed brief",
				"brief_uid", brief.UID,
				"committee_uid", brief.CommitteeUID,
				"state", brief.State,
			)
			stats.Updated++
		}

		if *sleep > 0 {
			time.Sleep(*sleep)
		}
	}

	stats.Log(ctx, "sync backfill-weekly-brief-index")

	if stats.Failed > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d brief(s) failed to index", stats.Failed))
	}
	return nil
}

// parseIndexKey extracts the committee UID and window start from a key of the
// form "{committee_uid}.{yyyymmdd}". The yyyymmdd suffix is always exactly 8
// characters; everything before the last dot is the committee UID.
func parseIndexKey(key string) (committeeUID string, windowStart time.Time, err error) {
	idx := strings.LastIndex(key, ".")
	if idx < 0 || idx == len(key)-1 {
		return "", time.Time{}, fmt.Errorf("key %q has no dot-separated date suffix", key)
	}
	datePart := key[idx+1:]
	if len(datePart) != 8 {
		return "", time.Time{}, fmt.Errorf("key %q: date suffix %q must be 8 characters (YYYYMMDD)", key, datePart)
	}
	t, parseErr := time.Parse("20060102", datePart)
	if parseErr != nil {
		return "", time.Time{}, fmt.Errorf("key %q: date suffix %q is not a valid YYYYMMDD date: %w", key, datePart, parseErr)
	}
	return key[:idx], t.UTC(), nil
}

// publishWeeklyBriefIndexMessage builds and publishes a group_weekly_brief
// indexer message. It is intentionally separate from the service-layer helper
// so the CLI avoids importing internal/service directly.
func publishWeeklyBriefIndexMessage(ctx context.Context, rc commands.RunContext, brief *model.GroupWeeklyBrief) error {
	public := false
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             brief.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", brief.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", brief.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", brief.CommitteeUID)},
		Fulltext:             brief.BriefText,
		Tags:                 brief.Tags(),
		Public:               &public,
	}
	msg := model.CommitteeIndexerMessage{
		Action:         model.ActionUpdated,
		Tags:           brief.Tags(),
		IndexingConfig: indexingConfig,
	}
	built, err := msg.Build(ctx, brief)
	if err != nil {
		return fmt.Errorf("failed to build indexer message: %w", err)
	}
	return rc.Publisher.Indexer(ctx, constants.IndexGroupWeeklyBriefSubject, built, false)
}
