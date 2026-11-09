// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package copybudget

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemaining_AContextWithoutABudgetHasNone(t *testing.T) {
	_, ok := Remaining(context.Background())

	assert.False(t, ok, "a request that is not a copy carries no budget")
}

func TestRemaining_ReportsWhatIsLeftOfTheDeadline(t *testing.T) {
	ctx := With(context.Background(), time.Now().Add(time.Minute))

	remaining, ok := Remaining(ctx)

	assert.True(t, ok)
	assert.Greater(t, remaining, 50*time.Second)
	assert.LessOrEqual(t, remaining, time.Minute)
}

func TestRemaining_IsNotPositiveOncePassed(t *testing.T) {
	remaining, ok := Remaining(With(context.Background(), time.Now().Add(-time.Second)))

	assert.True(t, ok)
	assert.LessOrEqual(t, remaining, time.Duration(0))
}

func TestRecovery_WithoutABudgetIsTheUsualBoundAndTheContextAsItIs(t *testing.T) {
	ctx := context.Background()

	got, timeout := Recovery(ctx, 5*time.Second, 2*time.Second)

	assert.Equal(t, 5*time.Second, timeout)
	assert.Equal(t, ctx, got, "a write that is not a copy is not touched")
	_, ok := Remaining(got)
	assert.False(t, ok)
}

func TestRecovery_OfACopyGetsWhatIsLeftBetweenTheFloorAndTheUsualBound(t *testing.T) {
	for name, tc := range map[string]struct {
		left time.Duration
		want time.Duration
	}{
		"more than the usual bound": {time.Minute, 5 * time.Second},
		"time left":                 {3 * time.Second, 3 * time.Second},
		"less than the floor":       {100 * time.Millisecond, 2 * time.Second},
		"passed":                    {-time.Second, 2 * time.Second},
	} {
		_, timeout := Recovery(With(context.Background(), time.Now().Add(tc.left)), 5*time.Second, 2*time.Second)

		assert.InDelta(t, tc.want.Seconds(), timeout.Seconds(), 0.2, name)
	}
}

// A recovery that calls another shares one deadline: the second gets what is left of it, and not the floor again.
func TestRecovery_ARecoveryInsideARecoveryDoesNotStartTheFloorAgain(t *testing.T) {
	outer, first := Recovery(With(context.Background(), time.Now().Add(-time.Second)), 5*time.Second, 2*time.Second)
	require.InDelta(t, 2.0, first.Seconds(), 0.2)

	_, nested := Recovery(outer, 5*time.Second, 2*time.Second)

	assert.LessOrEqual(t, nested, first, "never more than the recovery it is part of")
	assert.InDelta(t, 2.0, nested.Seconds(), 0.2, "what is left of the shared deadline")

	time.Sleep(300 * time.Millisecond)
	_, later := Recovery(outer, 5*time.Second, 2*time.Second)
	assert.Less(t, later, nested, "the deadline is shared, so it keeps running out")
}
