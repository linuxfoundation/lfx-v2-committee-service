// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storetest

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// Backend is a storage implementation under test.
type Backend struct {
	// Name labels the backend in test names.
	Name string
	// StaleRevision checks the error of a committee, settings or delete write whose revision is no
	// longer current. The default is errs.Conflict; a backend that reports something else says so
	// here, and the difference is documented where the backend is.
	StaleRevision func(t *testing.T, err error)
	// EmptyUID checks the error of a read with an empty uid. The default is errs.Validation.
	EmptyUID func(t *testing.T, err error)
}

// The checks below look at the outer error, the one the HTTP layer switches on (cmd/committee-api/service
// error.go), not anywhere in its chain: an internal error that wraps a validation error is a 500, not a
// 400, and errors.As would accept it as either.

// RequireUnexpected checks for an internal error. It is the StaleRevision check of a backend that
// reports a lost compare-and-swap that way (the NATS KV store does, where PostgreSQL reports a conflict),
// and the EmptyUID check of one that does for an empty uid.
func RequireUnexpected(t *testing.T, err error) {
	t.Helper()
	_, ok := err.(errs.Unexpected)
	require.True(t, ok, "want an internal error as the outer error, got %T: %v", err, err)
}

func (b Backend) requireStale(t *testing.T, err error) {
	t.Helper()
	if b.StaleRevision != nil {
		b.StaleRevision(t, err)
		return
	}
	requireConflict(t, err)
}

func (b Backend) requireEmptyUID(t *testing.T, err error) {
	t.Helper()
	if b.EmptyUID != nil {
		b.EmptyUID(t, err)
		return
	}
	requireValidation(t, err)
}

func requireConflict(t *testing.T, err error) {
	t.Helper()
	_, ok := err.(errs.Conflict)
	require.True(t, ok, "want a conflict as the outer error, got %T: %v", err, err)
}

func requireNotFound(t *testing.T, err error) {
	t.Helper()
	_, ok := err.(errs.NotFound)
	require.True(t, ok, "want not found as the outer error, got %T: %v", err, err)
}

func requireValidation(t *testing.T, err error) {
	t.Helper()
	_, ok := err.(errs.Validation)
	require.True(t, ok, "want a validation error as the outer error, got %T: %v", err, err)
}

// race runs write from several goroutines at once and returns how many succeeded. Every failure is
// checked with requireLoss. Each writer writes different content (write gets its number), because the
// NATS KV store treats a failed swap whose stored record equals what the writer wrote as a write that
// landed, which is right for a lost acknowledgement and makes identical writers all look like winners.
func race(t *testing.T, write func(writer int) error, requireLoss func(*testing.T, error)) int {
	t.Helper()
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = write(i)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
			continue
		}
		requireLoss(t, err)
	}
	return wins
}
