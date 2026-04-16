// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// split_member_index re-publishes two separate indexer messages for every
// committee member stored in the NATS KV store, backfilling the two-index
// structure introduced when the single committee_member index was split into:
//
//   - lfx.index.committee_member          (roster — non-sensitive, gated by roster_viewer)
//   - lfx.index.committee_member_sensitive (email   — sensitive,     gated by email_viewer)
//
// Existing indexed documents written under the old single-index structure
// (AccessCheckRelation: "viewer") must be re-published so the indexer updates
// both indexes with the correct access/history check relations.
//
// Usage:
//
//	NATS_URL=nats://localhost:4222 \
//	  go run ./scripts/migrations/split_member_index/ [--dry-run=false]
//
// Flags:
//
//	--dry-run  Log what would be published without actually publishing (default: true)
//
// Environment variables:
//
//	NATS_URL  NATS server URL (default: nats://127.0.0.1:4222)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const authorizationHeaderValue = "Bearer lfx-v2-committee-service"

func main() {
	dryRun := flag.Bool("dry-run", true, "log what would be published without actually publishing (default: true)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	ctx := context.Background()

	slog.InfoContext(ctx, "split_member_index starting",
		"nats_url", natsURL,
		"dry_run", *dryRun,
	)

	nc, err := nats.Connect(natsURL,
		nats.Timeout(10*time.Second),
		nats.MaxReconnects(5),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create JetStream context", "error", err)
		os.Exit(1)
	}

	kv, err := js.KeyValue(ctx, constants.KVBucketNameCommitteeMembers)
	if err != nil {
		slog.ErrorContext(ctx, "failed to bind to KV bucket",
			"bucket", constants.KVBucketNameCommitteeMembers,
			"error", err,
		)
		os.Exit(1)
	}

	keys, err := kv.ListKeys(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list keys", "error", err)
		os.Exit(1)
	}

	var memberKeys []string
	for key := range keys.Keys() {
		if strings.HasPrefix(key, "lookup/") || strings.HasPrefix(key, "slug/") {
			continue
		}
		memberKeys = append(memberKeys, key)
	}

	slog.InfoContext(ctx, "found members to migrate", "count", len(memberKeys))

	var processed, failed int

	for idx, key := range memberKeys {
		entry, getErr := kv.Get(ctx, key)
		if getErr != nil {
			slog.ErrorContext(ctx, "failed to get KV entry", "key", key, "error", getErr)
			failed++
			continue
		}

		var member model.CommitteeMember
		if unmarshalErr := json.Unmarshal(entry.Value(), &member); unmarshalErr != nil {
			slog.ErrorContext(ctx, "failed to unmarshal member", "key", key, "error", unmarshalErr)
			failed++
			continue
		}

		rosterBytes, sensitiveBytes, buildErr := buildBothMessages(ctx, &member)
		if buildErr != nil {
			slog.ErrorContext(ctx, "failed to build indexer messages",
				"key", key,
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"error", buildErr,
			)
			failed++
			continue
		}

		if *dryRun {
			slog.InfoContext(ctx, "[dry-run] would publish roster message",
				"key", key,
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"subject", constants.IndexCommitteeMemberSubject,
			)
			slog.InfoContext(ctx, "[dry-run] would publish sensitive message",
				"key", key,
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"subject", constants.IndexCommitteeMemberSensitiveSubject,
			)
			processed++
			continue
		}

		if pubErr := nc.Publish(constants.IndexCommitteeMemberSubject, rosterBytes); pubErr != nil {
			slog.ErrorContext(ctx, "failed to publish roster message",
				"key", key,
				"member_uid", member.UID,
				"subject", constants.IndexCommitteeMemberSubject,
				"error", pubErr,
			)
			failed++
			continue
		}

		if pubErr := nc.Publish(constants.IndexCommitteeMemberSensitiveSubject, sensitiveBytes); pubErr != nil {
			slog.ErrorContext(ctx, "failed to publish sensitive message",
				"key", key,
				"member_uid", member.UID,
				"subject", constants.IndexCommitteeMemberSensitiveSubject,
				"error", pubErr,
			)
			failed++
			continue
		}

		processed++

		if (idx+1)%100 == 0 {
			slog.InfoContext(ctx, "progress",
				"processed", processed,
				"failed", failed,
				"remaining", len(memberKeys)-idx-1,
			)
		}
	}

	slog.InfoContext(ctx, "split_member_index complete",
		"total", len(memberKeys),
		"processed", processed,
		"failed", failed,
		"dry_run", *dryRun,
	)

	if failed > 0 {
		os.Exit(1)
	}
}

// buildBothMessages builds the roster and sensitive indexer messages for a single member.
// The logic mirrors the ActionUpdated branch in committee_member_writer.go.
func buildBothMessages(ctx context.Context, member *model.CommitteeMember) (rosterBytes, sensitiveBytes []byte, err error) {
	rosterBytes, err = buildRosterMessage(ctx, member)
	if err != nil {
		return nil, nil, fmt.Errorf("roster message: %w", err)
	}

	sensitiveBytes, err = buildSensitiveMessage(ctx, member)
	if err != nil {
		return nil, nil, fmt.Errorf("sensitive message: %w", err)
	}

	return rosterBytes, sensitiveBytes, nil
}

// buildRosterMessage builds the non-sensitive indexer message gated by roster_viewer.
// Payload is CommitteeMemberBase (no email). Mirrors the roster block in
// committee_member_writer.go ActionUpdated.
func buildRosterMessage(ctx context.Context, member *model.CommitteeMember) ([]byte, error) {
	var nameAndAliases []string
	for _, v := range []string{member.CommitteeName, member.FirstName, member.LastName, member.Username} {
		if v != "" {
			nameAndAliases = append(nameAndAliases, v)
		}
	}

	msg := model.CommitteeIndexerMessage{
		Action: model.ActionUpdated,
		Tags:   member.Tags(),
	}

	built, err := msg.Build(ctx, member.CommitteeMemberBase)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}

	built.Headers = map[string]string{constants.AuthorizationHeader: authorizationHeaderValue}
	built.IndexingConfig = &indexerTypes.IndexingConfig{
		ObjectID:             member.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", member.CommitteeUID),
		AccessCheckRelation:  constants.RelationRosterViewer,
		HistoryCheckObject:   fmt.Sprintf("committee:%s", member.CommitteeUID),
		HistoryCheckRelation: "auditor",
		SortName:             member.FirstName,
		NameAndAliases:       nameAndAliases,
		ParentRefs:           []string{fmt.Sprintf("committee:%s", member.CommitteeUID)},
		Tags:                 member.Tags(),
		Fulltext:             fmt.Sprintf("%s %s %s", member.FirstName, member.LastName, member.Organization.Name),
	}

	return json.Marshal(built)
}

