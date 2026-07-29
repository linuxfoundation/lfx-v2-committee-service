// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	internalservice "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

type documentAuditUsersSubcommand struct{}

func (s *documentAuditUsersSubcommand) Name() string { return "document-audit-users" }

func (s *documentAuditUsersSubcommand) Help() string {
	return "backfill created_by/updated_by profiles on committee link folders, links, and documents; re-index OpenSearch"
}

func (s *documentAuditUsersSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("document-audit-users", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync document-audit-users [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	committeeUID := fs.String("committee-uid", "", "limit migration to a single committee UID")
	resourceType := fs.String("resource-type", "", "optional filter: folder, link, or document")
	sleep := fs.Duration("sleep", 0, "wait between each auth-service lookup (e.g. 200ms, 1s)")
	reindexOnly := fs.Bool("reindex-only", false, "re-publish ActionUpdated indexer messages without KV writes (recovery after a partial migration run)")
	update := fs.Bool("update", false, "write KV changes and publish indexer messages (default is preview-only)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	includeFolders, includeLinks, includeDocuments, err := parseDocumentResourceType(*resourceType)
	if err != nil {
		return err
	}

	if rc.DocumentAuditSync == nil {
		return errs.NewUnexpected("DocumentAuditSync is not wired in RunContext")
	}
	if rc.UserReader == nil {
		return errs.NewUnexpected("UserReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return errs.NewUnexpected("Publisher is not wired in RunContext")
	}

	rc.DryRun = !*update
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	runner := &documentAuditUsersRunner{
		store:       rc.DocumentAuditSync,
		userReader:  rc.UserReader,
		publisher:   rc.Publisher,
		dryRun:      rc.DryRun,
		reindexOnly: *reindexOnly,
		sleep:       *sleep,
		stats:       commands.NewStats(),
	}
	runner.stats.DryRun = rc.DryRun

	if err := runner.run(ctx, strings.TrimSpace(*committeeUID), includeFolders, includeLinks, includeDocuments); err != nil {
		return err
	}

	runner.stats.Log(ctx, "sync document-audit-users")
	if runner.stats.Failed > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d resource(s) failed to migrate", runner.stats.Failed))
	}
	return nil
}

type documentAuditUsersRunner struct {
	store       port.DocumentAuditSyncStorage
	userReader  port.UserReader
	publisher   port.CommitteePublisher
	dryRun      bool
	reindexOnly bool
	sleep       time.Duration
	stats       *commands.Stats
}

func (r *documentAuditUsersRunner) run(ctx context.Context, committeeUID string, folders, links, documents bool) error {
	if folders {
		if err := r.migrateFolders(ctx, committeeUID); err != nil {
			return err
		}
	}
	if links {
		if err := r.migrateLinks(ctx, committeeUID); err != nil {
			return err
		}
	}
	if documents {
		if err := r.migrateDocuments(ctx, committeeUID); err != nil {
			return err
		}
	}
	return nil
}

func (r *documentAuditUsersRunner) migrateFolders(ctx context.Context, committeeUID string) error {
	folders, err := r.store.ListAllLinkFolders(ctx, committeeUID)
	if err != nil {
		return errs.NewUnexpected("list folders", err)
	}
	for _, folder := range folders {
		r.stats.Total++
		if r.reindexOnly {
			if err := r.reindexFolder(ctx, folder); err != nil {
				return err
			}
			continue
		}
		if err := r.migrateFolder(ctx, folder); err != nil {
			return err
		}
	}
	return nil
}

func (r *documentAuditUsersRunner) migrateFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error {
	fresh, revision, err := r.store.GetLinkFolder(ctx, folder.CommitteeUID, folder.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to re-read folder before audit backfill",
			"folder_uid", folder.UID, "committee_uid", folder.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	return r.applyAuditUsers(ctx, freshAuditResource{
		resourceType: "folder",
		uid:          fresh.UID,
		committeeUID: fresh.CommitteeUID,
		name:         fresh.Name,
		createdBy:    fresh.CreatedBy,
		updatedBy:    fresh.UpdatedBy,
	}, func(createdBy, updatedBy *model.CommitteeUser) error {
		fresh.CreatedBy = createdBy
		fresh.UpdatedBy = updatedBy
		if r.dryRun {
			return nil
		}
		if err := publishLinkFolderIndexerMessage(ctx, r.publisher, model.ActionUpdated, fresh); err != nil {
			return err
		}
		return r.store.UpdateLinkFolder(ctx, fresh, revision)
	})
}

func (r *documentAuditUsersRunner) reindexFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error {
	fresh, _, err := r.store.GetLinkFolder(ctx, folder.CommitteeUID, folder.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to re-read folder before reindex",
			"folder_uid", folder.UID, "committee_uid", folder.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	if r.dryRun {
		slog.InfoContext(ctx, "dry-run: would reindex folder",
			"folder_uid", fresh.UID, "committee_uid", fresh.CommitteeUID)
		r.stats.Updated++
		return nil
	}
	if err := publishLinkFolderIndexerMessage(ctx, r.publisher, model.ActionUpdated, fresh); err != nil {
		slog.WarnContext(ctx, "failed to reindex folder",
			"folder_uid", fresh.UID, "committee_uid", fresh.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	r.stats.Updated++
	return nil
}

func (r *documentAuditUsersRunner) migrateLinks(ctx context.Context, committeeUID string) error {
	links, err := r.store.ListAllLinks(ctx, committeeUID)
	if err != nil {
		return errs.NewUnexpected("list links", err)
	}
	for _, link := range links {
		r.stats.Total++
		if r.reindexOnly {
			if err := r.reindexLink(ctx, link); err != nil {
				return err
			}
			continue
		}
		if err := r.migrateLink(ctx, link); err != nil {
			return err
		}
	}
	return nil
}

func (r *documentAuditUsersRunner) migrateLink(ctx context.Context, link *model.CommitteeLink) error {
	fresh, revision, err := r.store.GetLink(ctx, link.CommitteeUID, link.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to re-read link before audit backfill",
			"link_uid", link.UID, "committee_uid", link.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	return r.applyAuditUsers(ctx, freshAuditResource{
		resourceType: "link",
		uid:          fresh.UID,
		committeeUID: fresh.CommitteeUID,
		name:         fresh.Name,
		createdBy:    fresh.CreatedBy,
		updatedBy:    fresh.UpdatedBy,
	}, func(createdBy, updatedBy *model.CommitteeUser) error {
		fresh.CreatedBy = createdBy
		fresh.UpdatedBy = updatedBy
		if r.dryRun {
			return nil
		}
		if err := publishLinkIndexerMessage(ctx, r.publisher, model.ActionUpdated, fresh); err != nil {
			return err
		}
		return r.store.UpdateLink(ctx, fresh, revision)
	})
}

func (r *documentAuditUsersRunner) reindexLink(ctx context.Context, link *model.CommitteeLink) error {
	fresh, _, err := r.store.GetLink(ctx, link.CommitteeUID, link.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to re-read link before reindex",
			"link_uid", link.UID, "committee_uid", link.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	if r.dryRun {
		slog.InfoContext(ctx, "dry-run: would reindex link",
			"link_uid", fresh.UID, "committee_uid", fresh.CommitteeUID)
		r.stats.Updated++
		return nil
	}
	if err := publishLinkIndexerMessage(ctx, r.publisher, model.ActionUpdated, fresh); err != nil {
		slog.WarnContext(ctx, "failed to reindex link",
			"link_uid", fresh.UID, "committee_uid", fresh.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	r.stats.Updated++
	return nil
}

func (r *documentAuditUsersRunner) migrateDocuments(ctx context.Context, committeeUID string) error {
	docs, err := r.store.ListAllDocuments(ctx, committeeUID)
	if err != nil {
		return errs.NewUnexpected("list documents", err)
	}
	for _, doc := range docs {
		r.stats.Total++
		if r.reindexOnly {
			if err := r.reindexDocument(ctx, doc); err != nil {
				return err
			}
			continue
		}
		if err := r.migrateDocument(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func (r *documentAuditUsersRunner) migrateDocument(ctx context.Context, doc *model.CommitteeDocument) error {
	fresh, revision, err := r.store.GetDocumentMetadata(ctx, doc.CommitteeUID, doc.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to re-read document before audit backfill",
			"document_uid", doc.UID, "committee_uid", doc.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	return r.applyAuditUsers(ctx, freshAuditResource{
		resourceType: "document",
		uid:          fresh.UID,
		committeeUID: fresh.CommitteeUID,
		name:         fresh.Name,
		createdBy:    fresh.CreatedBy,
		updatedBy:    fresh.UpdatedBy,
	}, func(createdBy, updatedBy *model.CommitteeUser) error {
		fresh.CreatedBy = createdBy
		fresh.UpdatedBy = updatedBy
		if r.dryRun {
			return nil
		}
		if err := publishDocumentIndexerMessage(ctx, r.publisher, model.ActionUpdated, fresh); err != nil {
			return err
		}
		return r.store.UpdateDocumentMetadata(ctx, fresh, revision)
	})
}

func (r *documentAuditUsersRunner) reindexDocument(ctx context.Context, doc *model.CommitteeDocument) error {
	fresh, _, err := r.store.GetDocumentMetadata(ctx, doc.CommitteeUID, doc.UID)
	if err != nil {
		slog.WarnContext(ctx, "failed to re-read document before reindex",
			"document_uid", doc.UID, "committee_uid", doc.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	if r.dryRun {
		slog.InfoContext(ctx, "dry-run: would reindex document",
			"document_uid", fresh.UID, "committee_uid", fresh.CommitteeUID)
		r.stats.Updated++
		return nil
	}
	if err := publishDocumentIndexerMessage(ctx, r.publisher, model.ActionUpdated, fresh); err != nil {
		slog.WarnContext(ctx, "failed to reindex document",
			"document_uid", fresh.UID, "committee_uid", fresh.CommitteeUID, "error", err)
		r.stats.Failed++
		return nil
	}
	r.stats.Updated++
	return nil
}

type freshAuditResource struct {
	resourceType string
	uid          string
	committeeUID string
	name         string
	createdBy    *model.CommitteeUser
	updatedBy    *model.CommitteeUser
}

func (r *documentAuditUsersRunner) applyAuditUsers(
	ctx context.Context,
	res freshAuditResource,
	apply func(createdBy, updatedBy *model.CommitteeUser) error,
) error {
	createdNeeds := model.AuditUserNeedsMigration(res.createdBy)
	updatedNeeds := res.updatedBy != nil && model.AuditUserNeedsMigration(res.updatedBy)
	if !createdNeeds && !updatedNeeds {
		r.stats.Skipped++
		return nil
	}

	var newCreated, newUpdated *model.CommitteeUser

	if createdNeeds {
		profile, ok := r.resolveAuditProfile(ctx, res, model.AuditCreatorUsername(res.createdBy))
		if err := r.throttle(ctx); err != nil {
			return err
		}
		if !ok {
			return nil
		}
		newCreated = profile
	} else {
		newCreated = res.createdBy
	}

	switch {
	case updatedNeeds && res.updatedBy != nil &&
		strings.TrimSpace(res.updatedBy.Username) == model.AuditCreatorUsername(newCreated):
		newUpdated = model.CloneCommitteeUser(newCreated)
	case updatedNeeds && res.updatedBy != nil:
		profile, ok := r.resolveAuditProfile(ctx, res, strings.TrimSpace(res.updatedBy.Username))
		if err := r.throttle(ctx); err != nil {
			return err
		}
		if !ok {
			return nil
		}
		newUpdated = profile
	case res.updatedBy != nil:
		newUpdated = res.updatedBy
	case newCreated != nil:
		newUpdated = model.CloneCommitteeUser(newCreated)
	}

	slog.InfoContext(ctx, "document audit user drift detected",
		"resource_type", res.resourceType,
		"resource_uid", res.uid,
		"committee_uid", res.committeeUID,
		"name", res.name,
		"dry_run", r.dryRun,
	)

	if r.dryRun {
		r.stats.Updated++
		return nil
	}

	if err := apply(newCreated, newUpdated); err != nil {
		slog.WarnContext(ctx, "failed to migrate document audit users",
			"resource_type", res.resourceType,
			"resource_uid", res.uid,
			"committee_uid", res.committeeUID,
			"error", err,
		)
		r.stats.Failed++
		return nil
	}

	r.stats.Updated++
	return nil
}

func (r *documentAuditUsersRunner) resolveAuditProfile(ctx context.Context, res freshAuditResource, username string) (*model.CommitteeUser, bool) {
	profile := internalservice.ResolveAuditUserProfile(ctx, r.userReader, username)
	if profile == nil || model.AuditUserNeedsMigration(profile) {
		slog.WarnContext(ctx, "failed to resolve audit user profile for migration",
			"resource_type", res.resourceType,
			"resource_uid", res.uid,
			"committee_uid", res.committeeUID,
			"username", redaction.Redact(username),
		)
		r.stats.Failed++
		return nil, false
	}
	return profile, true
}

func (r *documentAuditUsersRunner) throttle(ctx context.Context) error {
	if r.sleep <= 0 {
		return nil
	}
	return documentAuditSleep(ctx, r.sleep)
}

func parseDocumentResourceType(raw string) (folders, links, documents bool, err error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return true, true, true, nil
	}
	switch raw {
	case "folder":
		return true, false, false, nil
	case "link":
		return false, true, false, nil
	case "document":
		return false, false, true, nil
	default:
		return false, false, false, errs.NewValidation(fmt.Sprintf("invalid --resource-type %q: want folder, link, or document", raw))
	}
}

func documentAuditSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func publishLinkIndexerMessage(ctx context.Context, publisher port.CommitteePublisher, action model.MessageAction, link *model.CommitteeLink) error {
	tags := link.Tags()
	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
		Tags:   tags,
	}
	parentRefs := []string{fmt.Sprintf("committee:%s", link.CommitteeUID)}
	if link.FolderUID != nil && *link.FolderUID != "" {
		parentRefs = append(parentRefs, fmt.Sprintf("committee_link_folder:%s", *link.FolderUID))
	}
	indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
		ObjectID:             link.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", link.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", link.CommitteeUID),
		HistoryCheckRelation: "auditor",
		SortName:             link.Name,
		NameAndAliases:       []string{link.Name},
		ParentRefs:           parentRefs,
		Tags:                 tags,
		Fulltext:             fmt.Sprintf("%s %s %s", link.Name, link.Description, link.URL),
	}
	built, err := indexerMessage.Build(ctx, link)
	if err != nil {
		return err
	}
	return publisher.Indexer(ctx, constants.IndexCommitteeLinkSubject, built, false)
}

func publishLinkFolderIndexerMessage(ctx context.Context, publisher port.CommitteePublisher, action model.MessageAction, folder *model.CommitteeLinkFolder) error {
	tags := folder.Tags()
	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
		Tags:   tags,
	}
	indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
		ObjectID:             folder.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", folder.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", folder.CommitteeUID),
		HistoryCheckRelation: "auditor",
		SortName:             folder.Name,
		NameAndAliases:       []string{folder.Name},
		ParentRefs:           []string{fmt.Sprintf("committee:%s", folder.CommitteeUID)},
		Tags:                 tags,
		Fulltext:             folder.Name,
	}
	built, err := indexerMessage.Build(ctx, folder)
	if err != nil {
		return err
	}
	return publisher.Indexer(ctx, constants.IndexCommitteeLinkFolderSubject, built, false)
}

func publishDocumentIndexerMessage(ctx context.Context, publisher port.CommitteePublisher, action model.MessageAction, doc *model.CommitteeDocument) error {
	tags := doc.Tags()
	indexerMessage := model.CommitteeIndexerMessage{
		Action: action,
		Tags:   tags,
	}
	indexerMessage.IndexingConfig = &indexerTypes.IndexingConfig{
		ObjectID:             doc.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", doc.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", doc.CommitteeUID),
		HistoryCheckRelation: "auditor",
		SortName:             doc.Name,
		NameAndAliases:       []string{doc.Name},
		ParentRefs:           []string{fmt.Sprintf("committee:%s", doc.CommitteeUID)},
		Tags:                 tags,
		Fulltext:             fmt.Sprintf("%s %s %s", doc.Name, doc.Description, doc.FileName),
	}
	built, err := indexerMessage.Build(ctx, doc)
	if err != nil {
		return err
	}
	return publisher.Indexer(ctx, constants.IndexCommitteeDocumentSubject, built, false)
}
