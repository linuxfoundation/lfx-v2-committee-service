// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// The writes of the invites return what the primary store returns. In dual mode each is followed by the same change in
// PostgreSQL, from what KV holds after the write, with the revision KV gave it; see Store. The invites have no delete.

// CreateInvite creates a committee invite.
func (s *Store) CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error {
	if err := s.CommitteeReaderWriter.CreateInvite(ctx, invite); err != nil {
		return err
	}
	if s.mode == ModeDual {
		uid := invite.UID
		s.replicate(ctx, "create_invite", func(ctx context.Context) error {
			// An invite is never deleted on its own, so there is no delete to race with and nothing to take out again.
			return copyCreated(ctx, func(ctx context.Context) (*model.CommitteeInvite, uint64, error) { return s.kv.GetInvite(ctx, uid) },
				s.pg.CreateInvite, func(context.Context, uint64) error { return nil })
		})
	}
	return nil
}

// UpdateInvite updates a committee invite, guarded by the revision.
func (s *Store) UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateInvite(ctx, invite, revision); err != nil {
		return err
	}
	if s.mode == ModeDual {
		uid := invite.UID
		s.replicate(ctx, "update_invite", func(ctx context.Context) error {
			return copyUpdated(ctx, func(ctx context.Context) (*model.CommitteeInvite, uint64, error) { return s.kv.GetInvite(ctx, uid) },
				func(ctx context.Context) (uint64, error) {
					_, current, err := s.pg.GetInvite(ctx, uid)
					return current, err
				}, s.pg.UpdateInvite)
		})
	}
	return nil
}

// GetInvite returns the invite and its revision.
func (s *Store) GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error) {
	invite, revision, err := s.CommitteeReaderWriter.GetInvite(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_invite", invite, revision, err, func(ctx context.Context) (any, uint64, error) { return s.pg.GetInvite(ctx, uid) })
	}
	return invite, revision, err
}

// ListInvites returns the invites of the committee.
func (s *Store) ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	invites, err := s.CommitteeReaderWriter.ListInvites(ctx, committeeUID)
	if s.sampled() {
		compareRecords(ctx, s, "list_invites", invites, err, func(ctx context.Context) ([]*model.CommitteeInvite, error) {
			return s.pg.ListInvites(ctx, committeeUID)
		})
	}
	return invites, err
}

// ListAllInvites returns every invite.
func (s *Store) ListAllInvites(ctx context.Context) ([]*model.CommitteeInvite, error) {
	invites, err := s.CommitteeReaderWriter.ListAllInvites(ctx)
	if s.sampled() {
		compareRecords(ctx, s, "list_all_invites", invites, err, s.pg.ListAllInvites)
	}
	return invites, err
}
