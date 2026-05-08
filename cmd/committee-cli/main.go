// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
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

	natsURL := env.Get("NATS_URL", "nats://localhost:4222")
	client, err := nats.NewClient(ctx, nats.Config{
		URL:           natsURL,
		Timeout:       10 * time.Second,
		MaxReconnect:  3,
		ReconnectWait: 2 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
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

func printUsage(w io.Writer, registry map[string]commands.Command) {
	fmt.Fprintln(w, "usage: committee-cli <command> <subcommand> [subcommand flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "environment variables:")
	fmt.Fprintln(w, "  NATS_URL    NATS server address (default: nats://localhost:4222)")
	fmt.Fprintln(w, "  LOG_LEVEL   Log verbosity, e.g. info (default: debug)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, cmd := range registry {
		fmt.Fprintf(w, "  %-30s %s\n", cmd.Name(), cmd.Help())
		for _, sub := range cmd.Subcommands() {
			fmt.Fprintf(w, "    %-28s %s\n", sub.Name(), sub.Help())
		}
	}
}
