// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func TestParseV1CommitteeSFID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "project and committee", value: "project-sfid:committee-sfid", want: "committee-sfid"},
		{name: "tombstoned", value: "!del", wantErr: true},
		{name: "tombstoned with suffix", value: "!del:1700000000", wantErr: true},
		{name: "no separator", value: "committee-sfid", wantErr: true},
		{name: "empty committee", value: "project-sfid:", wantErr: true},
		{name: "empty", value: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseV1CommitteeSFID([]byte(tt.value))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDecodeV1Object(t *testing.T) {
	want := map[string]any{"public_enabled": true, "public_name": "First Last Committee"}

	t.Run("json", func(t *testing.T) {
		got, err := decodeV1Object([]byte(`{"public_enabled":true,"public_name":"First Last Committee"}`))
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("msgpack", func(t *testing.T) {
		packed, err := msgpack.Marshal(want)
		require.NoError(t, err)
		got, err := decodeV1Object(packed)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("garbage", func(t *testing.T) {
		_, err := decodeV1Object([]byte{0xc1})
		require.Error(t, err)
	})

	// A null or empty record would read as all-zero fields and clear real v2 values.
	t.Run("json null", func(t *testing.T) {
		_, err := decodeV1Object([]byte("null"))
		require.Error(t, err)
	})

	t.Run("json empty object", func(t *testing.T) {
		_, err := decodeV1Object([]byte("{}"))
		require.Error(t, err)
	})

	t.Run("msgpack nil", func(t *testing.T) {
		_, err := decodeV1Object([]byte{0xc0})
		require.Error(t, err)
	})

	t.Run("msgpack empty map", func(t *testing.T) {
		packed, err := msgpack.Marshal(map[string]any{})
		require.NoError(t, err)
		_, err = decodeV1Object(packed)
		require.Error(t, err)
	})
}

func TestIsV1RecordDeleted(t *testing.T) {
	tests := []struct {
		name   string
		record map[string]any
		want   bool
	}{
		{"live", map[string]any{"public_enabled": true}, false},
		{"isdeleted true", map[string]any{"isdeleted": true}, true},
		{"isdeleted false", map[string]any{"isdeleted": false}, false},
		{"soft-deleted with a timestamp", map[string]any{"_sdc_deleted_at": "2026-01-01T00:00:00Z"}, true},
		{"soft-deleted with a non-string value", map[string]any{"_sdc_deleted_at": int64(1700000000)}, true},
		{"soft-delete marker is null", map[string]any{"_sdc_deleted_at": nil}, false},
		{"soft-delete marker is blank", map[string]any{"_sdc_deleted_at": "  "}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isV1RecordDeleted(tt.record))
		})
	}
}

// fakeKVEntry is a KV entry that only carries a value.
type fakeKVEntry struct {
	jetstream.KeyValueEntry
	value []byte
}

func (e fakeKVEntry) Value() []byte { return e.value }

// fakeKV is a KV bucket that answers Get from a map; calling any other method panics.
type fakeKV struct {
	jetstream.KeyValue
	entries map[string][]byte
	errs    map[string]error
	gets    []string
}

func (k *fakeKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	k.gets = append(k.gets, key)
	if err, ok := k.errs[key]; ok {
		return nil, err
	}
	value, ok := k.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return fakeKVEntry{value: value}, nil
}

// newFakeV1Reader returns a reader over two fake buckets, and the bucket names it bound.
func newFakeV1Reader(mappings, objects *fakeKV) (*kvV1CommitteeReader, *[]string) {
	var bound []string
	reader := newKVV1CommitteeReader(func(_ context.Context, bucket string) (jetstream.KeyValue, error) {
		bound = append(bound, bucket)
		switch bucket {
		case "v1-mappings":
			return mappings, nil
		case "v1-objects":
			return objects, nil
		}
		return nil, stderrors.New("unexpected bucket " + bucket)
	})
	return reader, &bound
}

func TestKVV1CommitteeReader_ReadsTheMappedRecord(t *testing.T) {
	mappings := &fakeKV{entries: map[string][]byte{"committee.uid.committee-1": []byte("project-sfid:committee-sfid")}}
	objects := &fakeKV{entries: map[string][]byte{
		"platform-collaboration__c.committee-sfid": []byte(`{"public_enabled":true,"public_name":"First Last Committee"}`),
	}}
	reader, bound := newFakeV1Reader(mappings, objects)

	got, err := reader.GetV1Committee(context.Background(), "committee-1")

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"public_enabled": true, "public_name": "First Last Committee"}, got)
	assert.ElementsMatch(t, []string{"v1-mappings", "v1-objects"}, *bound, "reads the sync helper's two buckets")
	assert.Equal(t, []string{"committee.uid.committee-1"}, mappings.gets, "the mapping key is committee.uid.<v2 uid>")
	assert.Equal(t, []string{"platform-collaboration__c.committee-sfid"}, objects.gets, "the object key is platform-collaboration__c.<v1 id>")
}

func TestKVV1CommitteeReader_MapsErrors(t *testing.T) {
	const mappingKey = "committee.uid.committee-1"
	const objectKey = "platform-collaboration__c.committee-sfid"
	mappingValue := []byte("project-sfid:committee-sfid")
	liveRecord := []byte(`{"public_enabled":true}`)

	tests := []struct {
		name     string
		mappings *fakeKV
		objects  *fakeKV
		wantType any // an errs.NotFound, errs.ServiceUnavailable or errs.Unexpected value
	}{
		{
			name:     "no mapping",
			mappings: &fakeKV{},
			objects:  &fakeKV{},
			wantType: errs.NotFound{},
		},
		{
			name:     "tombstoned mapping",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: []byte("!del")}},
			objects:  &fakeKV{},
			wantType: errs.NotFound{},
		},
		{
			name:     "no mirrored record",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
			objects:  &fakeKV{},
			wantType: errs.NotFound{},
		},
		{
			name:     "mapping read fails",
			mappings: &fakeKV{errs: map[string]error{mappingKey: stderrors.New("connection reset")}},
			objects:  &fakeKV{},
			wantType: errs.ServiceUnavailable{},
		},
		{
			name:     "record read fails",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
			objects:  &fakeKV{errs: map[string]error{objectKey: stderrors.New("connection reset")}},
			wantType: errs.ServiceUnavailable{},
		},
		{
			name:     "record marked deleted",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
			objects:  &fakeKV{entries: map[string][]byte{objectKey: []byte(`{"public_enabled":true,"isdeleted":true}`)}},
			wantType: errs.NotFound{},
		},
		{
			name:     "record soft-deleted",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
			objects:  &fakeKV{entries: map[string][]byte{objectKey: []byte(`{"public_enabled":true,"_sdc_deleted_at":"2026-01-01T00:00:00Z"}`)}},
			wantType: errs.NotFound{},
		},
		{
			name:     "record is null",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
			objects:  &fakeKV{entries: map[string][]byte{objectKey: []byte("null")}},
			wantType: errs.Unexpected{},
		},
		{
			name:     "record is undecodable",
			mappings: &fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
			objects:  &fakeKV{entries: map[string][]byte{objectKey: {0xc1}}},
			wantType: errs.Unexpected{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, _ := newFakeV1Reader(tt.mappings, tt.objects)

			got, err := reader.GetV1Committee(context.Background(), "committee-1")

			require.Error(t, err)
			assert.Nil(t, got)
			switch tt.wantType.(type) {
			case errs.NotFound:
				var target errs.NotFound
				assert.True(t, stderrors.As(err, &target), "want NotFound, got %T: %v", err, err)
			case errs.ServiceUnavailable:
				var target errs.ServiceUnavailable
				assert.True(t, stderrors.As(err, &target), "want ServiceUnavailable, got %T: %v", err, err)
			case errs.Unexpected:
				var target errs.Unexpected
				assert.True(t, stderrors.As(err, &target), "want Unexpected, got %T: %v", err, err)
			}
		})
	}

	// A live record next to the same setup still reads, so the cases above fail for their reason.
	reader, _ := newFakeV1Reader(
		&fakeKV{entries: map[string][]byte{mappingKey: mappingValue}},
		&fakeKV{entries: map[string][]byte{objectKey: liveRecord}},
	)
	_, err := reader.GetV1Committee(context.Background(), "committee-1")
	require.NoError(t, err)
}

func TestKVV1CommitteeReader_PropagatesBindErrors(t *testing.T) {
	bindErr := stderrors.New("bucket unavailable")
	reader := newKVV1CommitteeReader(func(context.Context, string) (jetstream.KeyValue, error) { return nil, bindErr })

	_, err := reader.GetV1Committee(context.Background(), "committee-1")

	require.ErrorIs(t, err, bindErr)
}
