// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// readyStore is a store that says whether it is ready.
type readyStore struct {
	port.CommitteeReaderWriter
	err error
}

func (r readyStore) IsReady(context.Context) error { return r.err }

var errDown = errors.New("down")

// The service reports ready through its storage. Where PostgreSQL is the primary store that is PostgreSQL's ping, so the store
// also reports NATS, which the service needs for the files of documents and for the indexer and access messages; where KV is
// the primary store it is NATS's, as it has always been, and a PostgreSQL that is down does not take the pod out of rotation.
func TestStore_IsReady(t *testing.T) {
	for _, tc := range []struct {
		mode       Mode
		kvErr      error
		pgErr      error
		wantFailed bool
	}{
		{ModeKV, nil, errDown, false},
		{ModeKV, errDown, nil, true},
		{ModeDual, nil, errDown, false},
		{ModeDual, errDown, nil, true},
		{ModeDualPG, nil, nil, false},
		{ModeDualPG, errDown, nil, true},
		{ModeDualPG, nil, errDown, true},
		{ModePostgres, nil, nil, false},
		{ModePostgres, errDown, nil, true},
		{ModePostgres, nil, errDown, true},
	} {
		store, err := New(tc.mode, readyStore{err: tc.kvErr}, readyStore{err: tc.pgErr})
		require.NoError(t, err)

		got := store.IsReady(context.Background())

		assert.Equal(t, tc.wantFailed, got != nil, "%s with KV %v and PostgreSQL %v", tc.mode, tc.kvErr, tc.pgErr)
	}
}

func TestStore_IsReadyInPostgresModeNeedsNoKVStore(t *testing.T) {
	store, err := New(ModePostgres, nil, readyStore{})
	require.NoError(t, err)

	assert.NoError(t, store.IsReady(context.Background()))
}
