// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
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
}
