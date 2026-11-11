// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/nats-io/nats.go/jetstream"
)

// visitKVSnapshot calls visit with every entry of the watcher's initial snapshot and returns nil only when it received the
// sentinel that ends it. jetstream.KeyLister has no error channel: when the watcher's subscription dies part way (the
// ordered consumer cannot be recreated, the connection closes), the library closes the updates channel, which a KeyLister
// reads as the end of the listing, so a truncated listing looks complete. Here a closed channel, or a cancelled context,
// before the sentinel is an error, for the callers that cannot treat a partial listing as the whole bucket.
//
// visit has seen every entry that arrived before the error, so a caller may still use them. The watcher is stopped.
func visitKVSnapshot(ctx context.Context, watcher jetstream.KeyWatcher, visit func(jetstream.KeyValueEntry)) error {
	defer func() { _ = watcher.Stop() }()
	for {
		select {
		case <-ctx.Done():
			return errs.NewUnexpected("key listing was cancelled before it was complete", ctx.Err())
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errs.NewUnexpected("key listing ended before it was complete")
			}
			if entry == nil {
				return nil
			}
			visit(entry)
		}
	}
}
