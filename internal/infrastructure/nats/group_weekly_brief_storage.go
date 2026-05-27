// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go/jetstream"
)

// sanitizeKVKey returns key with every JetStream-KV-forbidden character replaced by '.'.
// JetStream KV forbids '/', ':', ' ' — these would cause Get/Put to fail at runtime.
func sanitizeKVKey(key string) string {
	r := strings.NewReplacer("/", ".", ":", ".", " ", ".")
	return r.Replace(key)
}

// buildBriefIndexKey returns the secondary-index key for the (committee, window)
// pair: "{committee_uid}.{yyyymmdd}", routed through sanitizeKVKey so future UID
// shape changes cannot introduce forbidden characters.
func buildBriefIndexKey(committeeUID, windowYYYYMMDD string) string {
	return sanitizeKVKey(fmt.Sprintf("%s.%s", committeeUID, windowYYYYMMDD))
}

// GetGroupWeeklyBriefForWindow returns the brief for the given committee and
// window-start date, or (nil, nil) if no brief exists for that window. A miss
// is not an error — the GET /current endpoint maps a miss to a 200 with a null
// body. The throttle entry, when present, is returned as raw JSON bytes so
// Phase 2's throttle shape can evolve without churning Phase 1.
func (s *storage) GetGroupWeeklyBriefForWindow(ctx context.Context, committeeUID string, windowStart model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	// Sentinel: callers pass the brief value-type only so we can extract
	// WindowStart; the rest of the fields are unused.
	indexKey := buildBriefIndexKey(committeeUID, model.WindowDateKey(windowStart.WindowStart))

	idxBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefUIDIndex]
	if !ok {
		return nil, nil, errs.NewServiceUnavailable("group-weekly-brief-uid-index bucket not initialized")
	}

	entry, err := idxBucket.Get(ctx, indexKey)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, nil, nil
		}
		return nil, nil, errs.NewUnexpected("failed to read weekly-brief uid index", err)
	}
	briefUID := string(entry.Value())
	if briefUID == "" {
		return nil, nil, nil
	}

	briefBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefs]
	if !ok {
		return nil, nil, errs.NewServiceUnavailable("group-weekly-briefs bucket not initialized")
	}
	briefEntry, errGet := briefBucket.Get(ctx, sanitizeKVKey(briefUID))
	if errGet != nil {
		if errors.Is(errGet, jetstream.ErrKeyNotFound) {
			// Index points at a non-existent brief — treat as miss but log,
			// since this means the buckets have drifted.
			slog.WarnContext(ctx, "weekly-brief index points at missing brief",
				"committee_uid", committeeUID,
				"index_key", indexKey,
				"brief_uid", briefUID,
			)
			return nil, nil, nil
		}
		return nil, nil, errs.NewUnexpected("failed to read weekly brief", errGet)
	}

	brief := &model.GroupWeeklyBrief{}
	if err := json.Unmarshal(briefEntry.Value(), brief); err != nil {
		return nil, nil, errs.NewUnexpected("failed to unmarshal weekly brief", err)
	}

	// Defence in depth: confirm the index-resolved brief still belongs to the
	// requested committee and window. If the UID index has drifted, treat it as
	// a miss rather than leaking another committee's brief.
	if brief.CommitteeUID != committeeUID ||
		model.WindowDateKey(brief.WindowStart) != model.WindowDateKey(windowStart.WindowStart) {
		slog.WarnContext(ctx, "weekly-brief index resolved to mismatched brief",
			"committee_uid", committeeUID,
			"index_key", indexKey,
			"brief_uid", briefUID,
			"brief_committee_uid", brief.CommitteeUID,
			"brief_window_key", model.WindowDateKey(brief.WindowStart),
		)
		return nil, nil, nil
	}
	brief.Revision = briefEntry.Revision()

	// Best-effort throttle lookup. Misses and errors don't fail the read —
	// throttle is advisory metadata.
	var throttleBytes []byte
	if thBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefThrottle]; ok {
		thEntry, thErr := thBucket.Get(ctx, indexKey)
		switch {
		case thErr == nil:
			throttleBytes = thEntry.Value()
		case errors.Is(thErr, jetstream.ErrKeyNotFound):
			// no-op
		default:
			slog.WarnContext(ctx, "failed to read weekly-brief throttle entry",
				"committee_uid", committeeUID,
				"index_key", indexKey,
				"error", thErr,
			)
		}
	}

	return brief, throttleBytes, nil
}

