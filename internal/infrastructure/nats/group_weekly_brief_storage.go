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
