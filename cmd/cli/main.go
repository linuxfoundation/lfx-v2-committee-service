// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/cli/commands/sync"
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
	ctx := context.Background()

	registry := buildRegistry()
	flag.CommandLine.Usage = func() { printUsage(registry) }

	natsURL := flag.String("nats-url", env.Get("NATS_URL", "nats://localhost:4222"), "NATS server address")
	dryRun := flag.Bool("dry-run", false, "compute diffs without writing")
	debug := flag.Bool("debug", false, "verbose structured logging")

	const positionalLimit = 2
	parsed := splitArgs(os.Args[1:], positionalLimit)
	positionals, flagArgs := parsed.Positionals, parsed.FlagArgs

	// When both positionals are known, --help belongs to the subcommand.
	// Intercept before flag.CommandLine.Parse steals it and shows global usage.
	if len(positionals) >= 2 && hasHelpFlag(flagArgs) {
		if grp, ok := registry[positionals[0]]; ok {
			if sub, ok := grp.Subcommands()[positionals[1]]; ok {
				_ = sub.Run(ctx, commands.RunContext{Args: []string{"--help"}})
			}
		}
		printUsage(registry)
		os.Exit(0)
	}

	if err := flag.CommandLine.Parse(flagArgs); err != nil {
		os.Exit(1)
	}

	if *debug {
		os.Setenv("LOG_LEVEL", "debug")
	}
	logging.InitStructureLogConfig()

	if len(positionals) < 2 {
		printUsage(buildRegistry())
		os.Exit(1)
	}
	commandName := positionals[0]
	subcommandName := positionals[1]

	cmd, ok := registry[commandName]
	if !ok {
		slog.Error("unknown command", "command", commandName)
		printUsage(registry)
		os.Exit(1)
	}

	sub, ok := cmd.Subcommands()[subcommandName]
	if !ok {
		slog.Error("unknown subcommand", "command", commandName, "subcommand", subcommandName)
		printUsage(registry)
		os.Exit(1)
	}

	client, err := nats.NewClient(ctx, nats.Config{
		URL:           *natsURL,
		Timeout:       10 * time.Second,
		MaxReconnect:  3,
		ReconnectWait: 2 * time.Second,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to connect to NATS", "error", err, "url", *natsURL)
		os.Exit(1)
	}
	defer client.Close()

	storage := nats.NewStorage(client)

	writerOrchestrator := usecaseSvc.NewCommitteeWriterOrchestrator(
		usecaseSvc.WithCommitteeRetriever(storage),
		usecaseSvc.WithCommitteeWriter(storage),
		usecaseSvc.WithProjectRetriever(nats.NewMessageRequest(client)),
		usecaseSvc.WithUserReader(nats.NewUserRequest(client)),
		usecaseSvc.WithCommitteePublisher(nats.NewMessagePublisher(client)),
	)

	rc := commands.RunContext{
		CommitteeReader:             storage,
		CommitteeWriterOrchestrator: writerOrchestrator,
		DryRun:                      *dryRun,
		Args:                        flag.Args(),
	}

	if err := sub.Run(ctx, rc); err != nil {
		slog.ErrorContext(ctx, "command failed", "error", err)
		os.Exit(1)
	}
}

func buildRegistry() map[string]commands.Command {
	syncCmd := sync.NewCommand()
	return map[string]commands.Command{
		syncCmd.Name(): syncCmd,
	}
}

func printUsage(registry map[string]commands.Command) {
	fmt.Fprintln(os.Stderr, "usage: committee-cli [flags] <command> <subcommand> [subcommand flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "global flags:")
	fmt.Fprintln(os.Stderr, "  --nats-url string   NATS server address (default: $NATS_URL or nats://localhost:4222)")
	fmt.Fprintln(os.Stderr, "  --dry-run           compute diffs without writing (default: false)")
	fmt.Fprintln(os.Stderr, "  --debug             verbose structured logging (default: false)")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	for _, cmd := range registry {
		fmt.Fprintf(os.Stderr, "  %-30s %s\n", cmd.Name(), cmd.Help())
		for _, sub := range cmd.Subcommands() {
			fmt.Fprintf(os.Stderr, "    %-28s %s\n", sub.Name(), sub.Help())
		}
	}
}
