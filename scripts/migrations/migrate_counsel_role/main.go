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
	natsURL      = flag.String("nats-url", getEnvOrDefault("NATS_URL", "nats://localhost:4222"), "NATS server URL")
	bucketName   = flag.String("bucket-name", constants.KVBucketNameCommitteeMembers, "NATS KV bucket name")
	indexSubject = flag.String("index-subject", constants.IndexCommitteeMemberSubject, "NATS subject for index messages")
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

	slog.InfoContext(ctx, "Starting counsel_role migration",
		"nats_url", *natsURL,
		"bucket", *bucketName,
		"index_subject", *indexSubject,
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

	kvStore, err := js.KeyValue(ctx, *bucketName)
	if err != nil {
		return fmt.Errorf("failed to get KV store for bucket %s: %w", *bucketName, err)
	}

	slog.InfoContext(ctx, "Listing all keys in bucket", "bucket", *bucketName)
	keys, err := kvStore.ListKeys(ctx)
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	var memberUIDs []string
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") || strings.HasPrefix(key, "slug/") {
			continue
		}
		memberUIDs = append(memberUIDs, key)
	}

	slog.InfoContext(ctx, "Found committee member records", "count", len(memberUIDs))

	if *dryRun {
		slog.InfoContext(ctx, "DRY RUN MODE - No changes will be made")
	}

	stats := &migrationStats{Total: len(memberUIDs)}
	startTime := time.Now()

	for i, uid := range memberUIDs {
		err := processRecord(ctx, kvStore, uid, *dryRun, *indexSubject, nc)
		if err != nil {
			if strings.Contains(err.Error(), "not counsel") {
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
	fmt.Printf("Skipped:          %d (role was not Counsel)\n", stats.Skipped)
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
	entry, err := kvStore.Get(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to get entry: %w", err)
	}

	var dataMap map[string]interface{}
	if err := json.Unmarshal(entry.Value(), &dataMap); err != nil {
		return fmt.Errorf("failed to unmarshal member: %w", err)
	}

	roleMap, ok := dataMap["role"].(map[string]interface{})
	if !ok || roleMap["name"] != "Counsel" {
		slog.DebugContext(ctx, "record role is not Counsel, skipping", "uid", uid)
		return fmt.Errorf("not counsel")
	}

	roleMap["name"] = "None"
	dataMap["role"] = roleMap
	dataMap["updated_at"] = time.Now().Format(time.RFC3339Nano)

	slog.DebugContext(ctx, "converting Counsel role to None", "uid", uid)

	if dryRun {
		slog.InfoContext(ctx, "[DRY RUN] would update record", "uid", uid, "role", "None")
		return nil
	}

	maxRetries := 3
	var updateErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		data, err := json.Marshal(dataMap)
		if err != nil {
			return fmt.Errorf("failed to marshal member: %w", err)
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

			entry, err = kvStore.Get(ctx, uid)
			if err != nil {
				return fmt.Errorf("failed to refetch entry: %w", err)
			}

			if err := json.Unmarshal(entry.Value(), &dataMap); err != nil {
				return fmt.Errorf("failed to unmarshal refetched member: %w", err)
			}

			roleMap, ok = dataMap["role"].(map[string]interface{})
			if !ok || roleMap["name"] != "Counsel" {
				slog.DebugContext(ctx, "role was changed by concurrent process", "uid", uid)
				return fmt.Errorf("not counsel")
			}

			roleMap["name"] = "None"
			dataMap["role"] = roleMap
			dataMap["updated_at"] = time.Now().Format(time.RFC3339Nano)
		}
	}

	if updateErr != nil {
		return fmt.Errorf("failed to update after %d attempts: %w", maxRetries, updateErr)
	}

	slog.DebugContext(ctx, "successfully updated record", "uid", uid)

	msgData, err := json.Marshal(dataMap)
	if err != nil {
		slog.WarnContext(ctx, "failed to marshal member for index message", "uid", uid, "error", err)
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
