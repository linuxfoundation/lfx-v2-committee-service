// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// The writes of the members return what the primary store returns. In dual mode each is followed by the same change in
// PostgreSQL, from what KV holds after the write, with the revision KV gave it; see Store.

// CreateMember creates a committee member.
func (s *Store) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	if err := s.CommitteeReaderWriter.CreateMember(ctx, member); err != nil {
		return err
	}
	if s.mode == ModeDual {
		uid := member.UID
		s.replicate(ctx, "create_member", uid, func(ctx context.Context) error {
			return copyCreated(ctx, func(ctx context.Context) (*model.CommitteeMember, uint64, error) { return s.kv.GetMember(ctx, uid) },
				s.pg.CreateMember, func(ctx context.Context, revision uint64) error {
					return s.deleteMemberInPostgreSQL(ctx, uid, revision)
				},
				func(ctx context.Context) error { return s.copyMemberUpdate(ctx, uid) })
		})
	}
	return nil
}

// UpdateMember updates a committee member, guarded by the revision.
func (s *Store) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	updated, err := s.CommitteeReaderWriter.UpdateMember(ctx, member, revision)
	if err != nil {
		return updated, err
	}
	if s.mode == ModeDual {
		uid := member.UID
		s.replicate(ctx, "update_member", uid, func(ctx context.Context) error { return s.copyMemberUpdate(ctx, uid) })
	}
	return updated, nil
}

// copyMemberUpdate writes the member KV holds now to PostgreSQL; see copyUpdated.
func (s *Store) copyMemberUpdate(ctx context.Context, uid string) error {
	return copyUpdated(ctx, func(ctx context.Context) (*model.CommitteeMember, uint64, error) { return s.kv.GetMember(ctx, uid) },
		func(ctx context.Context) (uint64, error) { return s.pg.GetMemberRevision(ctx, uid) },
		func(ctx context.Context, current *model.CommitteeMember, guard uint64) error {
			_, err := s.pg.UpdateMember(ctx, current, guard)
			return err
		})
}

// DeleteMember removes a committee member, guarded by the revision.
func (s *Store) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	if err := s.CommitteeReaderWriter.DeleteMember(ctx, uid, revision); err != nil {
		return err
	}
	if s.mode == ModeDual {
		s.replicate(ctx, "delete_member", uid, func(ctx context.Context) error { return s.deleteMemberInPostgreSQL(ctx, uid, revision) })
	}
	return nil
}

// deleteMemberInPostgreSQL removes the member from PostgreSQL after KV decided that it is gone; see deleteConverged.
func (s *Store) deleteMemberInPostgreSQL(ctx context.Context, uid string, revision uint64) error {
	return deleteConverged(ctx,
		func(ctx context.Context, revision uint64) error { return s.pg.DeleteMember(ctx, uid, revision) },
		func(ctx context.Context) (uint64, error) { return s.pg.GetMemberRevision(ctx, uid) }, revision)
}

// The reads of the members return what the primary store returns. In dual mode a sampled fraction is also made against
// PostgreSQL and compared. EachMember, EachMemberStrict and ListAllMembers read the whole set for repair work and go to the
// primary store alone.

// GetMember returns the member and its revision.
func (s *Store) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	member, revision, err := s.CommitteeReaderWriter.GetMember(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_member", member, revision, err, func(ctx context.Context) (any, uint64, error) { return s.pg.GetMember(ctx, uid) })
	}
	return member, revision, err
}

// GetMemberRevision returns the revision of the member.
func (s *Store) GetMemberRevision(ctx context.Context, uid string) (uint64, error) {
	revision, err := s.CommitteeReaderWriter.GetMemberRevision(ctx, uid)
	if s.sampled() && (err == nil || isNotFound(err)) {
		s.compareValue(ctx, "get_member_revision", func(ctx context.Context) (bool, error) {
			other, otherErr := s.pg.GetMemberRevision(ctx, uid)
			if otherErr != nil && !isNotFound(otherErr) {
				return false, otherErr
			}
			return isNotFound(err) == isNotFound(otherErr) && revision == other, nil
		})
	}
	return revision, err
}

// ListMembersByCommittee returns the members of the committee.
func (s *Store) ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByCommittee(ctx, committeeUID)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_committee", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.pg.ListMembersByCommittee(ctx, committeeUID)
		})
	}
	return members, err
}

// ListMembersByOrganization returns the members held by the organization.
func (s *Store) ListMembersByOrganization(ctx context.Context, orgSFID string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByOrganization(ctx, orgSFID)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_organization", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.pg.ListMembersByOrganization(ctx, orgSFID)
		})
	}
	return members, err
}

// ListMembersByEmail returns the members with the email.
func (s *Store) ListMembersByEmail(ctx context.Context, email string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByEmail(ctx, email)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_email", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.pg.ListMembersByEmail(ctx, email)
		})
	}
	return members, err
}

// ListMembersByUsername returns the members with the username.
func (s *Store) ListMembersByUsername(ctx context.Context, username string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByUsername(ctx, username)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_username", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.pg.ListMembersByUsername(ctx, username)
		})
	}
	return members, err
}

// ListAllMembers returns every member. It reads the whole set, for repair work, and goes to the primary store alone, as
// EachMember does: a comparison of the whole set is what storage verify is for.
func (s *Store) ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error) {
	return s.CommitteeReaderWriter.ListAllMembers(ctx)
}
