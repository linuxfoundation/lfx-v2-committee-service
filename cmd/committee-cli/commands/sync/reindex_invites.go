// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
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
		return errs.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return errs.NewUnexpected("publisher is not wired in RunContext")
	}
	if rc.CommitteeInviteWriter == nil {
		return errs.NewUnexpected("CommitteeInviteWriter is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	var invites []*model.CommitteeInvite
	var listErr error
	if *committeeUID != "" {
		invites, listErr = rc.CommitteeReader.ListInvites(ctx, *committeeUID)
	} else {
		invites, listErr = rc.CommitteeReader.ListAllInvites(ctx)
	}
	if listErr != nil {
		return errs.NewUnexpected("failed to list invites", listErr)
	}

	stats := commands.NewStats()
	stats.Total = len(invites)
	stats.DryRun = rc.DryRun

	// Cache per-committee derived fields to avoid redundant NATS KV reads during batch reindex.
	// fetched=false means GetBase failed; in that case no fields on the invite are modified.
	type committeeSnapshot struct {
		name                 string
		organizationRequired bool
		fetched              bool
		settingsFetched      bool
	}
	committeeCache := make(map[string]committeeSnapshot)

	lookupCommittee := func(committeeUID string) committeeSnapshot {
		if snap, ok := committeeCache[committeeUID]; ok {
			return snap
		}
		snap := committeeSnapshot{}
		base, _, err := rc.CommitteeReader.GetBase(ctx, committeeUID)
		if err != nil {
			slog.WarnContext(ctx, "reindex-invites: failed to fetch committee base",
				"committee_uid", committeeUID, "error", err)
			committeeCache[committeeUID] = snap
			return snap
		}
		snap.fetched = true
		snap.name = base.Name
		settings, _, settingsErr := rc.CommitteeReader.GetSettings(ctx, committeeUID)
		if settingsErr != nil {
			// Leave OrganizationRequired unchanged rather than clobbering a correctly-stored
			// value with one derived from a transient settings failure.
			slog.WarnContext(ctx, "reindex-invites: failed to fetch committee settings — OrganizationRequired will not be updated",
				"committee_uid", committeeUID, "error", settingsErr)
		} else {
			snap.settingsFetched = true
			businessEmailRequired := settings != nil && settings.BusinessEmailRequired
			snap.organizationRequired = base.EnableVoting || businessEmailRequired
		}
		committeeCache[committeeUID] = snap
		return snap
	}

	for _, invite := range invites {
		snap := lookupCommittee(invite.CommitteeUID)

		// Only modify invite fields when the committee lookup succeeded, to avoid
		// corrupting correctly-set values on invites whose committee is temporarily unreachable.
		needsKVUpdate := false
		if snap.fetched {
			if invite.CommitteeName == "" && snap.name != "" {
				invite.CommitteeName = snap.name
				needsKVUpdate = true
			}
			if snap.settingsFetched && invite.OrganizationRequired != snap.organizationRequired {
				invite.OrganizationRequired = snap.organizationRequired
				needsKVUpdate = true
			}
		}

		if rc.DryRun {
			slog.InfoContext(ctx, "dry-run: would reindex invite",
				"invite_uid", invite.UID,
				"committee_uid", invite.CommitteeUID,
				"committee_name", invite.CommitteeName,
				"organization_required", invite.OrganizationRequired,
				"kv_update_needed", needsKVUpdate,
				"status", invite.Status,
			)
			stats.Updated++
			continue
		}

		failed := false

		if needsKVUpdate {
			freshInvite, rev, getErr := rc.CommitteeReader.GetInvite(ctx, invite.UID)
			if getErr != nil {
				slog.WarnContext(ctx, "failed to fetch invite revision for KV update",
					"error", getErr,
					"invite_uid", invite.UID,
				)
				failed = true
			} else {
				if freshInvite.CommitteeName == "" && snap.name != "" {
					freshInvite.CommitteeName = snap.name
				}
				if snap.settingsFetched {
					freshInvite.OrganizationRequired = snap.organizationRequired
				}
				if updateErr := rc.CommitteeInviteWriter.UpdateInvite(ctx, freshInvite, rev); updateErr != nil {
					slog.WarnContext(ctx, "failed to update invite in NATS KV",
						"error", updateErr,
						"invite_uid", invite.UID,
					)
					failed = true
				} else {
					invite = freshInvite
				}
			}
		}

		if !failed {
			if err := publishIndexerMessage(ctx, rc, invite); err != nil {
				slog.WarnContext(ctx, "failed to publish indexer message",
					"error", err,
					"invite_uid", invite.UID,
					"committee_uid", invite.CommitteeUID,
				)
				failed = true
			}
		}

		if !failed {
			if err := publishAccessControlMessage(ctx, rc, invite); err != nil {
				slog.WarnContext(ctx, "failed to publish access control message",
					"error", err,
					"invite_uid", invite.UID,
					"committee_uid", invite.CommitteeUID,
				)
				failed = true
			}
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
		return errs.NewUnexpected(fmt.Sprintf("%d invite(s) failed to reindex", stats.Failed))
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

	// Resolve email → LFID username. An errs.NotFound response means the invitee has no
	// LFID yet — skip the tuple silently (auditor visibility still works). Any other error
	// is a transient infrastructure failure: propagate it so the caller counts this invite
	// as failed rather than silently omitting the invitee tuple.
	if rc.UserReader != nil {
		username, lookupErr := rc.UserReader.UsernameByEmail(ctx, invite.InviteeEmail)
		if lookupErr != nil {
			var notFound errs.NotFound
			if errors.As(lookupErr, &notFound) {
				slog.DebugContext(ctx, "invitee has no LFID yet, tuple skipped",
					"invite_uid", invite.UID,
				)
			} else {
				return fmt.Errorf("username lookup failed for invite %s: %w", invite.UID, lookupErr)
			}
		} else if username != "" {
			data.Relations = map[string][]string{
				constants.RelationInvitee: {username},
			}
		}
	}

	// ExcludeRelations tells fga-sync not to touch the invitee relation when we have no
	// username to write, preventing it from deleting an already-resolved invitee tuple.
	if data.Relations == nil {
		data.ExcludeRelations = []string{constants.RelationInvitee}
	}

	msg := fgatypes.GenericFGAMessage{
		ObjectType: "committee_invite",
		Operation:  "update_access",
		Data:       data,
	}

	return rc.Publisher.Access(ctx, fgaconstants.GenericUpdateAccessSubject, msg, false)
}
