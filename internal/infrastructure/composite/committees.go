// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The writes below return what the primary store returns. In dual mode each is followed by the same change in
// PostgreSQL, from what KV holds after the write, with the revision KV gave it; see Store.

// Create persists the committee and its settings.
func (s *Store) Create(ctx context.Context, committee *model.Committee) error {
	if err := s.CommitteeReaderWriter.Create(ctx, committee); err != nil {
		return err
	}
	if s.mode == ModeDual {
		uid := committee.CommitteeBase.UID
		s.replicate(ctx, "create_committee", func(ctx context.Context) error {
			base, baseRevision, err := s.kv.GetBase(ctx, uid)
			if err != nil {
				return err
			}
			settings, settingsRevision, err := s.kv.GetSettings(ctx, uid)
			if err != nil && !isNotFound(err) {
				return err
			}
			if err != nil {
				settings = nil
			}
			wctx, err := postgres.WithRevision(ctx, baseRevision)
			if err == nil && settings != nil {
				wctx, err = postgres.WithSettingsRevision(wctx, settingsRevision)
			}
			if err != nil {
				return err
			}
			return s.pg.Create(wctx, &model.Committee{CommitteeBase: *base, CommitteeSettings: settings})
		})
	}
	return nil
}

// UpdateBase updates the committee base, guarded by the revision.
func (s *Store) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateBase(ctx, committee, revision); err != nil {
		return err
	}
	if s.mode == ModeDual {
		uid := committee.CommitteeBase.UID
		s.replicate(ctx, "update_committee", func(ctx context.Context) error { return s.replicateBase(ctx, uid) })
	}
	return nil
}

// UpdateSetting updates the committee's settings, guarded by the revision.
func (s *Store) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateSetting(ctx, settings, revision); err != nil {
		return err
	}
	if s.mode == ModeDual {
		uid := settings.UID
		s.replicate(ctx, "update_settings", func(ctx context.Context) error { return s.replicateSettings(ctx, uid) })
	}
	return nil
}

// Delete removes the committee and its settings, guarded by the revision.
func (s *Store) Delete(ctx context.Context, uid string, revision uint64) error {
	if err := s.CommitteeReaderWriter.Delete(ctx, uid, revision); err != nil {
		return err
	}
	if s.mode == ModeDual {
		s.replicate(ctx, "delete_committee", func(ctx context.Context) error {
			err := s.pg.Delete(ctx, uid, revision)
			var stale errs.Conflict
			if errors.As(err, &stale) {
				// PostgreSQL is not at the revision KV had, because an earlier copy of this committee failed. KV has
				// already decided that the committee is gone, and storage migrate never deletes, so what PostgreSQL holds
				// is removed under the revision it has now: left behind it would keep the name and the SSO group name taken
				// there, and the settings with their webhook URL.
				_, current, getErr := s.pg.GetBase(ctx, uid)
				if getErr != nil {
					return getErr
				}
				err = s.pg.Delete(ctx, uid, current)
			}
			if isNotFound(err) {
				return nil // it is not there, which is where a delete leaves it
			}
			return err
		})
	}
	return nil
}

// UpdateHasMailingList sets has_mailing_list; see port.CommitteeBaseWriter.
func (s *Store) UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error) {
	base, changed, err := s.CommitteeReaderWriter.UpdateHasMailingList(ctx, uid, hasMailingList)
	if err != nil || !changed {
		return base, changed, err
	}
	if s.mode == ModeDual {
		s.replicate(ctx, "update_has_mailing_list", func(ctx context.Context) error { return s.replicateBase(ctx, uid) })
	}
	return base, changed, nil
}

// UpdateTotalMembers sets total_members; see port.CommitteeBaseWriter.
func (s *Store) UpdateTotalMembers(ctx context.Context, uid string, totalMembers int) (*model.CommitteeBase, bool, error) {
	base, changed, err := s.CommitteeReaderWriter.UpdateTotalMembers(ctx, uid, totalMembers)
	if err != nil || !changed {
		return base, changed, err
	}
	if s.mode == ModeDual {
		s.replicate(ctx, "update_total_members", func(ctx context.Context) error { return s.replicateBase(ctx, uid) })
	}
	return base, changed, nil
}

// replicateBase writes the committee base KV holds now to PostgreSQL, with the revision KV gave it. Every update of the
// base is replicated this way, as the state KV holds, and not by running it again in PostgreSQL, which would stamp its own
// update time and leave the two records different. The write is guarded by the revision PostgreSQL has now, not by the
// one the caller sent: when an earlier copy failed the two differ, and guarding by the caller's would fail every later
// update of that committee too, where this one brings PostgreSQL up to what KV holds. It cannot lose an update or go
// back: PostgreSQL refuses a revision that is not above the one it replaces.
func (s *Store) replicateBase(ctx context.Context, uid string) error {
	base, newRevision, err := s.kv.GetBase(ctx, uid)
	if err != nil {
		return err
	}
	_, current, err := s.pg.GetBase(ctx, uid)
	if err != nil {
		return err
	}
	if newRevision == current {
		return nil // PostgreSQL already holds this revision: KV wrote nothing, or the copy is already there
	}
	wctx, err := postgres.WithRevision(ctx, newRevision)
	if err != nil {
		return err
	}
	return s.pg.UpdateBase(wctx, &model.Committee{CommitteeBase: *base}, current)
}