// PutGroupWeeklyBrief persists the brief and refreshes the secondary index that
// maps {committee_uid}.{yyyymmdd} → brief UID. The brief UID is generated when
// missing. The returned brief carries the new KV revision so callers can chain
// further compare-and-swap updates.
//
// Optimistic concurrency: when brief.Revision > 0 the write uses Update (CAS);
// otherwise it uses Put. This lets the orchestrator distinguish a true create
// from a regeneration overwrite.
func (s *storage) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	if brief == nil {
		return nil, errs.NewValidation("brief is required")
	}
	if brief.CommitteeUID == "" {
		return nil, errs.NewValidation("committee_uid is required")
	}
	if brief.WindowStart.IsZero() {
		return nil, errs.NewValidation("window_start is required")
	}
	if brief.UID == "" {
		brief.UID = uuid.NewString()
	}
	now := time.Now().UTC()
	if brief.CreatedAt.IsZero() {
		brief.CreatedAt = now
	}
	brief.UpdatedAt = now

	briefBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefs]
	if !ok {
		return nil, errs.NewServiceUnavailable("group-weekly-briefs bucket not initialised")
	}
	idxBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefUIDIndex]
	if !ok {
		return nil, errs.NewServiceUnavailable("group-weekly-brief-uid-index bucket not initialised")
	}

	payload, err := json.Marshal(brief)
	if err != nil {
		return nil, errs.NewUnexpected("failed to marshal weekly brief", err)
	}

	briefKey := sanitizeKVKey(brief.UID)
	var (
		rev    uint64
		putErr error
	)
	if brief.Revision > 0 {
		rev, putErr = briefBucket.Update(ctx, briefKey, payload, brief.Revision)
	} else {
		rev, putErr = briefBucket.Put(ctx, briefKey, payload)
	}
	if putErr != nil {
		// A CAS conflict (concurrent regeneration) on the Update path is
		// retryable — surface 503 rather than 500, mirroring the throttle write.
		if isJetStreamCASConflict(putErr) {
			return nil, errs.NewServiceUnavailable("weekly brief CAS conflict — retry", putErr)
		}
		return nil, errs.NewUnexpected("failed to write weekly brief", putErr)
	}
	brief.Revision = rev

	// Refresh the secondary index. A previous brief may have lived under a
	// different UID — Put overwrites unconditionally, which is what we want.
	indexKey := buildBriefIndexKey(brief.CommitteeUID, model.WindowDateKey(brief.WindowStart))
	if _, errIdx := idxBucket.Put(ctx, indexKey, []byte(brief.UID)); errIdx != nil {
		slog.WarnContext(ctx, "failed to update weekly-brief uid index",
			"committee_uid", brief.CommitteeUID,
			"index_key", indexKey,
			"error", errIdx,
		)
		// The brief is persisted; index drift is recoverable on next write.
		return brief, nil
	}
	return brief, nil
}

// GetGroupWeeklyBriefThrottle returns the throttle entry for the given
// (committee, window-start) pair. A miss returns (nil, nil).
func (s *storage) GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	thBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefThrottle]
	if !ok {
		return nil, errs.NewServiceUnavailable("group-weekly-brief-throttle bucket not initialised")
	}
	key := buildBriefIndexKey(committeeUID, model.WindowDateKey(windowStart))
	entry, err := thBucket.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, errs.NewUnexpected("failed to read weekly-brief throttle entry", err)
	}
	t := &model.GroupWeeklyBriefThrottle{}
	if err := json.Unmarshal(entry.Value(), t); err != nil {
		return nil, errs.NewUnexpected("failed to unmarshal weekly-brief throttle entry", err)
	}
	t.Revision = entry.Revision()
	return t, nil
}

// PutGroupWeeklyBriefThrottle writes the throttle entry using compare-and-swap
// on the carried Revision. Revision == 0 → Create (which fails if the entry
// already exists); Revision > 0 → Update (CAS). On any KV error the caller
// should treat the throttle increment as failed and surface 503 to the client.
func (s *storage) PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	if throttle == nil {
		return nil, errs.NewValidation("throttle is required")
	}
	if throttle.CommitteeUID == "" {
		return nil, errs.NewValidation("committee_uid is required")
	}
	if throttle.WindowStart.IsZero() {
		return nil, errs.NewValidation("window_start is required")
	}
	thBucket, ok := s.client.kvStore[constants.KVBucketNameGroupWeeklyBriefThrottle]
	if !ok {
		return nil, errs.NewServiceUnavailable("group-weekly-brief-throttle bucket not initialised")
	}

	payload, err := json.Marshal(throttle)
	if err != nil {
		return nil, errs.NewUnexpected("failed to marshal throttle entry", err)
	}
	key := buildBriefIndexKey(throttle.CommitteeUID, model.WindowDateKey(throttle.WindowStart))

	var (
		rev    uint64
		putErr error
	)
	if throttle.Revision == 0 {
		rev, putErr = thBucket.Create(ctx, key, payload)
	} else {
		rev, putErr = thBucket.Update(ctx, key, payload, throttle.Revision)
	}
	if putErr != nil {
		// JetStream KV signals CAS conflict (wrong last sequence) on Update when
		// the caller-supplied Revision no longer matches the bucket's current
		// sequence. Surface that as ServiceUnavailable so the API layer can
		// return 503 and the caller can retry, per the documented throttle
		// concurrency contract.
		if isJetStreamCASConflict(putErr) {
			return nil, errs.NewServiceUnavailable("weekly-brief throttle CAS conflict — retry", putErr)
		}
		return nil, errs.NewUnexpected("failed to write weekly-brief throttle entry", putErr)
	}
	throttle.Revision = rev
	return throttle, nil
}

// isJetStreamCASConflict reports whether err indicates a JetStream KV
// compare-and-swap revision mismatch on Update. Newer client versions return
// jetstream.ErrKeyExists for this case; older / wrapped errors carry the
// underlying "wrong last sequence" message — we match both so the surface
// stays stable across client upgrades.
func isJetStreamCASConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, jetstream.ErrKeyExists) {
		return true
	}
	return strings.Contains(err.Error(), "wrong last sequence")
}
