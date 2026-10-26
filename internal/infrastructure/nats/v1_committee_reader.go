// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	// v1CommitteeObjectPrefix is the v1-objects key prefix of a committee record.
	v1CommitteeObjectPrefix = "platform-collaboration__c."

	// v1TombstoneMarker is the value prefix the sync helper writes for a deleted mapping.
	v1TombstoneMarker = "!del"
)

type v1CommitteeReader struct {
	client *NATSClient
}

// NewV1CommitteeReader returns a read-only reader for v1 committee records mirrored by the
// v1 sync helper.
func NewV1CommitteeReader(client *NATSClient) port.V1CommitteeReader {
	return &v1CommitteeReader{client: client}
}

// GetV1Committee implements port.V1CommitteeReader.
func (r *v1CommitteeReader) GetV1Committee(ctx context.Context, committeeUID string) (map[string]any, error) {
	mappings, err := r.client.GetOrBindKVStore(ctx, constants.KVBucketNameV1Mappings)
	if err != nil {
		return nil, err
	}
	objects, err := r.client.GetOrBindKVStore(ctx, constants.KVBucketNameV1Objects)
	if err != nil {
		return nil, err
	}

	entry, err := mappings.Get(ctx, "committee.uid."+committeeUID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, errs.NewNotFound(fmt.Sprintf("no v1 mapping for committee %s", committeeUID))
		}
		return nil, errs.NewServiceUnavailable("failed to read v1 mapping", err)
	}
	committeeSFID, err := parseV1CommitteeSFID(entry.Value())
	if err != nil {
		return nil, errs.NewNotFound(fmt.Sprintf("v1 mapping for committee %s: %s", committeeUID, err.Error()))
	}

	object, err := objects.Get(ctx, v1CommitteeObjectPrefix+committeeSFID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, errs.NewNotFound(fmt.Sprintf("no mirrored v1 record for committee %s (v1 id %s)", committeeUID, committeeSFID))
		}
		return nil, errs.NewServiceUnavailable("failed to read v1 committee record", err)
	}
	record, err := decodeV1Object(object.Value())
	if err != nil {
		return nil, errs.NewUnexpected(fmt.Sprintf("failed to decode v1 record %s", committeeSFID), err)
	}
	return record, nil
}

// parseV1CommitteeSFID extracts the v1 committee ID from a committee.uid.* mapping value,
// which has the form "<projectSFID>:<committeeSFID>". A tombstoned mapping is rejected.
func parseV1CommitteeSFID(value []byte) (string, error) {
	text := string(value)
	if strings.HasPrefix(text, v1TombstoneMarker) {
		return "", errors.New("mapping is tombstoned")
	}
	_, committeeSFID, ok := strings.Cut(text, ":")
	if !ok || committeeSFID == "" {
		return "", fmt.Errorf("unexpected mapping value %q", text)
	}
	return committeeSFID, nil
}

// decodeV1Object decodes a v1-objects value. The sync helper stores either JSON or
// MessagePack, so JSON is tried first.
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
