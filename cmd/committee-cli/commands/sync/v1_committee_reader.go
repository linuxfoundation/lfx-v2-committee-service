// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	// v1MappingsBucket and v1ObjectsBucket are owned by lfx-v1-sync-helper; this command only
	// ever reads them.
	v1MappingsBucket = "v1-mappings"
	v1ObjectsBucket  = "v1-objects"

	// v1CommitteeMappingPrefix is the v1-mappings key prefix that maps a v2 committee UID to
	// "<projectSFID>:<committeeSFID>".
	v1CommitteeMappingPrefix = "committee.uid."

	// v1CommitteeObjectPrefix is the v1-objects key prefix of a committee record.
	v1CommitteeObjectPrefix = "platform-collaboration__c."

	// v1TombstoneMarker is the value prefix the sync helper writes for a deleted mapping.
	v1TombstoneMarker = "!del"
)

// v1CommitteeReader provides read-only access to the LFX v1 committee record that the v1 sync
// helper mirrors into NATS.
type v1CommitteeReader interface {
	// GetV1Committee returns the raw v1 committee (platform-collaboration__c) record that
	// corresponds to a v2 committee UID, resolved through the sync helper's v2-UID to v1-SFID
	// mapping. The record is the sync helper's mirror of v1, so it can lag live v1 by one
	// replication cycle. It returns a NotFound error when the committee has no live mapping or
	// no mirrored record.
	GetV1Committee(ctx context.Context, committeeUID string) (map[string]any, error)
}

// kvBinder binds a NATS KV bucket by name.
type kvBinder func(ctx context.Context, bucket string) (jetstream.KeyValue, error)

// kvV1CommitteeReader reads v1 committee records from the sync helper's KV buckets.
type kvV1CommitteeReader struct {
	bind kvBinder
}

func newKVV1CommitteeReader(bind kvBinder) *kvV1CommitteeReader {
	return &kvV1CommitteeReader{bind: bind}
}

// GetV1Committee implements v1CommitteeReader.
func (r *kvV1CommitteeReader) GetV1Committee(ctx context.Context, committeeUID string) (map[string]any, error) {
	mappings, err := r.bind(ctx, v1MappingsBucket)
	if err != nil {
		return nil, err
	}
	objects, err := r.bind(ctx, v1ObjectsBucket)
	if err != nil {
		return nil, err
	}

	entry, err := mappings.Get(ctx, v1CommitteeMappingPrefix+committeeUID)
	if err != nil {
		if stderrors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, errors.NewNotFound(fmt.Sprintf("no v1 mapping for committee %s", committeeUID))
		}
		return nil, errors.NewServiceUnavailable("failed to read v1 mapping", err)
	}
	committeeSFID, err := parseV1CommitteeSFID(entry.Value())
	if err != nil {
		return nil, errors.NewNotFound(fmt.Sprintf("v1 mapping for committee %s: %s", committeeUID, err.Error()))
	}

	object, err := objects.Get(ctx, v1CommitteeObjectPrefix+committeeSFID)
	if err != nil {
		if stderrors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, errors.NewNotFound(fmt.Sprintf("no mirrored v1 record for committee %s (v1 id %s)", committeeUID, committeeSFID))
		}
		return nil, errors.NewServiceUnavailable("failed to read v1 committee record", err)
	}
	record, err := decodeV1Object(object.Value())
	if err != nil {
		return nil, errors.NewUnexpected(fmt.Sprintf("failed to decode v1 record %s", committeeSFID), err)
	}
	return record, nil
}

// parseV1CommitteeSFID extracts the v1 committee ID from a committee.uid.* mapping value, which
// has the form "<projectSFID>:<committeeSFID>". A tombstoned mapping is rejected.
func parseV1CommitteeSFID(value []byte) (string, error) {
	text := string(value)
	if strings.HasPrefix(text, v1TombstoneMarker) {
		return "", stderrors.New("mapping is tombstoned")
	}
	_, committeeSFID, ok := strings.Cut(text, ":")
	if !ok || committeeSFID == "" {
		return "", fmt.Errorf("unexpected mapping value %q", text)
	}
	return committeeSFID, nil
}

// decodeV1Object decodes a v1-objects value. The sync helper stores either JSON or MessagePack,
// so JSON is tried first.
func decodeV1Object(value []byte) (map[string]any, error) {
	var record map[string]any
	if err := json.Unmarshal(value, &record); err == nil {
		return record, nil
	}
	record = nil
	if err := msgpack.Unmarshal(value, &record); err != nil {
		return nil, err
	}
	return record, nil
}
