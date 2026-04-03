// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var (
	natsURL      = flag.String("nats-url", getEnvOrDefault("NATS_URL", "nats://localhost:4222"), "NATS server URL")
	dryRun       = flag.Bool("dry-run", false, "Preview changes without applying them")
	debug        = flag.Bool("debug", false, "Enable debug logging")
	forceReindex = flag.Bool("force-reindex", false, "Republish indexer messages for all records without modifying KV data (use when data is already migrated but OpenSearch needs updating)")
)

// authToken is read from AUTH_TOKEN env var only — not a flag, to avoid leaking tokens in shell history.
// Set AUTH_TOKEN=<bearer-token> before running.
var authToken = os.Getenv("AUTH_TOKEN")

type migrationStats struct {
	Total   int
	Updated int
	Skipped int
	Failed  int
}

// committeeUserObject is the new format for writers/auditors entries.
type committeeUserObject struct {
	Avatar   string `json:"avatar,omitempty"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
}

// indexerMessage mirrors the CommitteeIndexerMessage envelope expected by the indexer service.
type indexerMessage struct {
	Action         string                       `json:"action"`
	Headers        map[string]string            `json:"headers"`
	Data           any                          `json:"data"`
	IndexingConfig *indexerTypes.IndexingConfig `json:"indexing_config,omitempty"`
}

func main() {
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	slog.InfoContext(ctx, "Starting writers/auditors migration: []string -> []object",
		"nats_url", *natsURL,
		"dry_run", *dryRun,
	)

	nc, err := nats.Connect(*natsURL,
		nats.Timeout(10*time.Second),
		nats.MaxReconnects(3),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	slog.InfoContext(ctx, "Connected to NATS", "url", nc.ConnectedUrl())

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	settingsKV, err := js.KeyValue(ctx, constants.KVBucketNameCommitteeSettings)
	if err != nil {
		return fmt.Errorf("failed to get KV store for bucket %s: %w", constants.KVBucketNameCommitteeSettings, err)
	}

	baseKV, err := js.KeyValue(ctx, constants.KVBucketNameCommittees)
	if err != nil {
		return fmt.Errorf("failed to get KV store for bucket %s: %w", constants.KVBucketNameCommittees, err)
	}

	slog.InfoContext(ctx, "Listing all keys in bucket", "bucket", constants.KVBucketNameCommitteeSettings)
	keys, err := settingsKV.ListKeys(ctx)
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	var uids []string
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") || strings.HasPrefix(key, "slug/") {
			continue
		}
		uids = append(uids, key)
	}

	slog.InfoContext(ctx, "Found committee settings records", "count", len(uids))

	if *dryRun {
		slog.InfoContext(ctx, "DRY RUN MODE - No changes will be made")
	}

	stats := &migrationStats{Total: len(uids)}
	startTime := time.Now()

	for i, uid := range uids {
		err := processRecord(ctx, settingsKV, baseKV, uid, *dryRun, *forceReindex, nc)
		if err != nil {
			if strings.Contains(err.Error(), "already migrated") {
				stats.Skipped++
			} else {
				slog.ErrorContext(ctx, "failed to process record",
					"uid", uid,
					"error", err,
				)
				stats.Failed++
			}
		} else {
			stats.Updated++
		}

		if (i+1)%10 == 0 {
			slog.InfoContext(ctx, "Migration progress",
				"processed", i+1,
				"total", stats.Total,
				"updated", stats.Updated,
				"skipped", stats.Skipped,
				"failed", stats.Failed,
			)
		}
	}

	duration := time.Since(startTime)

	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("Migration Complete!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Total records:    %d\n", stats.Total)
	fmt.Printf("Updated:          %d\n", stats.Updated)
	fmt.Printf("Skipped:          %d (already migrated)\n", stats.Skipped)
	fmt.Printf("Failed:           %d\n", stats.Failed)
	if stats.Total > 0 {
		successRate := float64(stats.Updated+stats.Skipped) / float64(stats.Total) * 100
		fmt.Printf("Success rate:     %.1f%%\n", successRate)
	}
	fmt.Printf("Duration:         %.2fs\n", duration.Seconds())
	if duration.Seconds() > 0 {
		rate := float64(stats.Total) / duration.Seconds()
		fmt.Printf("Rate:             %.1f rec/sec\n", rate)
	}
	fmt.Println(strings.Repeat("=", 50))

	if stats.Failed > 0 {
		return fmt.Errorf("%d records failed to migrate", stats.Failed)
	}

	return nil
}

// processRecord migrates a single committee settings entry.
// It converts writers and auditors from []string to []committeeUserObject if needed.
// Returns "already migrated" error if the record is already in the new format.
func processRecord(ctx context.Context, settingsKV jetstream.KeyValue, baseKV jetstream.KeyValue, uid string, dryRun bool, forceReindex bool, nc *nats.Conn) error {
	entry, err := settingsKV.Get(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to get settings entry: %w", err)
	}

	var settingsMap map[string]interface{}
	if err := json.Unmarshal(entry.Value(), &settingsMap); err != nil {
		return fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	// In force-reindex mode, skip migration checks and just republish the
	// current data to the indexer without modifying KV.
	if forceReindex {
		msg, err := buildIndexerMessage(ctx, uid, settingsMap, baseKV)
		if err != nil {
			return fmt.Errorf("failed to build indexer message: %w", err)
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("failed to marshal indexer message: %w", err)
		}
		if err := nc.Publish(constants.IndexCommitteeSettingsSubject, msgBytes); err != nil {
			return fmt.Errorf("failed to publish index message: %w", err)
		}
		slog.DebugContext(ctx, "reindexed record", "uid", uid)
		return nil
	}

	writersNeedsMigration := needsMigration(settingsMap, "writers")
	auditorsNeedsMigration := needsMigration(settingsMap, "auditors")

	if !writersNeedsMigration && !auditorsNeedsMigration {
		slog.DebugContext(ctx, "record already migrated — writers/auditors already in object format",
			"uid", uid,
		)
		return fmt.Errorf("already migrated")
	}

	if dryRun {
		slog.InfoContext(ctx, "[DRY RUN] would migrate record",
			"uid", uid,
			"writers_needs_migration", writersNeedsMigration,
			"auditors_needs_migration", auditorsNeedsMigration,
		)
		return nil
	}

	now := time.Now().Format(time.RFC3339Nano)

	if writersNeedsMigration {
		settingsMap["writers"] = convertStringsToUserObjects(settingsMap["writers"])
	}
	if auditorsNeedsMigration {
		settingsMap["auditors"] = convertStringsToUserObjects(settingsMap["auditors"])
	}
	settingsMap["updated_at"] = now

	updatedData, err := json.Marshal(settingsMap)
	if err != nil {
		return fmt.Errorf("failed to marshal updated settings: %w", err)
	}

	maxRetries := 3
	var updateErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		_, updateErr = settingsKV.Update(ctx, uid, updatedData, entry.Revision())
		if updateErr == nil {
			break
		}

		if attempt < maxRetries {
			slog.WarnContext(ctx, "update failed, retrying",
				"uid", uid,
				"attempt", attempt,
				"error", updateErr,
			)
			time.Sleep(time.Duration(attempt*100) * time.Millisecond)

			// Refetch for new revision
			entry, err = settingsKV.Get(ctx, uid)
			if err != nil {
				return fmt.Errorf("failed to refetch settings entry: %w", err)
			}

			if err := json.Unmarshal(entry.Value(), &settingsMap); err != nil {
				return fmt.Errorf("failed to unmarshal refetched settings: %w", err)
			}

			// Recompute per-field flags from the refetched data — a concurrent
			// process may have migrated one or both fields since the last attempt.
			writersNeedsMigration = needsMigration(settingsMap, "writers")
			auditorsNeedsMigration = needsMigration(settingsMap, "auditors")

			if !writersNeedsMigration && !auditorsNeedsMigration {
				slog.DebugContext(ctx, "already migrated by concurrent process", "uid", uid)
				return fmt.Errorf("already migrated")
			}

			if writersNeedsMigration {
				settingsMap["writers"] = convertStringsToUserObjects(settingsMap["writers"])
			}
			if auditorsNeedsMigration {
				settingsMap["auditors"] = convertStringsToUserObjects(settingsMap["auditors"])
			}
			settingsMap["updated_at"] = now
			updatedData, err = json.Marshal(settingsMap)
			if err != nil {
				return fmt.Errorf("failed to re-marshal settings: %w", err)
			}
		}
	}

	if updateErr != nil {
		return fmt.Errorf("failed to update after %d attempts: %w", maxRetries, updateErr)
	}

	slog.DebugContext(ctx, "successfully migrated record", "uid", uid)

	// Build and publish a proper indexer message envelope so OpenSearch gets
	// the correct tags, access config, and updated data — matching what the
	// service publishes on every settings write.
	msg, err := buildIndexerMessage(ctx, uid, settingsMap, baseKV)
	if err != nil {
		slog.WarnContext(ctx, "failed to build indexer message, skipping reindex",
			"uid", uid,
			"error", err,
		)
		return nil
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		slog.WarnContext(ctx, "failed to marshal indexer message",
			"uid", uid,
			"error", err,
		)
		return nil
	}

	if err := nc.Publish(constants.IndexCommitteeSettingsSubject, msgBytes); err != nil {
		slog.WarnContext(ctx, "failed to publish index message",
			"uid", uid,
			"subject", constants.IndexCommitteeSettingsSubject,
			"error", err,
		)
	}

	return nil
}

// buildIndexerMessage constructs the full indexer message envelope for a settings record,
// including tags and IndexingConfig derived from the committee base record.
// This mirrors what buildCommitteeSettingsIndexingConfig does in the service layer.
func buildIndexerMessage(ctx context.Context, uid string, settingsMap map[string]interface{}, baseKV jetstream.KeyValue) (*indexerMessage, error) {
	tags := buildTags(ctx, uid, baseKV)

	falseVal := false
	cfg := &indexerTypes.IndexingConfig{
		ObjectID:             uid,
		AccessCheckObject:    fmt.Sprintf("committee_settings:%s", uid),
		AccessCheckRelation:  "auditor",
		HistoryCheckObject:   fmt.Sprintf("committee_settings:%s", uid),
		HistoryCheckRelation: "writer",
		Tags:                 tags,
		Public:               &falseVal,
	}

	return &indexerMessage{
		Action:         "updated",
		Headers:        map[string]string{constants.AuthorizationHeader: authToken},
		Data:           settingsMap,
		IndexingConfig: cfg,
	}, nil
}

// buildTags fetches the committee base record and assembles the same tags that
// Committee.Tags() returns in the domain model.
func buildTags(ctx context.Context, uid string, baseKV jetstream.KeyValue) []string {
	baseEntry, err := baseKV.Get(ctx, uid)
	if err != nil {
		slog.WarnContext(ctx, "could not fetch base committee for tags, indexing without full tags",
			"uid", uid,
			"error", err,
		)
		return []string{uid, fmt.Sprintf("committee_uid:%s", uid)}
	}

	var base map[string]interface{}
	if err := json.Unmarshal(baseEntry.Value(), &base); err != nil {
		slog.WarnContext(ctx, "could not unmarshal base committee for tags",
			"uid", uid,
			"error", err,
		)
		return []string{uid, fmt.Sprintf("committee_uid:%s", uid)}
	}

	var tags []string

	if v, ok := base["project_uid"].(string); ok && v != "" {
		tags = append(tags, fmt.Sprintf("project_uid:%s", v))
	}
	if v, ok := base["project_slug"].(string); ok && v != "" {
		tags = append(tags, fmt.Sprintf("project_slug:%s", v))
	}
	if v, ok := base["parent_uid"].(string); ok && v != "" {
		tags = append(tags, fmt.Sprintf("parent_uid:%s", v))
	}
	if v, ok := base["category"].(string); ok && v != "" {
		tags = append(tags, fmt.Sprintf("category:%s", v))
	}

	// uid appears twice — once bare, once prefixed — matching Committee.Tags()
	tags = append(tags, uid)
	tags = append(tags, fmt.Sprintf("committee_uid:%s", uid))

	return tags
}

// needsMigration returns true if the given field in the settings map contains a non-empty
// array of strings (old format). Returns false if empty, nil, or already object format.
func needsMigration(settingsMap map[string]interface{}, field string) bool {
	val, ok := settingsMap[field]
	if !ok || val == nil {
		return false
	}

	arr, ok := val.([]interface{})
	if !ok || len(arr) == 0 {
		return false
	}

	// Check if the first element is a string (old format) vs a map (new format)
	_, isString := arr[0].(string)
	return isString
}

// convertStringsToUserObjects converts a raw interface value ([]interface{} of strings)
// into a slice of committeeUserObject, populating only the Username field.
func convertStringsToUserObjects(val interface{}) []committeeUserObject {
	arr, ok := val.([]interface{})
	if !ok {
		return []committeeUserObject{}
	}

	result := make([]committeeUserObject, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			result = append(result, committeeUserObject{Username: s})
		}
	}
	return result
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
