// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/opensearch"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/env"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	logging "github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
)

const settingsIndexPageSize = 500

// repairSettingsIndexSubcommand brings every committee_settings document in the search index onto
// the committee:<uid>#auditor anchor with public=false, and removes settings documents whose
// settings record no longer exists in storage. It is a dry run unless --apply is given; --verify
// only reads the index and reports documents that are still wrong.
type repairSettingsIndexSubcommand struct {
	// openSearch overrides the client built from --opensearch-url (tests only).
	openSearch *opensearchgo.Client
}

func (s *repairSettingsIndexSubcommand) Name() string { return "repair-settings-index" }

func (s *repairSettingsIndexSubcommand) Help() string {
	return "re-anchor committee settings index documents on committee:<uid>#auditor (non-public) and delete orphans; dry run unless --apply, --verify reports only"
}

// RunsOffline lets --verify run without NATS or storage: it only reads OpenSearch.
func (s *repairSettingsIndexSubcommand) RunsOffline(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--verify", "-verify", "--verify=true", "-verify=true":
			return true
		}
	}
	return false
}

// settingsIndexDoc is the access-control view of one live committee_settings index document.
type settingsIndexDoc struct {
	ObjectID             string `json:"object_id"`
	AccessCheckObject    string `json:"access_check_object"`
	AccessCheckRelation  string `json:"access_check_relation"`
	HistoryCheckObject   string `json:"history_check_object"`
	HistoryCheckRelation string `json:"history_check_relation"`
	Public               bool   `json:"public"`
}

type settingsRepairStats struct {
	reindexed, noSettings, orphansDeleted, unresolved, failed int
}

