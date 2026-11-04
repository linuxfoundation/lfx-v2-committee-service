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
	// StaleRevision checks the error of an update or delete whose revision is no longer current. The
	// default is errs.Conflict; a backend that reports something else says so here, and the difference
	// is documented where the backend is.
	StaleRevision func(t *testing.T, err error)
}

// RequireUnexpected is a StaleRevision check for a backend that reports a lost compare-and-swap as an
// internal error (the NATS KV store does, where PostgreSQL reports a conflict).
func RequireUnexpected(t *testing.T, err error) {
	t.Helper()
	var unexpected errs.Unexpected
	require.ErrorAs(t, err, &unexpected)
}

func (b Backend) requireStale(t *testing.T, err error) {
	t.Helper()
	if b.StaleRevision != nil {
		b.StaleRevision(t, err)
		return
	}
	requireConflict(t, err)
}

func requireConflict(t *testing.T, err error) {
	t.Helper()
	var conflict errs.Conflict
	require.ErrorAs(t, err, &conflict)
}

func requireNotFound(t *testing.T, err error) {
	t.Helper()
	var notFound errs.NotFound
	require.ErrorAs(t, err, &notFound)
}

func requireValidation(t *testing.T, err error) {
	t.Helper()
	var validation errs.Validation
	require.ErrorAs(t, err, &validation)
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
