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
	fgaconstants "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/constants"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// reindexInvitesSubcommand re-publishes all committee invites from NATS KV to both the
// indexer (OpenSearch) and fga-sync (OpenFGA), correcting their access-check objects to
// use the committee_invite FGA type introduced in model v14.
type reindexInvitesSubcommand struct{}

func (s *reindexInvitesSubcommand) Name() string { return "reindex-invites" }

func (s *reindexInvitesSubcommand) Help() string {
	return "re-publish all committee invites from NATS KV to OpenSearch and OpenFGA"
}

func (s *reindexInvitesSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("reindex-invites", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync reindex-invites [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit reindex to invites of a single committee UID")
	sleep := fs.Duration("sleep", 0, "wait between each invite publish (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "log what would be published without actually publishing")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun

	if rc.CommitteeReader == nil {
		return fmt.Errorf("CommitteeReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return fmt.Errorf("publisher is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	invites, err := rc.CommitteeReader.ListAllInvites(ctx)
	if err != nil {
		return fmt.Errorf("failed to list all invites: %w", err)
	}

	stats := commands.NewStats()
	stats.Total = len(invites)
	stats.DryRun = rc.DryRun

	for _, invite := range invites {
		if *committeeUID != "" && invite.CommitteeUID != *committeeUID {
			stats.Skipped++
			continue
		}

		if rc.DryRun {
			slog.InfoContext(ctx, "dry-run: would reindex invite",
				"invite_uid", invite.UID,
				"committee_uid", invite.CommitteeUID,
				"status", invite.Status,
			)
			stats.Updated++
			continue
		}

		failed := false

		if err := publishIndexerMessage(ctx, rc, invite); err != nil {
			slog.WarnContext(ctx, "failed to publish indexer message",
				"error", err,
				"invite_uid", invite.UID,
				"committee_uid", invite.CommitteeUID,
			)
			failed = true
		}

		if err := publishAccessControlMessage(ctx, rc, invite); err != nil {
			slog.WarnContext(ctx, "failed to publish access control message",
				"error", err,
				"invite_uid", invite.UID,
				"committee_uid", invite.CommitteeUID,
			)
			failed = true
		}

		if failed {
			stats.Failed++
		} else {
			slog.DebugContext(ctx, "reindexed invite",
				"invite_uid", invite.UID,
				"committee_uid", invite.CommitteeUID,
				"status", invite.Status,
			)
			stats.Updated++
		}

		if *sleep > 0 {
			time.Sleep(*sleep)
		}
	}

	stats.Log(ctx, "sync reindex-invites")

	if stats.Failed > 0 {
		return fmt.Errorf("%d invite(s) failed to reindex", stats.Failed)
	}
	return nil
}

// publishIndexerMessage re-publishes an invite to the indexer (OpenSearch) with the
// corrected AccessCheckObject pointing to the committee_invite FGA type.
func publishIndexerMessage(ctx context.Context, rc commands.RunContext, invite *model.CommitteeInvite) error {
	public := false
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             invite.UID,
		AccessCheckObject:    fmt.Sprintf("committee_invite:%s", invite.UID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", invite.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", invite.CommitteeUID)},
		SortName:             invite.InviteeEmail,
		NameAndAliases:       []string{invite.InviteeEmail},
		Fulltext:             invite.InviteeEmail,
		Tags:                 invite.Tags(),
		Public:               &public,
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action:         model.ActionUpdated,
		Tags:           invite.Tags(),
		IndexingConfig: indexingConfig,
	}

	built, err := indexerMessage.Build(ctx, invite)
	if err != nil {
		return fmt.Errorf("failed to build indexer message: %w", err)
	}

	return rc.Publisher.Indexer(ctx, constants.IndexCommitteeInviteSubject, built, false)
}

// publishAccessControlMessage re-publishes an invite's FGA tuples to fga-sync.
// It writes:
//   - committee_invite:<uid>#committee@committee:<committeeUID>  (enables auditor from committee)
//   - committee_invite:<uid>#invitee@user:<username>             (when email resolves to an LFID)
func publishAccessControlMessage(ctx context.Context, rc commands.RunContext, invite *model.CommitteeInvite) error {
	data := fgatypes.GenericAccessData{
		UID: invite.UID,
		References: map[string][]string{
			constants.RelationCommittee: {invite.CommitteeUID},
		},
	}

	// Resolve email → LFID username; skip the invitee tuple for unregistered emails.
	// Mirrors the committee member and API-layer invite paths.
	if rc.UserReader != nil {
		if username, err := rc.UserReader.UsernameByEmail(ctx, invite.InviteeEmail); err == nil && username != "" {
			data.Relations = map[string][]string{
				constants.RelationInvitee: {username},
			}
		} else if err != nil {
			slog.DebugContext(ctx, "username lookup failed, invitee tuple skipped",
				"error", err,
				"invite_uid", invite.UID,
			)
		}
	}

	msg := fgatypes.GenericFGAMessage{
		ObjectType: "committee_invite",
		Operation:  "update_access",
		Data:       data,
	}

	return rc.Publisher.Access(ctx, fgaconstants.GenericUpdateAccessSubject, msg, false)
}
