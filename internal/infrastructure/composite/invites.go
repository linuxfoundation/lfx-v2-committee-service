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
	if s.mode.HasSecondary() {
		uid := invite.UID
		s.replicate(ctx, "create_invite", uid, func(ctx context.Context) error {
			// An invite is never deleted on its own (port.CommitteeInviteWriter has no delete), so there is no delete to race
			// with and nothing to take out again.
			return copyCreated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeInvite, uint64, error) { return s.src.GetInvite(ctx, uid) },
				s.dst.CreateInvite, func(context.Context, uint64) error { return nil },
				func(ctx context.Context) error { return s.copyInviteUpdate(ctx, uid) })
		})
	}
	return nil
}

// UpdateInvite updates a committee invite, guarded by the revision.
func (s *Store) UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error {
	if err := s.CommitteeReaderWriter.UpdateInvite(ctx, invite, revision); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := invite.UID
		s.replicate(ctx, "update_invite", uid, func(ctx context.Context) error { return s.copyInviteUpdate(ctx, uid) })
	}
	return nil
}

// copyInviteUpdate writes the invite the primary store holds now to the secondary store; see copyUpdated.
func (s *Store) copyInviteUpdate(ctx context.Context, uid string) error {
	return copyUpdated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeInvite, uint64, error) { return s.src.GetInvite(ctx, uid) },
		func(ctx context.Context) (*model.CommitteeInvite, uint64, error) { return s.dst.GetInvite(ctx, uid) },
		s.dst.UpdateInvite)
}

// GetInvite returns the invite and its revision.
func (s *Store) GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error) {
	invite, revision, err := s.CommitteeReaderWriter.GetInvite(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_invite", invite, revision, err, func(ctx context.Context) (any, uint64, error) { return s.dst.GetInvite(ctx, uid) })
	}
	return invite, revision, err
}

// ListInvites returns the invites of the committee.
func (s *Store) ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	invites, err := s.CommitteeReaderWriter.ListInvites(ctx, committeeUID)
	if s.sampled() {
		compareRecords(ctx, s, "list_invites", invites, err, func(ctx context.Context) ([]*model.CommitteeInvite, error) {
			return s.dst.ListInvites(ctx, committeeUID)
		})
	}
	return invites, err
}

// ListAllInvites returns every invite. It reads the whole set, for repair work, and goes to the primary store alone: a
// comparison of the whole set is what storage verify is for.
func (s *Store) ListAllInvites(ctx context.Context) ([]*model.CommitteeInvite, error) {
	return s.CommitteeReaderWriter.ListAllInvites(ctx)
}
