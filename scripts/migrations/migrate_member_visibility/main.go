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

// CommitteeSettings represents the existing committee settings structure
// This is a copy of the model WITHOUT the member_visibility field
// to avoid setting it to the Go zero value when unmarshaling
type CommitteeSettings struct {
	UID                   string    `json:"uid"`
	BusinessEmailRequired bool      `json:"business_email_required"`
	ShowMeetingAttendees  bool      `json:"show_meeting_attendees"`
	LastReviewedAt        *string   `json:"last_reviewed_at,omitempty"`
	LastReviewedBy        *string   `json:"last_reviewed_by,omitempty"`
	Writers               []string  `json:"writers"`
	Auditors              []string  `json:"auditors"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

var (
	natsURL      = flag.String("nats-url", getEnvOrDefault("NATS_URL", "nats://localhost:4222"), "NATS server URL")
	bucketName   = flag.String("bucket-name", constants.KVBucketNameCommitteeSettings, "NATS KV bucket name")
	indexSubject = flag.String("index-subject", constants.IndexCommitteeSettingsSubject, "NATS subject for index messages")
	dryRun       = flag.Bool("dry-run", false, "Preview changes without applying them")
	debug        = flag.Bool("debug", false, "Enable debug logging")
)

type migrationStats struct {
	Total   int
	Updated int
	Skipped int
	Failed  int
}

func main() {
	flag.Parse()

	// Initialize structured logging after parsing flags
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

	slog.InfoContext(ctx, "Starting member_visibility migration",
		"nats_url", *natsURL,
		"bucket", *bucketName,
		"index_subject", *indexSubject,
		"dry_run", *dryRun,
	)

	// Create NATS connection
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

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Get KeyValueStore for the bucket
	kvStore, err := js.KeyValue(ctx, *bucketName)
	if err != nil {
		return fmt.Errorf("failed to get KV store for bucket %s: %w", *bucketName, err)
	}

	// List all keys in the bucket
	slog.InfoContext(ctx, "Listing all keys in bucket", "bucket", *bucketName)
	keys, err := kvStore.ListKeys(ctx)
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	// Collect all valid UIDs
	var settingsUIDs []string
	for key := range keys.Keys() {
		// Skip lookup and slug keys
		if strings.HasPrefix(key, "lookup/") || strings.HasPrefix(key, "slug/") {
			continue
		}
		settingsUIDs = append(settingsUIDs, key)
	}

	slog.InfoContext(ctx, "Found committee settings records", "count", len(settingsUIDs))

	if *dryRun {
		slog.InfoContext(ctx, "DRY RUN MODE - No changes will be made")
	}

	// Process each committee settings record
	stats := &migrationStats{Total: len(settingsUIDs)}
	startTime := time.Now()

	for i, uid := range settingsUIDs {
		err := processRecord(ctx, kvStore, uid, *dryRun, *indexSubject, nc)
		if err != nil {
			if strings.Contains(err.Error(), "already has field") {
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

		// Log progress every 10 records
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

	// Print summary
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("Migration Complete!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Total records:    %d\n", stats.Total)
	fmt.Printf("Updated:          %d\n", stats.Updated)
	fmt.Printf("Skipped:          %d (already had field)\n", stats.Skipped)
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

func processRecord(ctx context.Context, kvStore jetstream.KeyValue, uid string, dryRun bool, indexSubject string, nc *nats.Conn) error {
	// Get current settings with revision
	entry, err := kvStore.Get(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to get entry: %w", err)
	}

	// Unmarshal into map to work with raw JSON
	var dataMap map[string]interface{}
	if err := json.Unmarshal(entry.Value(), &dataMap); err != nil {
		return fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	// Check if field is already set
	if _, exists := dataMap["member_visibility"]; exists {
		slog.DebugContext(ctx, "record already has member_visibility field",
			"uid", uid,
		)
		return fmt.Errorf("already has field")
	}

	// Add the new field with default value
	dataMap["member_visibility"] = "hidden"
	dataMap["updated_at"] = time.Now().Format(time.RFC3339Nano)

	slog.DebugContext(ctx, "setting member_visibility",
		"uid", uid,
		"value", "hidden",
	)

	if dryRun {
		slog.InfoContext(ctx, "[DRY RUN] would update record",
			"uid", uid,
			"member_visibility", "hidden",
		)
		return nil
	}

	// Update with optimistic locking and retry
	maxRetries := 3
	var updateErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Marshal current data on each attempt
		data, err := json.Marshal(dataMap)
		if err != nil {
			return fmt.Errorf("failed to marshal settings: %w", err)
		}

		_, updateErr = kvStore.Update(ctx, uid, data, entry.Revision())
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

			// Refetch the entry for the new revision
			entry, err = kvStore.Get(ctx, uid)
			if err != nil {
				return fmt.Errorf("failed to refetch entry: %w", err)
			}

			// Re-unmarshal refetched data
			if err := json.Unmarshal(entry.Value(), &dataMap); err != nil {
				return fmt.Errorf("failed to unmarshal refetched settings: %w", err)
			}

			// Re-check if field was added by concurrent process
			if _, exists := dataMap["member_visibility"]; exists {
				slog.DebugContext(ctx, "field was added by concurrent process",
					"uid", uid,
				)
				return fmt.Errorf("already has field")
			}

			// Re-apply changes to refetched data
			dataMap["member_visibility"] = "hidden"
			dataMap["updated_at"] = time.Now().Format(time.RFC3339Nano)
		}
	}

	if updateErr != nil {
		return fmt.Errorf("failed to update after %d attempts: %w", maxRetries, updateErr)
	}

	slog.DebugContext(ctx, "successfully updated record",
		"uid", uid,
	)

	// Publish index message (don't fail on error)
	msgData, err := json.Marshal(dataMap)
	if err != nil {
		slog.WarnContext(ctx, "failed to marshal settings for index message",
			"uid", uid,
			"error", err,
		)
	} else {
		if err := nc.Publish(indexSubject, msgData); err != nil {
			slog.WarnContext(ctx, "failed to publish index message",
				"uid", uid,
				"subject", indexSubject,
				"error", err,
			)
		}
	}

	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