// replicateSettings is replicateBase for the settings.
func (s *Store) replicateSettings(ctx context.Context, uid string) error {
	settings, newRevision, err := s.kv.GetSettings(ctx, uid)
	if err != nil {
		return err
	}
	_, current, err := s.pg.GetSettings(ctx, uid)
	if err != nil {
		return err
	}
	if newRevision == current {
		return nil
	}
	wctx, err := postgres.WithRevision(ctx, newRevision)
	if err != nil {
		return err
	}
	return s.pg.UpdateSetting(wctx, settings, current)
}

// The reads below return what the primary store returns. In dual mode a sampled fraction is also made against
// PostgreSQL and compared; a mismatch is counted and logged and the KV result is still the one returned.

// GetBase returns the committee base and its revision.
func (s *Store) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	base, revision, err := s.CommitteeReaderWriter.GetBase(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_committee", base, revision, err, func(ctx context.Context) (any, uint64, error) {
			return s.pg.GetBase(ctx, uid)
		})
	}
	return base, revision, err
}

// GetSettings returns the committee's settings and their revision.
func (s *Store) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	settings, revision, err := s.CommitteeReaderWriter.GetSettings(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_settings", settings, revision, err, func(ctx context.Context) (any, uint64, error) {
			return s.pg.GetSettings(ctx, uid)
		})
	}
	return settings, revision, err
}

// ListAllUIDs returns the uid of every committee.
func (s *Store) ListAllUIDs(ctx context.Context) ([]string, error) {
	uids, err := s.CommitteeReaderWriter.ListAllUIDs(ctx)
	if err == nil && s.sampled() {
		s.compareValue(ctx, "list_committees", func(ctx context.Context) (bool, error) {
			other, err := s.pg.ListAllUIDs(ctx)
			if err != nil {
				return false, err
			}
			return sameSet(uids, other), nil
		})
	}
	return uids, err
}

// FindUIDByProjectAndName returns the uid of the committee with the name in the project.
func (s *Store) FindUIDByProjectAndName(ctx context.Context, projectUID, name string) (string, error) {
	uid, err := s.CommitteeReaderWriter.FindUIDByProjectAndName(ctx, projectUID, name)
	if s.sampled() && (err == nil || isNotFound(err)) {
		s.compareValue(ctx, "find_committee", func(ctx context.Context) (bool, error) {
			other, otherErr := s.pg.FindUIDByProjectAndName(ctx, projectUID, name)
			if otherErr != nil && !isNotFound(otherErr) {
				return false, otherErr
			}
			return isNotFound(err) == isNotFound(otherErr) && uid == other, nil
		})
	}
	return uid, err
}

// compareRecord compares a record the primary store returned with the one PostgreSQL returns for the same read.
func (s *Store) compareRecord(ctx context.Context, operation string, record any, revision uint64, err error,
	readOther func(ctx context.Context) (any, uint64, error)) {
	if err != nil && !isNotFound(err) {
		return // the read failed, so there is nothing to compare
	}
	s.compareValue(ctx, operation, func(ctx context.Context) (bool, error) {
		other, otherRevision, otherErr := readOther(ctx)
		switch {
		case otherErr != nil && !isNotFound(otherErr):
			return false, otherErr
		case isNotFound(err) || isNotFound(otherErr):
			return isNotFound(err) && isNotFound(otherErr), nil
		}
		digest, err := Digest(record)
		if err != nil {
			return false, err
		}
		otherDigest, err := Digest(other)
		if err != nil {
			return false, err
		}
		return digest == otherDigest && revision == otherRevision, nil
	})
}

// compareValue runs a comparison with the secondary store under the compare timeout and records what came of it: a
// mismatch, or a comparison that could not be made.
func (s *Store) compareValue(ctx context.Context, operation string, compare func(ctx context.Context) (same bool, err error)) {
	cctx, cancel := context.WithTimeout(ctx, s.compareTimeout)
	defer cancel()
	// As in replicate, a panic in the secondary store, or in the comparison, must not fail a read that KV has decided.
	same, err := func() (same bool, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("the comparison panicked: %v", recovered)
			}
		}()
		return compare(cctx)
	}()
	switch {
	case err != nil:
		s.metrics.ReadCompareFailed(ctx, s.mode, operation, err)
	case !same:
		s.metrics.ReadMismatch(ctx, s.mode, operation)
	}
}

// sameSet reports whether two lists hold the same values, whatever their order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
