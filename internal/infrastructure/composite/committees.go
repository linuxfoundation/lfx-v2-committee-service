// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"fmt"
	"slices"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// The writes below return what the primary store returns. In dual mode each is followed by the same change in
// PostgreSQL, from what KV holds after the write, with the revision KV gave it; see Store.

// Create persists the committee and its settings.
func (s *Store) Create(ctx context.Context, committee *model.Committee) error {
	if err := s.CommitteeReaderWriter.Create(ctx, committee); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := committee.CommitteeBase.UID
		s.replicate(ctx, "create_committee", uid, func(ctx context.Context) error {
			base, baseRevision, err := s.src.GetBase(ctx, uid)
			if isNotFound(err) {
				return nil // deleted meanwhile, so there is nothing to copy
			}
			if err != nil {
				return err
			}
			settings, settingsRevision, err := s.src.GetSettings(ctx, uid)
			if err != nil && !isNotFound(err) {
				return err
			}
			if err != nil {
				settings = nil
			}
			wctx, err := s.carry(ctx, baseRevision)
			if err == nil && settings != nil && s.secondaryIsPostgres() {
				wctx, err = postgres.WithSettingsRevision(wctx, settingsRevision)
			}
			if err != nil {
				return err
			}
			if err := s.dst.Create(wctx, &model.Committee{CommitteeBase: *base, CommitteeSettings: settings}); err != nil {
				return err
			}
			// A delete that finished in KV while this was being copied found nothing in PostgreSQL to remove, and so
			// left the insert above to bring the committee back; an update that finished meanwhile failed to copy for the
			// same reason, and left the older version. So read KV again: if the committee is gone take it out, and if it
			// has moved on copy what KV holds now. From either order the two stores end up the same.
			_, current, err := s.src.GetBase(ctx, uid)
			switch {
			case isNotFound(err):
				return s.deleteInSecondary(ctx, uid, baseRevision)
			case err != nil:
				return fmt.Errorf("reading the primary store again after the copy: %w", err)
			}
			if current != baseRevision {
				if err := s.replicateBase(ctx, uid); err != nil {
					return err
				}
			}
			if settings != nil {
				_, currentSettings, err := s.src.GetSettings(ctx, uid)
				switch {
				case isNotFound(err):
					// Deleted with the committee meanwhile; the check of the base above covers it.
				case err != nil:
					return fmt.Errorf("reading the primary store settings again after the copy: %w", err)
				case currentSettings != settingsRevision:
					return s.replicateSettings(ctx, uid)
				}
			}
			return nil
		})
	}
	return nil
}

// UpdateBase updates the committee base, guarded by the revision.
func (s *Store) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateBase(ctx, committee, revision); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := committee.CommitteeBase.UID
		s.replicate(ctx, "update_committee", uid, func(ctx context.Context) error { return s.replicateBase(ctx, uid) })
	}
	return nil
}

// UpdateSetting updates the committee's settings, guarded by the revision.
func (s *Store) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateSetting(ctx, settings, revision); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := settings.UID
		s.replicate(ctx, "update_settings", uid, func(ctx context.Context) error { return s.replicateSettings(ctx, uid) })
	}
	return nil
}

// Delete removes the committee and its settings, guarded by the revision.
func (s *Store) Delete(ctx context.Context, uid string, revision uint64) error {
	if err := s.CommitteeReaderWriter.Delete(ctx, uid, revision); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		s.replicate(ctx, "delete_committee", uid, func(ctx context.Context) error { return s.deleteInSecondary(ctx, uid, revision) })
	}
	return nil
}

// deleteInSecondary removes the committee and its settings from the secondary store after the primary store decided that
// it is gone; see deleteConverged. Left behind, the committee would keep the name and the SSO group name taken there, and
// the settings with their webhook URL.
func (s *Store) deleteInSecondary(ctx context.Context, uid string, revision uint64) error {
	return deleteConverged(ctx, &s.core,
		func(ctx context.Context, revision uint64) error { return s.dst.Delete(ctx, uid, revision) },
		func(ctx context.Context) (uint64, error) {
			_, current, err := s.dst.GetBase(ctx, uid)
			return current, err
		}, revision)
}

