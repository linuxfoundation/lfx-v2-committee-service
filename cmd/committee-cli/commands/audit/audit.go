// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package audit provides source-owned bootstrap, replay and queue inspection.
package audit

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

type command struct{}

// NewCommand returns the audited publisher's operational command group.
func NewCommand() commands.Command { return &command{} }
func (*command) Name() string      { return "audit" }
func (*command) Help() string {
	return "Inspect, bootstrap or replay the retained committee audit outbox"
}
func (*command) Subcommands() map[string]commands.Subcommand {
	return map[string]commands.Subcommand{"status": &operation{"status"}, "bootstrap": &operation{"bootstrap"}, "replay": &operation{"replay"}, "retire": &operation{"retire"}, "prune": &operation{"prune"}}
}

type operation struct{ name string }

func (o *operation) Name() string { return o.name }
func (o *operation) Help() string {
	return "Audit " + o.name + " (PostgreSQL required; never prints event payloads)"
}
func (o *operation) Run(ctx context.Context, rc commands.RunContext) error {
	flags := flag.NewFlagSet("audit "+o.name, flag.ContinueOnError)
	committee := flags.String("committee", "", "Committee UID for a scoped source bootstrap")
	event := flags.String("event-id", "", "Original retained event ID to replay")
	dry := flags.Bool("dry-run", false, "Validate the selection without changing outbox state")
	confirmed := flags.Bool("confirmed-persisted", false, "Confirm the event has completed required indexer/history processing, not only PubAck")
	attempt := flags.Uint64("delivery-attempt", 0, "Broker delivery generation independently verified as persisted (from audit status --event-id)")
	if err := flags.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected audit command arguments")
	}
	if o.name == "bootstrap" && *committee == "" {
		return fmt.Errorf("audit bootstrap requires --committee")
	}
	if (o.name == "replay" || o.name == "retire") && *event == "" {
		return fmt.Errorf("audit replay requires --event-id")
	}
	if o.name == "retire" && !*dry && !*confirmed {
		return fmt.Errorf("retirement requires --confirmed-persisted after indexer reconciliation")
	}
	if o.name == "retire" && *attempt == 0 {
		return fmt.Errorf("retirement requires --delivery-attempt from the independently verified delivery")
	}
	if rc.PostgresStore == nil {
		return fmt.Errorf("PostgreSQL is required for audit operations")
	}
	store, closeStore, err := rc.PostgresStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx = model.WithCommitteeSystemActor(ctx)
	switch o.name {
	case "status":
		if *event != "" {
			generation, err := store.AuditDeliveryAttempt(ctx, *event)
			if err == nil {
				slog.InfoContext(ctx, "committee audit delivery", "event_id", *event, "delivery_attempt", generation)
			}
			return err
		}
		status, err := store.AuditQueueStatus(ctx)
		if err != nil {
			return err
		}
		var age float64
		if status.OldestPendingAt != nil {
			age = time.Since(*status.OldestPendingAt).Seconds()
		}
		slog.InfoContext(ctx, "committee audit queue", "retained", status.Retained, "pending", status.Pending, "oldest_pending_age_seconds", age)
		return nil
	case "bootstrap":
		if *dry {
			count, err := store.PreviewAuditBootstrap(ctx, *committee)
			if err == nil {
				slog.InfoContext(ctx, "validated committee audit baseline", "events", count, "dry_run", true)
			}
			return err
		}
		count, err := store.QueueAuditBootstrap(ctx, *committee)
		if err == nil {
			slog.InfoContext(ctx, "queued committee audit baseline", "events", count)
		}
		return err
	case "replay":
		if *dry {
			_, _, err := store.ReadRetainedAuditEvent(ctx, *event)
			return err
		}
		return store.RequeueAuditEvent(ctx, *event)
	case "retire":
		if *dry {
			return store.PreviewAuditRetirement(ctx, *event, *attempt)
		}
		return store.RetireAuditEvent(ctx, *event, *attempt)
	case "prune":
		retention, err := postgres.AuditRetentionFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		if *dry {
			return fmt.Errorf("use audit status for inspection; prune does not support --dry-run")
		}
		count, err := store.PruneRetiredAuditEvents(ctx, retention)
		if err == nil {
			slog.InfoContext(ctx, "pruned retired audit records", "records", count)
		}
		return err
	default:
		return fmt.Errorf("unknown audit operation")
	}
}
