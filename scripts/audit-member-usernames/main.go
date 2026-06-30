// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// audit-member-usernames scans every entry in the committee-members NATS KV
// bucket and validates each member's stored username against the expected value
// derived from the v1-mappings and v1-objects secondary indexes.
//
// Validation logic mirrors the current v1-sync-helper flow: the expected
// username is the plain LFX username (username__c) from v1-objects — not the
// auth0|{id} sub format that older sync-helper code wrote.
//
//  1. Look up the member's email in the v1-user.email.* index (v1-mappings bucket)
//     to resolve a v1 user SFID.
//  2. Look up the salesforce-merged_user.{sfid} record (v1-objects bucket) to
//     get the canonical username__c field.
//  3. Compare with the username currently stored on the committee member.
//
// Members written by older sync-helper code may still store an auth0|{username}
// value. Those are flagged as wrong_username so they can be corrected.
//
// This read-only audit surfaces three bug classes introduced by the v1-sync-helper
// not re-syncing members when:
//   - A username goes from unset to set on the v1 user record.
//   - An alternate email is added to v1-objects/v1-mappings linking an email to a user.
//   - A user merge happens upstream and propagates into v1-objects/v1-mappings.
//
// The script makes NO changes. It only reports counts and (with -verbose) details.
//
// Usage:
//
//	go run ./scripts/audit-member-usernames/main.go [flags]
//
// Flags:
//
//	-nats-url    NATS server URL (default: $NATS_URL or nats://localhost:4222)
//	-debug       Enable debug-level logging
//	-verbose     Print one log line per finding (needs_username / wrong_username)
//	-limit       Stop after processing this many primary member records (0 = no limit)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/vmihailenco/msgpack/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

// ── v1-mappings KV key prefixes (must match lfx-v1-sync-helper exactly) ──────

const (
	kvKeyEmailPrefix   = "v1-user.email."
	v1MergedUserPrefix = "salesforce-merged_user."
	tombstoneMarker    = "!del"
)

// ── NATS KV key normalization (mirrors handlers_users.go in v1-sync-helper) ──

