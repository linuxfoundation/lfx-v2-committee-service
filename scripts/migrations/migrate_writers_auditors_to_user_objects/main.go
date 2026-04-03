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
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var (
	natsURL           = flag.String("nats-url", getEnvOrDefault("NATS_URL", "nats://localhost:4222"), "NATS server URL")
	settingsBucketName = flag.String("settings-bucket-name", constants.KVBucketNameCommitteeSettings, "NATS KV bucket name for committee settings")
	indexSubject      = flag.String("index-subject", constants.IndexCommitteeSettingsSubject, "NATS subject for index messages")
	dryRun            = flag.Bool("dry-run", false, "Preview changes without applying them")
	debug             = flag.Bool("debug", false, "Enable debug logging")
)

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
		"settings_bucket", *settingsBucketName,
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

	settingsKV, err := js.KeyValue(ctx, *settingsBucketName)
	if err != nil {
		return fmt.Errorf("failed to get KV store for bucket %s: %w", *settingsBucketName, err)
	}

	slog.InfoContext(ctx, "Listing all keys in bucket", "bucket", *settingsBucketName)
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
		err := processRecord(ctx, settingsKV, uid, *dryRun, *indexSubject, nc)
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
func processRecord(ctx context.Context, settingsKV jetstream.KeyValue, uid string, dryRun bool, idxSubject string, nc *nats.Conn) error {
	entry, err := settingsKV.Get(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to get settings entry: %w", err)
	}

	var settingsMap map[string]interface{}
	if err := json.Unmarshal(entry.Value(), &settingsMap); err != nil {
		return fmt.Errorf("failed to unmarshal settings: %w", err)
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

			// Re-check if already migrated by a concurrent process
			if !needsMigration(settingsMap, "writers") && !needsMigration(settingsMap, "auditors") {
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

	if err := nc.Publish(idxSubject, updatedData); err != nil {
		slog.WarnContext(ctx, "failed to publish index message",
			"uid", uid,
			"subject", idxSubject,
			"error", err,
		)
	}

	return nil
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
