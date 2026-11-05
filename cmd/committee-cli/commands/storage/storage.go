// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package storage implements the "storage" command group: tooling for moving committee data from the
// NATS KV store to PostgreSQL and checking that the two match.
package storage

import "github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"

// command is the "storage" command group.
type command struct{}

func (c *command) Name() string { return "storage" }

func (c *command) Help() string {
	return "copy committee data from the NATS KV store to PostgreSQL and verify the copy"
}

func (c *command) Subcommands() map[string]commands.Subcommand {
	return map[string]commands.Subcommand{
		"migrate": &migrateSubcommand{},
		"verify":  &verifySubcommand{},
	}
}

// NewCommand creates the storage command group.
func NewCommand() commands.Command {
	return &command{}
}
