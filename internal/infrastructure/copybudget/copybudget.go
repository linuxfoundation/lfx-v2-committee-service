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

type recovering struct{}

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

// Recovery returns the context a rollback or cleanup that follows a write runs with, and how long it may take. Without a
// budget in the context, which is every write that is not the copy of a composite store, that is usual and the context is
// returned as it is. With one, the recovery gets what is left of the copy's deadline, but at least floor and at most usual,
// and the context records that deadline and that it is a recovery: a recovery that calls another (a rollback that releases a
// lookup key) then shares that one deadline, and gets what is left of it, not the floor again.
func Recovery(ctx context.Context, usual, floor time.Duration) (context.Context, time.Duration) {
	remaining, ok := Remaining(ctx)
	if !ok {
		return ctx, usual
	}
	timeout := min(usual, max(remaining, floor))
	if ctx.Value(recovering{}) != nil {
		timeout = min(usual, max(remaining, 0))
	}
	ctx = With(ctx, time.Now().Add(timeout))
	return context.WithValue(ctx, recovering{}, true), timeout
}
