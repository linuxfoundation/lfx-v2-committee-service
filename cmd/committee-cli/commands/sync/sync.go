// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import "github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"

// command is the "sync" command group.
type command struct{}

func (c *command) Name() string { return "sync" }

func (c *command) Help() string {
	return "reconcile committee data attributes against the source of truth in the KV store"
}

func (c *command) Subcommands() map[string]commands.Subcommand {
	return map[string]commands.Subcommand{
		"total-members-attribute":       &totalMembersAttributeSubcommand{},
		"member-project-attribute":      &memberProjectAttributeSubcommand{},
		"members-by-committee-index":    &membersByCommitteeIndexSubcommand{},
		"members-by-organization-index": &membersByOrganizationIndexSubcommand{},
		"reindex-invites":               &reindexInvitesSubcommand{},
	}
}

// NewCommand creates the sync command group.
func NewCommand() commands.Command {
	return &command{}
}
