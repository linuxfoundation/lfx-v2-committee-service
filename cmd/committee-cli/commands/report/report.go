// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package report implements the "report" command group: read-only data
// inventories/comparisons that emit a result without writing anything back
// to the KV store.
package report

import "github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"

// command is the "report" command group.
type command struct{}

func (c *command) Name() string { return "report" }

func (c *command) Help() string {
	return "generate read-only data reports (no writes to the KV store)"
}

func (c *command) Subcommands() map[string]commands.Subcommand {
	return map[string]commands.Subcommand{
		"board-seats": &boardSeatsSubcommand{},
	}
}

// NewCommand creates the report command group.
func NewCommand() commands.Command {
	return &command{}
}
