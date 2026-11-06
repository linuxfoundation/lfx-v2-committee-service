// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// The writes of the applications return what the primary store returns. In dual mode each is followed by the same change in
// PostgreSQL, from what KV holds after the write, with the revision KV gave it; see Store.

// CreateApplication creates a committee application.
func (s *Store) CreateApplication(ctx context.Context, application *model.CommitteeApplication) error {
	if err := s.CommitteeReaderWriter.CreateApplication(ctx, application); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := application.UID
		s.replicate(ctx, "create_application", uid, func(ctx context.Context) error {
			return copyCreated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeApplication, uint64, error) {
				return s.src.GetApplication(ctx, uid)
			},
				s.dst.CreateApplication, func(ctx context.Context, _ uint64) error {
					return s.dst.DeleteApplication(ctx, application)
				}, func(ctx context.Context) error { return s.copyApplicationUpdate(ctx, uid) })
		})
	}
	return nil
}

// UpdateApplication updates a committee application, guarded by the revision.
func (s *Store) UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateApplication(ctx, application, revision); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := application.UID
		s.replicate(ctx, "update_application", uid, func(ctx context.Context) error { return s.copyApplicationUpdate(ctx, uid) })
	}
	return nil
}

// copyApplicationUpdate writes the application KV holds now to PostgreSQL; see copyUpdated.
func (s *Store) copyApplicationUpdate(ctx context.Context, uid string) error {
	return copyUpdated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeApplication, uint64, error) {
		return s.src.GetApplication(ctx, uid)
	},
		func(ctx context.Context) (*model.CommitteeApplication, uint64, error) {
			return s.dst.GetApplication(ctx, uid)
		},
		s.dst.UpdateApplication)
}

// DeleteApplication removes a committee application. It has no revision to check, and deleting one that is already
// gone is not an error, in either store.
func (s *Store) DeleteApplication(ctx context.Context, application *model.CommitteeApplication) error {
	if err := s.CommitteeReaderWriter.DeleteApplication(ctx, application); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		s.replicate(ctx, "delete_application", application.UID, func(ctx context.Context) error { return s.dst.DeleteApplication(ctx, application) })
	}
	return nil
}

// GetApplication returns the application and its revision.
func (s *Store) GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error) {
	application, revision, err := s.CommitteeReaderWriter.GetApplication(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_application", application, revision, err, func(ctx context.Context) (any, uint64, error) {
			return s.dst.GetApplication(ctx, uid)
		})
	}
	return application, revision, err
}

// ListApplications returns the applications of the committee.
func (s *Store) ListApplications(ctx context.Context, committeeUID string) ([]*model.CommitteeApplication, error) {
	applications, err := s.CommitteeReaderWriter.ListApplications(ctx, committeeUID)
	if s.sampled() {
		compareRecords(ctx, s, "list_applications", applications, err, func(ctx context.Context) ([]*model.CommitteeApplication, error) {
			return s.dst.ListApplications(ctx, committeeUID)
		})
	}
	return applications, err
}

// ListAllApplications returns every application. It reads the whole set, for repair work, and goes to the primary store
// alone: a comparison of the whole set is what storage verify is for.
func (s *Store) ListAllApplications(ctx context.Context) ([]*model.CommitteeApplication, error) {
	return s.CommitteeReaderWriter.ListAllApplications(ctx)
}
