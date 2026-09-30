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
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// reindexApplicationsSubcommand re-publishes all committee applications from NATS KV to both the
// indexer (OpenSearch) and fga-sync (OpenFGA), updating their access-check objects to use the
// committee_application FGA type (applicant + auditor from committee visibility).
type reindexApplicationsSubcommand struct{}

func (s *reindexApplicationsSubcommand) Name() string { return "reindex-applications" }

func (s *reindexApplicationsSubcommand) Help() string {
	return "re-publish all committee applications from NATS KV to OpenSearch and OpenFGA"
}

func (s *reindexApplicationsSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("reindex-applications", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync reindex-applications [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit reindex to applications of a single committee UID")
	sleep := fs.Duration("sleep", 0, "wait between each application publish (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", false, "log what would be published without actually publishing")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	rc.DryRun = *dryRun

	if rc.CommitteeApplicationReader == nil {
		return errs.NewUnexpected("CommitteeApplicationReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return errs.NewUnexpected("publisher is not wired in RunContext")
	}

	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	var applications []*model.CommitteeApplication
	var listErr error
	if *committeeUID != "" {
		applications, listErr = rc.CommitteeApplicationReader.ListApplications(ctx, *committeeUID)
	} else {
		applications, listErr = rc.CommitteeApplicationReader.ListAllApplications(ctx)
	}
	if listErr != nil {
		return errs.NewUnexpected("failed to list applications", listErr)
	}

	stats := commands.NewStats()
	stats.Total = len(applications)
	stats.DryRun = rc.DryRun

	for _, application := range applications {
		if rc.DryRun {
			slog.InfoContext(ctx, "dry-run: would reindex application",
				"application_uid", application.UID,
				"committee_uid", application.CommitteeUID,
				"status", application.Status,
				"applicant_email_redacted", application.ApplicantEmail != "",
			)
			stats.Updated++
			continue
		}

		failed := false

		if err := publishApplicationIndexerMsg(ctx, rc, application); err != nil {
			slog.WarnContext(ctx, "failed to publish application indexer message",
				"error", err,
				"application_uid", application.UID,
				"committee_uid", application.CommitteeUID,
			)
			failed = true
		}

		if !failed {
			if err := publishApplicationAccessControlMsg(ctx, rc, application); err != nil {
				slog.WarnContext(ctx, "failed to publish application access control message",
					"error", err,
					"application_uid", application.UID,
					"committee_uid", application.CommitteeUID,
				)
				failed = true
			}
		}

		if failed {
			stats.Failed++
		} else {
			slog.DebugContext(ctx, "reindexed application",
				"application_uid", application.UID,
				"committee_uid", application.CommitteeUID,
				"status", application.Status,
			)
			stats.Updated++
		}

		if *sleep > 0 {
			time.Sleep(*sleep)
		}
	}

	stats.Log(ctx, "sync reindex-applications")

	if stats.Failed > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d application(s) failed to reindex", stats.Failed))
	}
	return nil
}

// publishApplicationIndexerMsg re-publishes an application to the indexer (OpenSearch) with
// AccessCheckObject pointing to the committee_application FGA type.
func publishApplicationIndexerMsg(ctx context.Context, rc commands.RunContext, application *model.CommitteeApplication) error {
	public := false
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             application.UID,
		AccessCheckObject:    fmt.Sprintf("committee_application:%s", application.UID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", application.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", application.CommitteeUID)},
		Fulltext:             application.Message,
		Tags:                 application.Tags(),
		Public:               &public,
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action:         model.ActionUpdated,
		Tags:           application.Tags(),
		IndexingConfig: indexingConfig,
	}

	built, err := indexerMessage.Build(ctx, application)
	if err != nil {
		return fmt.Errorf("failed to build indexer message: %w", err)
	}

	return rc.Publisher.Indexer(ctx, constants.IndexCommitteeApplicationSubject, built, false)
}

// publishApplicationAccessControlMsg re-publishes an application's FGA tuples to fga-sync.
// It writes:
//   - committee_application:<uid>#committee@committee:<committeeUID>  (enables auditor from committee)
//   - committee_application:<uid>#applicant@user:<username>           (when email resolves to an LFID)
func publishApplicationAccessControlMsg(ctx context.Context, rc commands.RunContext, application *model.CommitteeApplication) error {
	data := fgatypes.GenericAccessData{
		UID: application.UID,
		References: map[string][]string{
			constants.RelationCommittee: {application.CommitteeUID},
		},
	}

	// Resolve email → LFID username. An errs.NotFound response means the applicant has no
	// LFID yet — skip the tuple silently (auditor visibility still works). Any other error
	// is a transient infrastructure failure: propagate it so the caller counts this application
	// as failed rather than silently omitting the applicant tuple.
	if rc.UserReader != nil {
		username, lookupErr := rc.UserReader.UsernameByEmail(ctx, application.ApplicantEmail)
		if lookupErr != nil {
			var notFound errs.NotFound
			if errors.As(lookupErr, &notFound) {
				slog.DebugContext(ctx, "applicant has no LFID yet, tuple skipped",
					"application_uid", application.UID,
				)
			} else {
				return fmt.Errorf("username lookup failed for application %s: %w", application.UID, lookupErr)
			}
		} else if username != "" {
			data.Relations = map[string][]string{
				constants.RelationApplicant: {username},
			}
		}
	}

	// ExcludeRelations tells fga-sync not to touch the applicant relation when we have no
	// username to write, preventing it from deleting an already-resolved applicant tuple.
	if data.Relations == nil {
		data.ExcludeRelations = []string{constants.RelationApplicant}
	}

	msg := fgatypes.GenericFGAMessage{
		ObjectType: "committee_application",
		Operation:  "update_access",
		Data:       data,
	}

	return rc.Publisher.UpdateAccess(ctx, msg)
}