func (s *repairSettingsIndexSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet(s.Name(), flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync repair-settings-index [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Pass 1 re-publishes every stored committee's settings with the committee:<uid>#auditor anchor and public=false.")
		_, _ = fmt.Fprintln(fs.Output(), "Pass 2 deletes live settings documents whose settings record no longer exists in storage.")
		_, _ = fmt.Fprintf(fs.Output(), "Run --verify after the indexer has processed the messages; it exits non-zero while any document is wrong.\n\nflags:\n")
		fs.PrintDefaults()
	}
	apply := fs.Bool("apply", false, "publish index updates and deletes (default is a dry run)")
	verify := fs.Bool("verify", false, "only read the index and report settings documents that are not yet repaired")
	committeeUID := fs.String("committee-uid", "", "limit both passes to a single committee UID")
	sleep := fs.Duration("sleep", 0, "wait between each publish (e.g. 200ms, 1s)")
	openSearchURL := fs.String("opensearch-url", strings.TrimSpace(env.Get("OPENSEARCH_URL", defaultOpenSearchURL)), "override OPENSEARCH_URL")
	openSearchIndex := fs.String("opensearch-index", strings.TrimSpace(env.Get("OPENSEARCH_INDEX", defaultOpenSearchIndex)), "override OPENSEARCH_INDEX")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *apply && *verify {
		return errs.NewValidation("--apply and --verify cannot be combined")
	}

	client := s.openSearch
	if client == nil {
		url := strings.TrimSpace(*openSearchURL)
		if url == "" {
			url = defaultOpenSearchURL
		}
		var err error
		if client, err = opensearch.NewClient(url); err != nil {
			return errs.NewUnexpected("failed to create OpenSearch client", err)
		}
	}

	if *verify {
		// --verify runs offline, before main sets up logging. It writes nothing to stdout, so the
		// structured logger (and LOG_LEVEL) can be set up here.
		logging.InitStructureLogConfig()
		return verifySettingsIndex(ctx, client, *openSearchIndex, *committeeUID)
	}
	docs, err := listSettingsIndexDocs(ctx, client, *openSearchIndex, *committeeUID)
	if err != nil {
		return err
	}

	if rc.CommitteeReader == nil {
		return errs.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.Publisher == nil {
		return errs.NewUnexpected("publisher is not wired in RunContext")
	}
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")
	dryRun := !*apply
	stats := &settingsRepairStats{}
	pause := func() {
		if *sleep > 0 && !dryRun {
			time.Sleep(*sleep)
		}
	}

	// Pass 1: storage is authoritative for every committee that still exists.
	uids := []string{*committeeUID}
	if *committeeUID == "" {
		if uids, err = rc.CommitteeReader.ListAllUIDs(ctx); err != nil {
			return errs.NewUnexpected("failed to list committee UIDs", err)
		}
	}
	reindexed := make(map[string]bool, len(uids))
	for _, uid := range uids {
		base, _, baseErr := rc.CommitteeReader.GetBase(ctx, uid)
		if baseErr != nil {
			var notFound errs.NotFound
			if errors.As(baseErr, &notFound) {
				continue // a deleted committee's leftover document is handled by pass 2
			}
			slog.WarnContext(ctx, "repair-settings-index: failed to fetch committee base, skipping", "committee_uid", uid, "error", baseErr)
			stats.failed++
			continue
		}
		settings, _, settingsErr := rc.CommitteeReader.GetSettings(ctx, uid)
		if settingsErr != nil {
			var notFound errs.NotFound
			if errors.As(settingsErr, &notFound) {
				stats.noSettings++
				continue
			}
			slog.WarnContext(ctx, "repair-settings-index: failed to fetch committee settings, skipping", "committee_uid", uid, "error", settingsErr)
			stats.failed++
			continue
		}
		reindexed[uid] = true
		if dryRun {
			stats.reindexed++
			continue
		}
		if err := publishSettingsIndexerMessage(ctx, rc, base, settings); err != nil {
			slog.WarnContext(ctx, "repair-settings-index: failed to publish settings update", "committee_uid", uid, "error", err)
			stats.failed++
			continue
		}
		stats.reindexed++
		pause()
	}

	// Pass 2: live index documents that pass 1 did not cover. The settings record is the source
	// of its document, so a document is deleted when its settings read as NotFound and its
	// committee read either succeeded or was NotFound too. A storage error never deletes, and a
	// document whose settings still exist without a committee is reported for review.
	for _, doc := range docs {
		uid := doc.ObjectID
		if reindexed[uid] {
			continue
		}
		_, _, baseErr := rc.CommitteeReader.GetBase(ctx, uid)
		_, _, settingsErr := rc.CommitteeReader.GetSettings(ctx, uid)
		var notFound errs.NotFound
		baseMissing := errors.As(baseErr, &notFound)
		settingsMissing := errors.As(settingsErr, &notFound)
		switch {
		case (baseErr != nil && !baseMissing) || (settingsErr != nil && !settingsMissing):
			slog.WarnContext(ctx, "repair-settings-index: cannot confirm orphan, leaving document",
				"committee_uid", uid, "base_error", baseErr, "settings_error", settingsErr)
			stats.failed++
			continue
		case !settingsMissing:
			slog.WarnContext(ctx, "repair-settings-index: settings still exist but pass 1 did not reindex them, leaving document for review",
				"committee_uid", uid, "committee_found", !baseMissing, "public", doc.Public)
			stats.unresolved++
			continue
		}
		// The settings record is gone: the document is an orphan whether or not the committee remains.
		if dryRun {
			slog.InfoContext(ctx, "dry-run: would delete orphaned settings document", "committee_uid", uid)
			stats.orphansDeleted++
			continue
		}
		if err := publishSettingsDeleteMessage(ctx, rc, uid); err != nil {
			slog.WarnContext(ctx, "repair-settings-index: failed to publish orphan delete", "committee_uid", uid, "error", err)
			stats.failed++
			continue
		}
		slog.InfoContext(ctx, "repair-settings-index: deleted orphaned settings document", "committee_uid", uid)
		stats.orphansDeleted++
		pause()
	}

	slog.InfoContext(ctx, "sync repair-settings-index complete",
		"dry_run", dryRun,
		"index_documents", len(docs),
		"reindexed", stats.reindexed,
		"committees_without_settings", stats.noSettings,
		"orphans_deleted", stats.orphansDeleted,
		"unresolved", stats.unresolved,
		"failed", stats.failed,
	)
	if stats.failed > 0 || stats.unresolved > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d committee settings document(s) could not be repaired (%d failed, %d unresolved)",
			stats.failed+stats.unresolved, stats.failed, stats.unresolved))
	}
	return nil
}

