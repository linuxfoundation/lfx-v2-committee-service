// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/report"
	storagecmd "github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/storage"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/sync"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	usecaseSvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/env"
	logging "github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
)

// Build-time variables set via ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	registry := buildRegistry()

	const positionalLimit = 2
	parsed := splitArgs(os.Args[1:], positionalLimit)
	positionals := parsed.Positionals

	// Intercept --help/-h before any infrastructure is initialised so help
	// always exits 0 regardless of how many positionals are present.
	if hasHelpFlag(parsed.SubArgs) {
		switch len(positionals) {
		case 0, 1:
			printUsage(os.Stdout, registry)
			return nil
		default:
			if grp, ok := registry[positionals[0]]; ok {
				if sub, ok := grp.Subcommands()[positionals[1]]; ok {
					_ = sub.Run(ctx, commands.RunContext{Args: []string{"--help"}})
					return nil
				}
			}
			fmt.Fprintf(os.Stderr, "unknown command: %s %s\n\n", positionals[0], positionals[1])
			printUsage(os.Stderr, registry)
			return fmt.Errorf("unknown command: %s %s", positionals[0], positionals[1])
		}
	}

	logging.InitStructureLogConfig()

	if len(positionals) < 2 {
		printUsage(os.Stderr, registry)
		return fmt.Errorf("usage: committee-cli <command> <subcommand> [subcommand flags]")
	}
	commandName := positionals[0]
	subcommandName := positionals[1]

	cmd, ok := registry[commandName]
	if !ok {
		printUsage(os.Stderr, registry)
		return fmt.Errorf("unknown command: %s", commandName)
	}

	sub, ok := cmd.Subcommands()[subcommandName]
	if !ok {
		printUsage(os.Stderr, registry)
		return fmt.Errorf("unknown subcommand: %s %s", commandName, subcommandName)
	}

	// Run invocations that need no infrastructure (e.g. --list-fields) before connecting to NATS,
	// so they work when the broker is unreachable.
	if offline, ok := sub.(commands.OfflineSubcommand); ok && offline.RunsOffline(parsed.SubArgs) {
		return sub.Run(ctx, commands.RunContext{Args: parsed.SubArgs})
	}

	// A store mode that was set on purpose and cannot be used stops the command before anything connects; with
	// STORAGE_MODE unset the mode is kv and the command is as it has always been.
	storageMode, err := composite.ModeFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid storage mode: %w", err)
	}
	composite.NewMetrics().RecordMode(ctx, storageMode)

	natsURL := env.Get("NATS_URL", "nats://localhost:4222")
	natsTimeout := 120 * time.Second
	if raw := strings.TrimSpace(env.Get("NATS_TIMEOUT", "")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid NATS_TIMEOUT %q: %w", raw, err)
		}
		if parsed <= 0 {
			return fmt.Errorf("invalid NATS_TIMEOUT %q: must be greater than zero", raw)
		}
		natsTimeout = parsed
	}
	client, err := nats.NewClient(ctx, nats.Config{
		URL:           natsURL,
		Timeout:       natsTimeout,
		MaxReconnect:  3,
		ReconnectWait: 2 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer func() { _ = client.Close() }()

	storage := nats.NewStorageBackend(client)
	// What the commands write committee data through: the KV storage in kv mode, the composite stores of the mode otherwise.
	// The KV maintenance commands below (index backfillers, exports) keep using the KV storage itself.
	selected, err := selectStores(ctx, storageMode, client, storage, storage, storage, connectPostgres)
	if err != nil {
		return err
	}
	defer selected.close()
	publisher := nats.NewMessagePublisher(client)
	userReader := nats.NewUserRequest(client)
	b2bOrgFallbackResolver := nats.NewB2BOrgFallbackResolver(client)

	writerOrchestrator := usecaseSvc.NewCommitteeWriterOrchestrator(
		usecaseSvc.WithCommitteeRetriever(selected.committees),
		usecaseSvc.WithCommitteeWriter(selected.committees),
		usecaseSvc.WithProjectRetriever(nats.NewMessageRequest(client)),
		usecaseSvc.WithUserReader(userReader),
		usecaseSvc.WithCommitteePublisher(publisher),
		usecaseSvc.WithB2BOrgResolver(nats.NewB2BOrgResolver(client)),
		usecaseSvc.WithB2BOrgFallbackResolver(b2bOrgFallbackResolver),
	)

	rc := commands.RunContext{
		CommitteeReader:             selected.committees,
		KVCommitteeReader:           storage,
		CommitteeWriterOrchestrator: writerOrchestrator,
		CommitteeMemberWriter:       selected.committees,
		MemberIndexBackfiller:       storage,
		CommitteeInviteWriter:       selected.committees,
		DocumentAuditSync:           selected.documentAudit,
		Publisher:                   publisher,
		UserReader:                  userReader,
		GroupWeeklyBriefReader:      selected.briefs,
		CommitteeBaseWriter:         selected.committees,
		KVStore:                     client.GetOrBindKVStore,
		KVExport:                    storage,
		KVRepair:                    nats.NewKVRepairStore(client),
		PostgresStore:               selected.postgres,
		CommitteeApplicationReader:  selected.committees,
		CommitteeApplicationWriter:  selected.committees,
		B2BOrgFallbackResolver:      b2bOrgFallbackResolver,
		Args:                        parsed.SubArgs,
	}

	return sub.Run(ctx, rc)
}

func buildRegistry() map[string]commands.Command {
	syncCmd := sync.NewCommand()
	reportCmd := report.NewCommand()
	storageCmd := storagecmd.NewCommand()
	return map[string]commands.Command{
		syncCmd.Name():    syncCmd,
		reportCmd.Name():  reportCmd,
		storageCmd.Name(): storageCmd,
	}
}

func printUsage(w io.Writer, registry map[string]commands.Command) {
	_, _ = fmt.Fprintln(w, "usage: committee-cli <command> <subcommand> [subcommand flags]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "environment variables:")
	_, _ = fmt.Fprintln(w, "  NATS_URL            NATS server address (default: nats://localhost:4222)")
	_, _ = fmt.Fprintln(w, "  NATS_TIMEOUT        NATS connection dial/timeout (default: 120s); KV ops use caller context")
	_, _ = fmt.Fprintln(w, "  POSTGRES_URL        PostgreSQL connection string (storage migrate)")
	_, _ = fmt.Fprintln(w, "  POSTGRES_MAX_CONNS  PostgreSQL pool size (storage migrate; default 10)")
	_, _ = fmt.Fprintln(w, "  STORAGE_MODE        Store mode: kv, dual, dual-pg, postgres (default: kv); the modes but kv need POSTGRES_URL")
	_, _ = fmt.Fprintln(w, "  STORAGE_READ_COMPARE_RATE, STORAGE_COMPARE_TIMEOUT, STORAGE_SECONDARY_WRITE_TIMEOUT")
	_, _ = fmt.Fprintln(w, "                      Tune dual and dual-pg: the fraction of reads compared (default 0.01) and the two timeouts (2s, 3s)")
	_, _ = fmt.Fprintln(w, "  OPENSEARCH_URL      OpenSearch base URL (default: http://localhost:9200)")
	_, _ = fmt.Fprintln(w, "  OPENSEARCH_INDEX    OpenSearch resources index (default: resources)")
	_, _ = fmt.Fprintln(w, "  LOG_LEVEL           Log verbosity, e.g. info (default: debug)")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "commands:")
	for _, cmd := range registry {
		_, _ = fmt.Fprintf(w, "  %-30s %s\n", cmd.Name(), cmd.Help())
		for _, sub := range cmd.Subcommands() {
			_, _ = fmt.Fprintf(w, "    %-28s %s\n", sub.Name(), sub.Help())
		}
	}
}