// toKVKey normalises s with TrimSpace → ToLower → NFC, then returns
// URL-safe base64 (no padding) for use as a NATS KV key segment.
func toKVKey(s string) string {
	s = norm.NFC.String(strings.ToLower(strings.TrimSpace(s)))
	if s == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// ── Domain types ──────────────────────────────────────────────────────────────

// committeeMember mirrors the fields we care about from CommitteeMemberBase.
type committeeMember struct {
	UID           string `json:"uid"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	CommitteeUID  string `json:"committee_uid"`
	CommitteeName string `json:"committee_name"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
}

// auditStats collects counts for the final report.
type auditStats struct {
	Total int // primary member records examined (lookup/* skipped)

	OK               int // username matches expected
	NeedsUsername    int // should have a username, doesn't
	WrongUsername    int // has a username but it's wrong
	StaleHasUsername int // v1 user has no username but member does (stale)

	UserNoV1Username int // v1 user found but has no username; member.Username correctly empty
	EmailNotInV1     int // no v1 email mapping; can't validate

	NoEmail         int // no email field; skipped
	LookupKeySkip   int // lookup/* / secondary-index keys; skipped
	UnmarshalFailed int // failed to decode the committee member record
	V1LookupFailed  int // failed to read or decode v1-objects for the user's SFID
}

// ── Flags ──────────────────────────────────────────────────────────────────────

var (
	natsURL = flag.String("nats-url", getEnvOrDefault("NATS_URL", "nats://localhost:4222"), "NATS server URL")
	debug   = flag.Bool("debug", false, "Enable debug-level logging")
	verbose = flag.Bool("verbose", false, "Print one log line per needs_username / wrong_username finding")
	limit   = flag.Int("limit", 0, "Stop after this many primary member records (0 = no limit, useful for testing)")
)

// ── Entry point ────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		log.Fatalf("audit failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	// ── Connect to NATS ────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "connecting to NATS")
	nc, err := nats.Connect(*natsURL,
		nats.Timeout(10*time.Second),
		nats.MaxReconnects(3),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	defer nc.Close()
	slog.InfoContext(ctx, "connected to NATS")

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("create JetStream context: %w", err)
	}

	// ── Open KV buckets ────────────────────────────────────────────────────────
	membersKV, err := js.KeyValue(ctx, constants.KVBucketNameCommitteeMembers)
	if err != nil {
		return fmt.Errorf("open %s KV bucket: %w", constants.KVBucketNameCommitteeMembers, err)
	}

	mappingsKV, err := js.KeyValue(ctx, "v1-mappings")
	if err != nil {
		return fmt.Errorf("open v1-mappings KV bucket: %w", err)
	}

	v1KV, err := js.KeyValue(ctx, "v1-objects")
	if err != nil {
		return fmt.Errorf("open v1-objects KV bucket: %w", err)
	}

	slog.InfoContext(ctx, "opened KV buckets",
		"members", constants.KVBucketNameCommitteeMembers,
		"v1-mappings", "v1-mappings",
		"v1-objects", "v1-objects",
	)

	// ── List all committee-member keys ─────────────────────────────────────────
	slog.InfoContext(ctx, "listing committee-member keys…")
	lister, err := membersKV.ListKeys(ctx)
	if err != nil {
		return fmt.Errorf("list committee-member keys: %w", err)
	}
	defer func() {
		if err := lister.Stop(); err != nil {
			slog.WarnContext(ctx, "error stopping key lister", "error", err)
		}
	}()

	// ── Audit loop ─────────────────────────────────────────────────────────────
	var s auditStats

	for key := range lister.Keys() {
		// Skip secondary-index / lookup entries.
		if strings.HasPrefix(key, "lookup/") {
			s.LookupKeySkip++
			continue
		}

		// Apply -limit (only counts primary records, not lookup keys).
		if *limit > 0 && s.Total >= *limit {
			break
		}
		s.Total++

		// Fetch and decode the committee member record.
		entry, err := membersKV.Get(ctx, key)
		if err != nil {
			slog.WarnContext(ctx, "failed to get member entry", "key", key, "error", err)
			s.UnmarshalFailed++
			continue
		}

		var m committeeMember
		if err := json.Unmarshal(entry.Value(), &m); err != nil {
			slog.WarnContext(ctx, "failed to unmarshal member", "key", key, "error", err)
			s.UnmarshalFailed++
			continue
		}

		if s.Total%1000 == 0 {
			slog.InfoContext(ctx, "audit progress",
				"examined", s.Total,
				"needs_username", s.NeedsUsername,
				"wrong_username", s.WrongUsername,
			)
		}

		slog.DebugContext(ctx, "examining member",
			"uid", m.UID,
			"email", redaction.RedactEmail(m.Email),
			"stored_username", redaction.Redact(m.Username),
		)

		// Members without an email cannot be resolved via the email index.
		if m.Email == "" {
			s.NoEmail++
			slog.DebugContext(ctx, "skipping member: no email", "uid", m.UID)
			continue
		}

		// ── Step 1: email → v1 user SFID via v1-mappings ──────────────────────
		sfid, err := resolveEmailToSFID(ctx, mappingsKV, m.Email)
		if err != nil {
			slog.WarnContext(ctx, "error resolving email in v1-mappings",
				"uid", m.UID, "error", err)
			s.V1LookupFailed++
			continue
		}
		if sfid == "" {
			slog.DebugContext(ctx, "email not found in v1-mappings", "uid", m.UID)
			s.EmailNotInV1++
			continue
		}

		// ── Step 2: SFID → username__c via v1-objects ─────────────────────────
		v1Username, err := resolveV1Username(ctx, v1KV, sfid)
		if err != nil {
			slog.WarnContext(ctx, "error reading v1-objects for user",
				"uid", m.UID, "sfid", sfid, "error", err)
			s.V1LookupFailed++
			continue
		}

		// ── Step 3: compare ───────────────────────────────────────────────────
		switch {
		case v1Username == "" && m.Username == "":
			s.UserNoV1Username++
			slog.DebugContext(ctx, "v1 user has no username; member correctly empty",
				"uid", m.UID, "sfid", sfid)

		case v1Username == "" && m.Username != "":
			s.StaleHasUsername++
			if *verbose {
				slog.InfoContext(ctx, "STALE_HAS_USERNAME",
					"uid", m.UID,
					"email", redaction.RedactEmail(m.Email),
					"committee_uid", m.CommitteeUID,
					"committee_name", m.CommitteeName,
					"stored_username", redaction.Redact(m.Username),
					"expected_username", "",
				)
			}

		case v1Username != "" && m.Username == "":
			s.NeedsUsername++
			if *verbose {
				slog.InfoContext(ctx, "NEEDS_USERNAME",
					"uid", m.UID,
					"email", redaction.RedactEmail(m.Email),
					"committee_uid", m.CommitteeUID,
					"committee_name", m.CommitteeName,
					"stored_username", "",
					"expected_username", redaction.Redact(v1Username),
				)
			}

		case !strings.EqualFold(m.Username, v1Username):
			s.WrongUsername++
			if *verbose {
				slog.InfoContext(ctx, "WRONG_USERNAME",
					"uid", m.UID,
					"email", redaction.RedactEmail(m.Email),
					"committee_uid", m.CommitteeUID,
					"committee_name", m.CommitteeName,
					"stored_username", redaction.Redact(m.Username),
					"expected_username", redaction.Redact(v1Username),
				)
			}

		default:
			s.OK++
			slog.DebugContext(ctx, "username ok", "uid", m.UID, "username", redaction.Redact(m.Username))
		}
	}

	// ── Final report ──────────────────────────────────────────────────────────
	slog.InfoContext(ctx, "─── AUDIT COMPLETE ───────────────────────────────────────────────")
	slog.InfoContext(ctx, "primary records examined", "count", s.Total)
	slog.InfoContext(ctx, "lookup/* keys skipped", "count", s.LookupKeySkip)
	slog.InfoContext(ctx, "")
	slog.InfoContext(ctx, "USERNAME CORRECT (ok)", "count", s.OK)
	slog.InfoContext(ctx, "")
	slog.InfoContext(ctx, "=== NEEDS FIXING ===")
	slog.InfoContext(ctx, "NEEDS_USERNAME  (has email→v1 user with username, member.username empty)",
		"count", s.NeedsUsername)
	slog.InfoContext(ctx, "WRONG_USERNAME  (member.username doesn't match plain v1 username)",
		"count", s.WrongUsername)
	slog.InfoContext(ctx, "STALE_HAS_USERNAME (v1 user has no username, member.username non-empty)",
		"count", s.StaleHasUsername)
	slog.InfoContext(ctx, "")
	slog.InfoContext(ctx, "=== INFORMATIONAL ===")
	slog.InfoContext(ctx, "USER_NO_V1_USERNAME (v1 user found, but has no username; member.username correctly empty)",
		"count", s.UserNoV1Username)
	slog.InfoContext(ctx, "EMAIL_NOT_IN_V1     (no v1-mappings entry for member email; cannot validate)",
		"count", s.EmailNotInV1)
	slog.InfoContext(ctx, "NO_EMAIL            (member has no email field; skipped)",
		"count", s.NoEmail)
	slog.InfoContext(ctx, "V1_LOOKUP_FAILED    (v1-objects fetch or decode error; skipped)",
		"count", s.V1LookupFailed)
	slog.InfoContext(ctx, "UNMARSHAL_FAILED    (committee-member record decode error; skipped)",
		"count", s.UnmarshalFailed)
	slog.InfoContext(ctx, "")

	// Machine-readable summary line for easy grep/CI assertion.
	slog.InfoContext(ctx, "SUMMARY",
		"total", s.Total,
		"ok", s.OK,
		"needs_username", s.NeedsUsername,
		"wrong_username", s.WrongUsername,
		"stale_has_username", s.StaleHasUsername,
		"user_no_v1_username", s.UserNoV1Username,
		"email_not_in_v1", s.EmailNotInV1,
		"no_email", s.NoEmail,
		"v1_lookup_failed", s.V1LookupFailed,
		"unmarshal_failed", s.UnmarshalFailed,
	)

	if s.NeedsUsername > 0 || s.WrongUsername > 0 || s.StaleHasUsername > 0 {
		slog.InfoContext(ctx, "audit found members needing username correction (see counts above); re-run with -verbose for per-member details")
	} else {
		slog.InfoContext(ctx, "audit found no username discrepancies in validated members")
	}

	return nil
}