// settingsNotRepairedScript matches a document that is not on committee:<object_id>#auditor for
// both access and history, or that is public. A missing public field counts as non-public.
const settingsNotRepairedScript = `
def id = doc['object_id'].size() == 0 ? '' : doc['object_id'].value;
def want = 'committee:' + id;
boolean repaired = doc['access_check_object'].size() > 0 && doc['access_check_object'].value == want
  && doc['history_check_object'].size() > 0 && doc['history_check_object'].value == want
  && doc['access_check_relation'].size() > 0 && doc['access_check_relation'].value == 'auditor'
  && doc['history_check_relation'].size() > 0 && doc['history_check_relation'].value == 'auditor'
  && (doc['public'].size() == 0 || doc['public'].value == false);
return !repaired;`

// verifyListLimit bounds how many unrepaired documents --verify logs; the count is always exact.
const verifyListLimit = 1000

// verifySettingsIndex counts every current settings document that is not repaired, including
// duplicate latest copies the indexer has not cleaned up yet, with one exact query rather than a
// paged scan. It returns an error while any remain, so it can gate a rollout.
func verifySettingsIndex(ctx context.Context, client *opensearchgo.Client, index, committeeUID string) error {
	all, err := searchSettingsIndex(ctx, client, index, map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query":            settingsIndexQuery(committeeUID),
	})
	if err != nil {
		return err
	}
	query := settingsIndexQuery(committeeUID)
	boolQuery := query["bool"].(map[string]any)
	boolQuery["filter"] = append(boolQuery["filter"].([]any), map[string]any{
		"script": map[string]any{"script": map[string]any{"lang": "painless", "source": settingsNotRepairedScript}},
	})
	wrong, err := searchSettingsIndex(ctx, client, index, map[string]any{
		"size":             verifyListLimit,
		"track_total_hits": true,
		"query":            query,
		"sort":             []any{map[string]any{"object_id": "asc"}},
		"_source":          settingsIndexSourceFields,
	})
	if err != nil {
		return err
	}
	public := 0
	for _, doc := range wrong.docs {
		if doc.Public {
			public++
		}
		slog.InfoContext(ctx, "verify: settings document not repaired",
			"committee_uid", doc.ObjectID,
			"public", doc.Public,
			"access_check_object_type", objectType(doc.AccessCheckObject),
			"history_check_object_type", objectType(doc.HistoryCheckObject),
		)
	}
	slog.InfoContext(ctx, "sync repair-settings-index verify complete",
		"index_documents", all.total, "not_repaired", wrong.total, "listed", len(wrong.docs), "public_listed", public)
	if wrong.total > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d of %d committee settings document(s) are not on committee:<uid>#auditor with public=false",
			wrong.total, all.total))
	}
	return nil
}

func objectType(ref string) string {
	if ref == "" {
		return "(none)"
	}
	kind, _, _ := strings.Cut(ref, ":")
	return kind
}

// publishSettingsDeleteMessage removes a committee_settings document from the index, the same
// message the committee delete path sends.
func publishSettingsDeleteMessage(ctx context.Context, rc commands.RunContext, uid string) error {
	msg := model.CommitteeIndexerMessage{Action: model.ActionDeleted}
	built, err := msg.Build(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to build settings delete message: %w", err)
	}
	return rc.Publisher.Indexer(ctx, constants.IndexCommitteeSettingsSubject, built, false)
}

var settingsIndexSourceFields = []string{"object_id", "access_check_object", "access_check_relation",
	"history_check_object", "history_check_relation", "public"}

