// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
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

// The lookup-key helpers take their recovery context from the same place, so a stalled copy is not given another full
// rollback timeout by them either.
func TestRecoveryCtx_TheLookupKeyBoundFollowsTheCopyBudgetToo(t *testing.T) {
	parent := copybudget.With(context.Background(), time.Now().Add(-time.Second))

	ctx, cancel := recoveryCtx(parent, rollbackTimeout)
	defer cancel()

	assert.LessOrEqual(t, timeLeft(t, ctx), minRecoveryTimeout, "not the five seconds a write without a budget gets")
}

func TestRecoveryCtx_WithoutABudgetTheLookupKeyBoundIsAsItAlwaysWas(t *testing.T) {
	ctx, cancel := recoveryCtx(context.Background(), rollbackTimeout)
	defer cancel()

	assert.Greater(t, timeLeft(t, ctx), rollbackTimeout-time.Second)
}

// A rollback that calls the helpers that release a lookup key runs inside one recovery deadline, and does not get the floor
// again for each call.
func TestRecoveryCtx_NestedRecoveriesShareOneDeadline(t *testing.T) {
	outer, cancelOuter := detachedCtx(copybudget.With(context.Background(), time.Now().Add(-time.Second)))
	defer cancelOuter()
	time.Sleep(300 * time.Millisecond)

	inner, cancelInner := recoveryCtx(outer, rollbackTimeout)
	defer cancelInner()

	assert.Less(t, timeLeft(t, inner), minRecoveryTimeout-200*time.Millisecond, "what is left of the outer recovery")
}

// deadlineKV records the time the context it was called with had left.
type deadlineKV struct {
	jetstream.KeyValue
	left []time.Duration
}

func (d *deadlineKV) note(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		d.left = append(d.left, time.Until(deadline))
	}
}

func (d *deadlineKV) Get(ctx context.Context, _ string) (jetstream.KeyValueEntry, error) {
	d.note(ctx)
	return nil, jetstream.ErrKeyNotFound
}

func (d *deadlineKV) Purge(ctx context.Context, _ string, _ ...jetstream.KVDeleteOpt) error {
	d.note(ctx)
	return nil
}

// The helpers that release a lookup key detach from the request too, and take the same bound: a stalled copy is not given
// another five seconds by them, and a write that is not a copy gets the five seconds it always did.
func TestLookupKeyHelpers_AreBoundedByTheCopyBudget(t *testing.T) {
	helpers := map[string]func(ctx context.Context, kv jetstream.KeyValue){
		"rollbackLookupKey":   func(ctx context.Context, kv jetstream.KeyValue) { rollbackLookupKey(ctx, kv, "record", "lookup", nil) },
		"releaseLookupKey":    func(ctx context.Context, kv jetstream.KeyValue) { releaseLookupKey(ctx, kv, "lookup", 1, nil) },
		"purgeOwnedLookupKey": func(ctx context.Context, kv jetstream.KeyValue) { purgeOwnedLookupKey(ctx, kv, "lookup", "uid") },
	}
	for name, run := range helpers {
		t.Run(name, func(t *testing.T) {
			stalled := &deadlineKV{}
			run(copybudget.With(context.Background(), time.Now().Add(-time.Second)), stalled)
			require.NotEmpty(t, stalled.left, "the helper used the store")
			for _, left := range stalled.left {
				assert.LessOrEqual(t, left, minRecoveryTimeout, "a stalled copy gets the floor and no more")
			}

			plain := &deadlineKV{}
			run(context.Background(), plain)
			require.NotEmpty(t, plain.left)
			for _, left := range plain.left {
				assert.Greater(t, left, rollbackTimeout-time.Second, "a write without a budget is bounded as always")
			}
		})
	}
}
