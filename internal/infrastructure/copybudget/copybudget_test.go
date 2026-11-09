// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package copybudget

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