// UpdateHasMailingList sets has_mailing_list; see port.CommitteeBaseWriter.
func (s *Store) UpdateHasMailingList(ctx context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error) {
	base, changed, err := s.CommitteeReaderWriter.UpdateHasMailingList(ctx, uid, hasMailingList)
	if err != nil || !changed {
		return base, changed, err
	}
	if s.mode.HasSecondary() {
		s.replicate(ctx, "update_has_mailing_list", uid, func(ctx context.Context) error { return s.replicateBase(ctx, uid) })
	}
	return base, changed, nil
}

// UpdateTotalMembers sets total_members; see port.CommitteeBaseWriter.
func (s *Store) UpdateTotalMembers(ctx context.Context, uid string, totalMembers int) (*model.CommitteeBase, bool, error) {
	base, changed, err := s.CommitteeReaderWriter.UpdateTotalMembers(ctx, uid, totalMembers)
	if err != nil || !changed {
		return base, changed, err
	}
	if s.mode.HasSecondary() {
		s.replicate(ctx, "update_total_members", uid, func(ctx context.Context) error { return s.replicateBase(ctx, uid) })
	}
	return base, changed, nil
}

// replicateBase writes the committee base the primary store holds now to the secondary store; see copyUpdated. Every update
// of the base is replicated this way, as the state the primary store holds, and not by running it again in the secondary
// store, which would stamp its own update time and leave the two records different.
func (s *Store) replicateBase(ctx context.Context, uid string) error {
	return copyUpdated(ctx, &s.core,
		func(ctx context.Context) (*model.CommitteeBase, uint64, error) { return s.src.GetBase(ctx, uid) },
		func(ctx context.Context) (*model.CommitteeBase, uint64, error) { return s.dst.GetBase(ctx, uid) },
		func(ctx context.Context, base *model.CommitteeBase, guard uint64) error {
			return s.dst.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, guard)
		})
}

// replicateSettings is replicateBase for the settings.
func (s *Store) replicateSettings(ctx context.Context, uid string) error {
	return copyUpdated(ctx, &s.core,
		func(ctx context.Context) (*model.CommitteeSettings, uint64, error) {
			return s.src.GetSettings(ctx, uid)
		},
		func(ctx context.Context) (*model.CommitteeSettings, uint64, error) {
			return s.dst.GetSettings(ctx, uid)
		},
		s.dst.UpdateSetting)
}

// The reads below return what the primary store returns. In dual mode a sampled fraction is also made against
// PostgreSQL and compared; a mismatch is counted and logged and the KV result is still the one returned.

// GetBase returns the committee base and its revision.
func (s *Store) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	base, revision, err := s.CommitteeReaderWriter.GetBase(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_committee", base, revision, err, func(ctx context.Context) (any, uint64, error) {
			return s.dst.GetBase(ctx, uid)
		})
	}
	return base, revision, err
}

// GetSettings returns the committee's settings and their revision.
func (s *Store) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	settings, revision, err := s.CommitteeReaderWriter.GetSettings(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_settings", settings, revision, err, func(ctx context.Context) (any, uint64, error) {
			return s.dst.GetSettings(ctx, uid)
		})
	}
	return settings, revision, err
}

// ListAllUIDs returns the uid of every committee.
func (s *Store) ListAllUIDs(ctx context.Context) ([]string, error) {
	uids, err := s.CommitteeReaderWriter.ListAllUIDs(ctx)
	if err == nil && s.sampled() {
		s.compareValue(ctx, "list_committees", func(ctx context.Context) (bool, error) {
			other, err := s.dst.ListAllUIDs(ctx)
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
			other, otherErr := s.dst.FindUIDByProjectAndName(ctx, projectUID, name)
			if otherErr != nil && !isNotFound(otherErr) {
				return false, otherErr
			}
			return isNotFound(err) == isNotFound(otherErr) && uid == other, nil
		})
	}
	return uid, err
}

// compareRecord compares a record the primary store returned with the one PostgreSQL returns for the same read.
func (s *core) compareRecord(ctx context.Context, operation string, record any, revision uint64, err error,
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
		return digest == otherDigest && s.sameRevision(revision, otherRevision), nil
	})
}

// compareValue runs a comparison with the secondary store under the compare timeout and records what came of it: a
// mismatch, or a comparison that could not be made.
func (s *core) compareValue(ctx context.Context, operation string, compare func(ctx context.Context) (same bool, err error)) {
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