// buildSensitiveMessage builds the email-only indexer message gated by email_viewer.
// Payload is {uid, committee_uid, email}. Mirrors the sensitive block in
// committee_member_writer.go ActionUpdated.
func buildSensitiveMessage(ctx context.Context, member *model.CommitteeMember) ([]byte, error) {
	sensitiveMemberTags := []string{
		member.UID,
		fmt.Sprintf("committee_member_uid:%s", member.UID),
		fmt.Sprintf("committee_uid:%s", member.CommitteeUID),
		fmt.Sprintf("email:%s", member.Email),
	}

	msg := model.CommitteeIndexerMessage{
		Action: model.ActionUpdated,
		Tags:   sensitiveMemberTags,
	}

	sensitivePayload := struct {
		UID          string `json:"uid"`
		CommitteeUID string `json:"committee_uid"`
		Email        string `json:"email"`
	}{
		UID:          member.UID,
		CommitteeUID: member.CommitteeUID,
		Email:        member.Email,
	}

	built, err := msg.Build(ctx, sensitivePayload)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}

	built.Headers = map[string]string{constants.AuthorizationHeader: authorizationHeaderValue}
	built.IndexingConfig = &indexerTypes.IndexingConfig{
		ObjectID:             member.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", member.CommitteeUID),
		AccessCheckRelation:  constants.RelationEmailViewer,
		HistoryCheckObject:   fmt.Sprintf("committee:%s", member.CommitteeUID),
		HistoryCheckRelation: constants.RelationAuditor,
		SortName:             member.Email,
		NameAndAliases:       []string{member.Email},
		Fulltext:             member.Email,
		Tags:                 sensitiveMemberTags,
		ParentRefs:           []string{fmt.Sprintf("committee:%s", member.CommitteeUID)},
	}

	return json.Marshal(built)
}
