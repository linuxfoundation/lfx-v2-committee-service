// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/copybudget"
)

// timeLeft returns what is left of the context's deadline.
func timeLeft(t *testing.T, ctx context.Context) time.Duration {
	t.Helper()
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "the recovery context is always bounded")
	return time.Until(deadline)
}

func TestDetachedCtx_WithoutACopyBudgetIsBoundedAsItAlwaysWas(t *testing.T) {
	// A request in kv mode, even one with a short deadline of its own, gets the full recovery time.
	parent, cancelParent := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelParent()

	ctx, cancel := detachedCtx(parent)
	defer cancel()

	assert.Greater(t, timeLeft(t, ctx), committeeRollbackTimeout-time.Second)
	assert.LessOrEqual(t, timeLeft(t, ctx), committeeRollbackTimeout)
}

func TestDetachedCtx_SurvivesCancellationOfTheRequest(t *testing.T) {
	parent, cancelParent := context.WithCancel(copybudget.With(context.Background(), time.Now().Add(time.Second)))
	cancelParent()

	ctx, cancel := detachedCtx(parent)
	defer cancel()

	assert.NoError(t, ctx.Err(), "a rollback still runs when the request was cancelled")
}

func TestDetachedCtx_OfACopyIsBoundedByWhatIsLeftOfTheCopyDeadline(t *testing.T) {
	for name, tc := range map[string]struct {
		left     time.Duration
		min, max time.Duration
	}{
		"a copy with time left":       {3 * time.Second, 2 * time.Second, 3 * time.Second},
		"more than the usual bound":   {time.Minute, committeeRollbackTimeout - time.Second, committeeRollbackTimeout},
		"a deadline that has passed":  {-time.Second, minRecoveryTimeout - time.Second, minRecoveryTimeout},
		"a deadline about to pass":    {10 * time.Millisecond, minRecoveryTimeout - time.Second, minRecoveryTimeout},
		"just over the minimum bound": {minRecoveryTimeout + 500*time.Millisecond, minRecoveryTimeout, minRecoveryTimeout + 500*time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			parent := copybudget.With(context.Background(), time.Now().Add(tc.left))

			ctx, cancel := detachedCtx(parent)
			defer cancel()

			left := timeLeft(t, ctx)
			assert.Greater(t, left, tc.min-time.Second)
			assert.LessOrEqual(t, left, tc.max)
		})
	}
}