// ── v1 lookup helpers ─────────────────────────────────────────────────────────

// resolveEmailToSFID looks up a member email in the v1-user.email.* secondary
// index (v1-mappings bucket) and returns the user SFID, or "" on miss.
//
// The index stores the SFID as a plain string value. Tombstoned entries ("!del")
// are treated as misses.
func resolveEmailToSFID(ctx context.Context, mappingsKV jetstream.KeyValue, email string) (string, error) {
	encoded := toKVKey(email)
	if encoded == "" {
		return "", nil
	}
	entry, err := mappingsKV.Get(ctx, kvKeyEmailPrefix+encoded)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return "", nil
		}
		return "", fmt.Errorf("get email index key: %w", err)
	}
	if string(entry.Value()) == tombstoneMarker {
		return "", nil // tombstoned → treat as miss
	}
	return string(entry.Value()), nil
}

// resolveV1Username fetches the salesforce-merged_user.{sfid} record from the
// v1-objects bucket and returns the username__c field (raw, as stored in v1).
//
// Returns ("", nil) when the user record doesn't exist, is deleted, or has an
// empty username__c — callers treat that as "no username in v1".
//
// v1-objects records are written by Meltano; the vast majority are JSON. A small
// number of older records may be msgpack-encoded — those are decoded via a msgpack
// fallback after JSON fails. Only records that fail both decoders are returned as
// an error and counted as V1LookupFailed in the caller.
func resolveV1Username(ctx context.Context, v1KV jetstream.KeyValue, sfid string) (string, error) {
	key := v1MergedUserPrefix + sfid
	entry, err := v1KV.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return "", nil
		}
		return "", fmt.Errorf("get v1-objects entry %q: %w", key, err)
	}

	// Tombstone check (mirrors isTombstonedMapping in v1-sync-helper).
	if string(entry.Value()) == tombstoneMarker {
		return "", nil
	}

	var data map[string]any
	if err := json.Unmarshal(entry.Value(), &data); err != nil {
		if msgpackErr := msgpack.Unmarshal(entry.Value(), &data); msgpackErr != nil {
			return "", fmt.Errorf("decode v1-objects entry %q (json: %w, msgpack: %w)", key, err, msgpackErr)
		}
	}

	// Hard-delete flag.
	if del, ok := data["isdeleted"].(bool); ok && del {
		return "", nil
	}

	// WAL soft-delete: _sdc_deleted_at set to a non-empty string or non-nil value.
	if deletedAt, ok := data["_sdc_deleted_at"]; ok {
		if s, isStr := deletedAt.(string); (isStr && strings.TrimSpace(s) != "") || (!isStr && deletedAt != nil) {
			return "", nil
		}
	}

	username, _ := data["username__c"].(string)
	return username, nil
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
