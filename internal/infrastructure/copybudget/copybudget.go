// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package copybudget carries the deadline of a copy to the secondary store from the composite store to the store
// adapters, which detach their rollback and cleanup work from the request's cancellation and so also from its deadline.
package copybudget

import (
	"context"
	"time"
)

type key struct{}

// With returns a context that records the deadline a copy to the secondary store has to finish by.
func With(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, key{}, deadline)
}

// Remaining returns the time left until the copy's deadline, which is not positive once it has passed, and whether the
// context carries one. Only the composite store sets it, on the copy to the secondary store; a request that does not go
// through it never has one, so the adapters' own bounds are what they have always been.
func Remaining(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Value(key{}).(time.Time)
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}
