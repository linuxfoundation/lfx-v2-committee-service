// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"fmt"
	"slices"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The functions below are what the records that have a uid and a revision of their own have in common in dual mode: the
// committees' replication, written for any such record. Each takes the reads and writes of the record as functions, so
// the members, the invites and the applications share it. See Store for what each guarantees.

// copyCreated copies a record that was just created in the primary store to the secondary one, with the revision the
// primary store gave it where the secondary store can take one (see core.carry). A record the primary store no longer has
// was deleted meanwhile, and there is nothing to copy. A delete that finishes in the primary store while the record is being
// copied finds nothing in the secondary store to remove and counts as done, and the insert would bring the record back; and
// an update that finishes meanwhile fails to copy for the same reason, and the insert would leave the older version. So
// once the record is in, it is read from the primary store again: if it is gone it is taken out, and if it has moved on,
// catchUp copies what the primary store holds now. A re-read that fails is a failure of the copy.
func copyCreated[T any](ctx context.Context, c *core, readSrc func(context.Context) (T, uint64, error),
	createDst func(context.Context, T) error, deleteDst func(ctx context.Context, revision uint64) error,
	catchUp func(context.Context) error) error {
	record, revision, err := readSrc(ctx)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	wctx, err := c.carry(ctx, revision)
	if err != nil {
		return err
	}
	if err := createDst(wctx, record); err != nil {
		return err
	}
	_, current, err := readSrc(ctx)
	switch {
	case isNotFound(err):
		return deleteDst(ctx, revision)
	case err != nil:
		return fmt.Errorf("reading the primary store again after the copy: %w", err)
	case current != revision:
		return catchUp(ctx)
	}
	return nil
}

// copyUpdated writes the record the primary store holds now to the secondary one, guarded by the revision the secondary
// store has now and not by the one the caller sent: when an earlier copy failed the two differ, and guarding by the
// caller's would fail every later update of the record too. In dual mode PostgreSQL is given the revision the primary store
// gave, cannot lose an update or go back, since it refuses a revision that is not above the one it replaces, and nothing is
// written when it already holds that revision. KV, the secondary store of dual-pg mode, assigns its own revisions, so there
// nothing is written when it already holds the same content. A record that the primary store no longer has was deleted
// meanwhile, and there is nothing to copy; one that the secondary store does not have is missing there, unless the primary
// store has lost it too.
func copyUpdated[T any](ctx context.Context, c *core, readSrc, readDst func(context.Context) (T, uint64, error),
	updateDst func(ctx context.Context, record T, guard uint64) error) error {
	record, revision, err := readSrc(ctx)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	existing, current, err := readDst(ctx)
	if isNotFound(err) {
		if _, _, srcErr := readSrc(ctx); isNotFound(srcErr) {
			return nil // deleted in both stores while this was being copied
		}
		return err
	}
	if err != nil {
		return err
	}
	if c.secondaryIsPostgres() {
		if revision == current {
			return nil
		}
	} else if same, err := sameContent(record, existing); err != nil {
		return err
	} else if same {
		return nil
	}
	wctx, err := c.carry(ctx, revision)
	if err != nil {
		return err
	}
	return updateDst(wctx, record, current)
}

// sameContent reports whether two records have the same content, by their digests.
func sameContent(a, b any) (bool, error) {
	x, err := Digest(a)
	if err != nil {
		return false, err
	}
	y, err := Digest(b)
	if err != nil {
		return false, err
	}
	return x == y, nil
}

// deleteConverged removes a record from the secondary store after the primary store decided that it is gone. In dual mode
// PostgreSQL is guarded by the revision the record had; if it is not at that revision, because an earlier copy failed, the
// record is removed under the revision it has now, since storage migrate never deletes. KV, the secondary store of dual-pg
// mode, has revisions of its own, so it is always guarded by the revision it has now. A record that is not there, or goes
// away meanwhile, is where a delete leaves it, and is not a failure.
func deleteConverged(ctx context.Context, c *core, deleteDst func(ctx context.Context, revision uint64) error,
	readDstRevision func(context.Context) (uint64, error), revision uint64) error {
	if !c.secondaryIsPostgres() {
		current, err := readDstRevision(ctx)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		revision = current
	}
	err := deleteDst(ctx, revision)
	var stale errs.Conflict
	if errors.As(err, &stale) {
		current, getErr := readDstRevision(ctx)
		if getErr != nil {
			err = getErr
		} else {
			err = deleteDst(ctx, current)
		}
	}
	if isNotFound(err) {
		return nil
	}
	return err
}

// comparer is what compareRecords needs of a composite: the comparison under the compare timeout.
type comparer interface {
	compareValue(ctx context.Context, operation string, compare func(ctx context.Context) (same bool, err error))
}

// compareRecords compares the records a list read of the primary store returned with the ones PostgreSQL returns, whatever
// their order, by content alone: a list does not carry the revisions. Only the lists that are bounded by a committee, an
// organization, an email or a username are compared; the reads of the whole set are for repair work, and storage verify
// is the comparison of them.
func compareRecords[T any](ctx context.Context, s comparer, operation string, records []T, err error, readOther func(context.Context) ([]T, error)) {
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
