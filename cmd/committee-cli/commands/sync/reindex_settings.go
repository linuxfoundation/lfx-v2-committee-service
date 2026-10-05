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
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// reindexSettingsSubcommand re-publishes all committee settings documents from NATS KV to
// the indexer (OpenSearch) with public=false, correcting any documents that were previously
// indexed with public=true (inherited from the committee's own visibility).
type reindexSettingsSubcommand struct{}

func (s *reindexSettingsSubcommand) Name() string { return "reindex-settings" }

func (s *reindexSettingsSubcommand) Help() string {
	return "re-publish all committee settings from NATS KV to OpenSearch with public=false"
}

func (s *reindexSettingsSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("reindex-settings", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync reindex-settings [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit reindex to a single committee UID")
	sleep := fs.Duration("sleep", 0, "wait between each publish (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "log what would be published without actually publishing")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun

	if rc.CommitteeReader == nil {
		return errs.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return errs.NewUnexpected("publisher is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	var uids []string
	if *committeeUID != "" {
		uids = []string{*committeeUID}
	} else {
		var listErr error
		uids, listErr = rc.CommitteeReader.ListAllUIDs(ctx)
		if listErr != nil {
			return errs.NewUnexpected("failed to list committee UIDs", listErr)
		}
	}

	stats := commands.NewStats()
	stats.Total = len(uids)
	stats.DryRun = rc.DryRun

	for _, uid := range uids {
		base, _, getBaseErr := rc.CommitteeReader.GetBase(ctx, uid)
		if getBaseErr != nil {
			slog.WarnContext(ctx, "reindex-settings: failed to fetch committee base — skipping",
				"committee_uid", uid, "error", getBaseErr)
			stats.Failed++
			continue
		}

		settings, _, getSettingsErr := rc.CommitteeReader.GetSettings(ctx, uid)
		if getSettingsErr != nil {
			slog.WarnContext(ctx, "reindex-settings: failed to fetch committee settings — skipping",
				"committee_uid", uid, "error", getSettingsErr)
			stats.Failed++
			continue
		}
		if settings == nil {
			slog.DebugContext(ctx, "reindex-settings: no settings record — skipping",
				"committee_uid", uid)
			stats.Skipped++
			continue
		}

		if rc.DryRun {
			slog.InfoContext(ctx, "dry-run: would reindex settings",
				"committee_uid", uid,
				"committee_name", base.Name,
				"public", base.Public,
			)
			stats.Updated++
			continue
		}

		if err := publishSettingsIndexerMessage(ctx, rc, base, settings); err != nil {
			slog.WarnContext(ctx, "reindex-settings: failed to publish indexer message",
				"committee_uid", uid, "error", err)
			stats.Failed++
			continue
		}

		slog.DebugContext(ctx, "reindex-settings: reindexed settings",
			"committee_uid", uid,
			"committee_name", base.Name,
		)
		stats.Updated++

		if *sleep > 0 {
			time.Sleep(*sleep)
		}
	}

	stats.Log(ctx, "sync reindex-settings")

	if stats.Failed > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d committee(s) failed to reindex settings", stats.Failed))
	}
	return nil
}

// publishSettingsIndexerMessage publishes a committee_settings indexer message with public=false,
// matching the corrected behaviour of buildCommitteeSettingsIndexingConfig in the service layer.
func publishSettingsIndexerMessage(ctx context.Context, rc commands.RunContext, base *model.CommitteeBase, settings *model.CommitteeSettings) error {
	committee := &model.Committee{CommitteeBase: *base}
	tags := committee.Tags()

	notPublic := false
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             base.UID,
		AccessCheckObject:    fmt.Sprintf("committee_settings:%s", base.UID),
		AccessCheckRelation:  "auditor",
		HistoryCheckObject:   fmt.Sprintf("committee_settings:%s", base.UID),
		HistoryCheckRelation: "auditor",
		Tags:                 tags,
		Public:               &notPublic,
	}

	// Strip the webhook URL — bearer credential that must not enter the search index.
	indexSettings := *settings
	indexSettings.ChatWebhookURL = nil

	msg := model.CommitteeIndexerMessage{
		Action:         model.ActionUpdated,
		Tags:           tags,
		IndexingConfig: indexingConfig,
	}

	built, err := msg.Build(ctx, &indexSettings)
	if err != nil {
		return fmt.Errorf("failed to build settings indexer message: %w", err)
	}

	return rc.Publisher.Indexer(ctx, constants.IndexCommitteeSettingsSubject, built, false)
}
