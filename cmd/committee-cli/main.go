// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/sync"
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
			printUsage(registry)
			os.Exit(0)
		default:
			if grp, ok := registry[positionals[0]]; ok {
				if sub, ok := grp.Subcommands()[positionals[1]]; ok {
					_ = sub.Run(ctx, commands.RunContext{Args: []string{"--help"}})
					os.Exit(0)
				}
			}
			fmt.Fprintf(os.Stderr, "unknown command: %s %s\n\n", positionals[0], positionals[1])
			printUsage(registry)
			os.Exit(1)
		}
	}

	logging.InitStructureLogConfig()

	if len(positionals) < 2 {
		printUsage(registry)
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

	natsURL := env.Get("NATS_URL", "nats://localhost:4222")
	client, err := nats.NewClient(ctx, nats.Config{
		URL:           natsURL,
		Timeout:       10 * time.Second,
		MaxReconnect:  3,
		ReconnectWait: 2 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to NATS %s: %w", natsURL, err)
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
		Args:                        parsed.SubArgs,
	}

	return sub.Run(ctx, rc)
}

func buildRegistry() map[string]commands.Command {
	syncCmd := sync.NewCommand()
	return map[string]commands.Command{
		syncCmd.Name(): syncCmd,
	}
}

func printUsage(registry map[string]commands.Command) {
	fmt.Fprintln(os.Stderr, "usage: committee-cli <command> <subcommand> [subcommand flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "environment variables:")
	fmt.Fprintln(os.Stderr, "  NATS_URL    NATS server address (default: nats://localhost:4222)")
	fmt.Fprintln(os.Stderr, "  LOG_LEVEL   Log verbosity, e.g. debug (default: info)")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	for _, cmd := range registry {
		fmt.Fprintf(os.Stderr, "  %-30s %s\n", cmd.Name(), cmd.Help())
		for _, sub := range cmd.Subcommands() {
			fmt.Fprintf(os.Stderr, "    %-28s %s\n", sub.Name(), sub.Help())
		}
	}
}