// settingsIndexQuery matches every current (latest) committee_settings document, optionally for a
// single committee. Older copies stay in the index with latest=false, and delete tombstones
// (documents with deleted_at) carry no data.
func settingsIndexQuery(committeeUID string) map[string]any {
	filter := []any{
		map[string]any{"term": map[string]any{"object_type": string(model.ResourceCommitteeSettings)}},
		map[string]any{"term": map[string]any{"latest": true}},
	}
	if committeeUID != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"object_id": committeeUID}})
	}
	return map[string]any{"bool": map[string]any{
		"filter":   filter,
		"must_not": []any{map[string]any{"exists": map[string]any{"field": "deleted_at"}}},
	}}
}

// listSettingsIndexDocs returns the distinct committees that have a current settings document,
// for pass 2. Pages use search_after on object_id so the scan is not bounded by
// max_result_window; duplicate copies of one committee's document collapse into one entry.
func listSettingsIndexDocs(ctx context.Context, client *opensearchgo.Client, index, committeeUID string) ([]settingsIndexDoc, error) {
	var docs []settingsIndexDoc
	seen := map[string]bool{}
	var after []any
	for {
		request := map[string]any{
			"size":    settingsIndexPageSize,
			"query":   settingsIndexQuery(committeeUID),
			"sort":    []any{map[string]any{"object_id": "asc"}},
			"_source": settingsIndexSourceFields,
		}
		if after != nil {
			request["search_after"] = after
		}
		page, err := searchSettingsIndex(ctx, client, index, request)
		if err != nil {
			return nil, err
		}
		for _, doc := range page.docs {
			if doc.ObjectID == "" || seen[doc.ObjectID] {
				continue
			}
			seen[doc.ObjectID] = true
			docs = append(docs, doc)
		}
		if len(page.docs) < settingsIndexPageSize {
			return docs, nil
		}
		after = []any{page.docs[len(page.docs)-1].ObjectID}
	}
}

type settingsSearchResult struct {
	docs  []settingsIndexDoc
	total int64
}

// searchSettingsIndex runs one search and refuses incomplete results: OpenSearch can answer
// HTTP 200 with only some shards, which would end a scan early or under-count --verify.
func searchSettingsIndex(ctx context.Context, client *opensearchgo.Client, index string, request map[string]any) (settingsSearchResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return settingsSearchResult{}, errs.NewUnexpected("marshal OpenSearch settings query", err)
	}
	res, err := client.Search(
		client.Search.WithContext(ctx),
		client.Search.WithIndex(index),
		client.Search.WithAllowPartialSearchResults(false),
		client.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return settingsSearchResult{}, errs.NewUnexpected("OpenSearch settings query failed", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.IsError() {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return settingsSearchResult{}, errs.NewUnexpected(fmt.Sprintf("OpenSearch settings query error %s: %s", res.Status(), raw))
	}
	var parsed struct {
		TimedOut        bool `json:"timed_out"`
		TerminatedEarly bool `json:"terminated_early"`
		Shards          struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source settingsIndexDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return settingsSearchResult{}, errs.NewUnexpected("decode OpenSearch settings response", err)
	}
	if parsed.TimedOut || parsed.TerminatedEarly || parsed.Shards.Failed > 0 {
		return settingsSearchResult{}, errs.NewUnexpected(fmt.Sprintf(
			"OpenSearch returned incomplete settings results (timed_out=%t, terminated_early=%t, failed_shards=%d)",
			parsed.TimedOut, parsed.TerminatedEarly, parsed.Shards.Failed))
	}
	result := settingsSearchResult{total: parsed.Hits.Total.Value, docs: make([]settingsIndexDoc, 0, len(parsed.Hits.Hits))}
	for _, hit := range parsed.Hits.Hits {
		result.docs = append(result.docs, hit.Source)
	}
	return result, nil
}

var _ commands.OfflineSubcommand = (*repairSettingsIndexSubcommand)(nil)
