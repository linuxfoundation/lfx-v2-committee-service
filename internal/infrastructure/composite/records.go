// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"slices"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The functions below are what the records that have a uid and a revision of their own have in common in dual mode: the
// committees' replication, written for any such record. Each takes the reads and writes of the record as functions, so
// the members, the invites and the applications share it. See Store for what each guarantees.

// copyCreated copies a record that was just created in KV to PostgreSQL, with the revision KV gave it. A delete that
// finished in KV while it was being copied found nothing in PostgreSQL to remove and counted as done, and the insert
// would bring the record back; so once it is in, it is read from KV again and, if it is gone, taken out.
func copyCreated[T any](ctx context.Context, readKV func(context.Context) (T, uint64, error),
	createPG func(context.Context, T) error, deletePG func(ctx context.Context, revision uint64) error) error {
	record, revision, err := readKV(ctx)
	if err != nil {
		return err
	}
	wctx, err := postgres.WithRevision(ctx, revision)
	if err != nil {
		return err
	}
	if err := createPG(wctx, record); err != nil {
		return err
	}
	if _, _, err := readKV(ctx); isNotFound(err) {
		return deletePG(ctx, revision)
	}
	return nil
}

// copyUpdated writes the record KV holds now to PostgreSQL, with the revision KV gave it, guarded by the revision
// PostgreSQL has now and not by the one the caller sent: when an earlier copy failed the two differ, and guarding by the
// caller's would fail every later update of the record too. It cannot lose an update or go back, since PostgreSQL refuses
// a revision that is not above the one it replaces. Nothing is written when PostgreSQL already holds KV's revision.
func copyUpdated[T any](ctx context.Context, readKV func(context.Context) (T, uint64, error),
	readPGRevision func(context.Context) (uint64, error), updatePG func(ctx context.Context, record T, guard uint64) error) error {
	record, revision, err := readKV(ctx)
	if err != nil {
		return err
	}
	current, err := readPGRevision(ctx)
	if err != nil {
		return err
	}
	if revision == current {
		return nil
	}
	wctx, err := postgres.WithRevision(ctx, revision)
	if err != nil {
		return err
	}
	return updatePG(wctx, record, current)
}

// deleteConverged removes a record from PostgreSQL after KV decided that it is gone. It is guarded by the revision the
// record had; if PostgreSQL is not at that revision, because an earlier copy failed, it is removed under the revision it
// has now, since storage migrate never deletes. A record that is not there, or goes away meanwhile, is where a delete
// leaves it, and is not a failure.
func deleteConverged(ctx context.Context, deletePG func(ctx context.Context, revision uint64) error,
	readPGRevision func(context.Context) (uint64, error), revision uint64) error {
	err := deletePG(ctx, revision)
	var stale errs.Conflict
	if errors.As(err, &stale) {
		current, getErr := readPGRevision(ctx)
		if getErr != nil {
			err = getErr
		} else {
			err = deletePG(ctx, current)
		}
	}
	if isNotFound(err) {
		return nil
	}
	return err
}

// compareRecords compares the records a list read of the primary store returned with the ones PostgreSQL returns, whatever
// their order, by content.
func compareRecords[T any](ctx context.Context, s *Store, operation string, records []T, err error, readOther func(context.Context) ([]T, error)) {
	if err != nil {
		return // the read failed, so there is nothing to compare
	}
	s.compareValue(ctx, operation, func(ctx context.Context) (bool, error) {
		other, err := readOther(ctx)
		if err != nil {
			return false, err
		}
		a, err := digests(records)
		if err != nil {
			return false, err
		}
		b, err := digests(other)
		if err != nil {
			return false, err
		}
		return slices.Equal(a, b), nil
	})
}

// digests is the sorted content digest of each record.
func digests[T any](records []T) ([]string, error) {
	out := make([]string, 0, len(records))
	for _, record := range records {
		digest, err := Digest(record)
		if err != nil {
			return nil, err
		}
		out = append(out, string(digest[:]))
	}
	slices.Sort(out)
	return out, nil
}
