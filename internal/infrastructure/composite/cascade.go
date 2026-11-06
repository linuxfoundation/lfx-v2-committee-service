// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// CascadingStore is the Store of a mode in which PostgreSQL is the primary store, with the cascading delete of a committee
// that store has. The service uses port.CommitteeCascadeDeleter, when its writer provides it, in place of Delete, so what
// the writer implements decides what a committee delete removes. That is why it is a type of its own, and not a method of
// Store that every mode would have: in kv and dual mode the committee delete is the one the service has always had, which
// removes the committee and its settings and leaves the rest for the service to clean up, and WithCascade returns the
// Store itself for them. In postgres mode, without it, a committee delete would leave every child of the committee behind
// in PostgreSQL, which has no other way to remove them than this.
type CascadingStore struct {
	*Store
	cascade port.CommitteeCascadeDeleter
}

var (
	_ port.CommitteeReaderWriter   = (*CascadingStore)(nil)
	_ port.CommitteeCascadeDeleter = (*CascadingStore)(nil)
)

// WithCascade returns the writer the service is to use for the store: the store itself when the primary store is KV, which
// has no cascading delete, and a CascadingStore when it is PostgreSQL. cascade is the PostgreSQL store's cascading delete,
// needed in the second case.
func WithCascade(store *Store, cascade port.CommitteeCascadeDeleter) (port.CommitteeReaderWriter, error) {
	if !store.mode.PostgresIsPrimary() {
		return store, nil
	}
	if cascade == nil {
		return nil, errors.New("storage mode " + string(store.mode) + " needs the cascading delete of the PostgreSQL store")
	}
	return &CascadingStore{Store: store, cascade: cascade}, nil
}

// DeleteCommitteeCascade removes the committee, its settings and everything that belongs to it from PostgreSQL in one
// transaction, guarded by the revision, and returns what it removed besides the committee, for the service to clean up the
// search index, the access tuples and the stored files of the documents.
//
// In dual-pg mode the committee and its settings are then removed from KV, and so are the applications that were removed,
// which is what the service does with KV in a committee delete; the other children are left in KV, as a delete in KV mode
// leaves them, where verify reports them as orphans. A failure to do so is counted and never fails the request, and the
// committee is removed under the revision KV has, not the one PostgreSQL gave.
func (c *CascadingStore) DeleteCommitteeCascade(ctx context.Context, uid string, revision uint64) (*model.CommitteeRemovedChildren, error) {
	removed, err := c.cascade.DeleteCommitteeCascade(ctx, uid, revision)
	if err != nil {
		return nil, err
	}
	if c.mode.HasSecondary() {
		c.replicate(ctx, "delete_committee_cascade", uid, func(ctx context.Context) error {
			errs := []error{c.deleteInSecondary(ctx, uid, revision)}
			if removed != nil {
				for _, application := range removed.Applications {
					errs = append(errs, c.dst.DeleteApplication(ctx, application))
				}
			}
			return errors.Join(errs...)
		})
	}
	return removed, nil
}
