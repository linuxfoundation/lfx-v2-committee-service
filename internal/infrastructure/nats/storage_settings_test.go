// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// settingsUpdateKV fails every Update with the configured error.
type settingsUpdateKV struct {
	jetstream.KeyValue
	updateErr error
}

func (k *settingsUpdateKV) Update(context.Context, string, []byte, uint64) (uint64, error) {
	return 0, k.updateErr
}

func TestStorage_UpdateSetting_ErrorMapping(t *testing.T) {
	tests := []struct {
		name      string
		updateErr error
		assertErr func(t *testing.T, err error)
	}{
		{
			name:      "revision mismatch is a conflict",
			updateErr: jetstream.ErrKeyExists,
			assertErr: func(t *testing.T, err error) {
				var conflict errs.Conflict
				assert.ErrorAs(t, err, &conflict)
			},
		},
		{
			name:      "wrapped wrong last sequence is a conflict",
			updateErr: fmt.Errorf("nats: wrong last sequence: 7"),
			assertErr: func(t *testing.T, err error) {
				var conflict errs.Conflict
				assert.ErrorAs(t, err, &conflict)
			},
		},
		{
			name:      "missing key is not found",
			updateErr: jetstream.ErrKeyNotFound,
			assertErr: func(t *testing.T, err error) {
				var notFound errs.NotFound
				assert.ErrorAs(t, err, &notFound)
			},
		},
		{
			name:      "anything else is unexpected",
			updateErr: errors.New("connection reset"),
			assertErr: func(t *testing.T, err error) {
				var unexpected errs.Unexpected
				assert.ErrorAs(t, err, &unexpected)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &storage{client: &NATSClient{
				kvStore: map[string]jetstream.KeyValue{constants.KVBucketNameCommitteeSettings: &settingsUpdateKV{updateErr: tc.updateErr}},
			}}

			err := s.UpdateSetting(context.Background(), &model.CommitteeSettings{UID: "committee-1"}, 3)

			tc.assertErr(t, err)
		})
	}
}
