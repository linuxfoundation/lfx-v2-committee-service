// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The writes of the members return what the primary store returns. In dual mode each is followed by the same change in
// PostgreSQL, from what KV holds after the write, with the revision KV gave it; see Store.

// CreateMember creates a committee member.
func (s *Store) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	if err := s.CommitteeReaderWriter.CreateMember(ctx, member); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		uid := member.UID
		s.replicate(ctx, "create_member", uid, func(ctx context.Context) error { return s.copyMemberCreate(ctx, uid) })
	}
	return nil
}

// UpdateMember updates a committee member, guarded by the revision.
func (s *Store) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	updated, err := s.CommitteeReaderWriter.UpdateMember(ctx, member, revision)
	if err != nil {
		return updated, err
	}
	if s.mode.HasSecondary() {
		uid := member.UID
		s.replicate(ctx, "update_member", uid, func(ctx context.Context) error { return s.copyMemberUpdate(ctx, uid) })
	}
	return updated, nil
}

// copyMemberCreate copies the member the primary store holds to the secondary store; see copyCreated.
func (s *Store) copyMemberCreate(ctx context.Context, uid string) error {
	return copyCreated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeMember, uint64, error) { return s.src.GetMember(ctx, uid) },
		s.dst.CreateMember, func(ctx context.Context, revision uint64) error {
			return s.deleteMemberInSecondary(ctx, uid, revision)
		},
		func(ctx context.Context) error { return s.copyMemberUpdate(ctx, uid) })
}

// copyMemberUpdate writes the member the primary store holds now to the secondary store; see copyUpdated.
func (s *Store) copyMemberUpdate(ctx context.Context, uid string) error {
	return copyUpdated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeMember, uint64, error) { return s.src.GetMember(ctx, uid) },
		func(ctx context.Context) (*model.CommitteeMember, uint64, error) { return s.dst.GetMember(ctx, uid) },
		func(ctx context.Context, current *model.CommitteeMember, guard uint64) error {
			_, err := s.dst.UpdateMember(ctx, current, guard)
			return err
		}, func(ctx context.Context) error { return s.copyMemberCreate(ctx, uid) })
}

// DeleteMember removes a committee member, guarded by the revision.
func (s *Store) DeleteMember(ctx context.Context, uid string, revision uint64) error {
	if err := s.CommitteeReaderWriter.DeleteMember(ctx, uid, revision); err != nil {
		return err
	}
	if s.mode.HasSecondary() {
		s.replicate(ctx, "delete_member", uid, func(ctx context.Context) error { return s.deleteMemberInSecondary(ctx, uid, revision) })
	}
	return nil
}

// deleteMemberInSecondary removes the member from the secondary store after the primary store decided that it is gone; see
// deleteConverged.
func (s *Store) deleteMemberInSecondary(ctx context.Context, uid string, revision uint64) error {
	return deleteConverged(ctx, &s.core,
		func(ctx context.Context, revision uint64) error { return s.dst.DeleteMember(ctx, uid, revision) },
		func(ctx context.Context) (uint64, error) { return s.dst.GetMemberRevision(ctx, uid) }, revision)
}

// The reads of the members return what the primary store returns. In dual mode a sampled fraction is also made against
// PostgreSQL and compared. EachMember, EachMemberStrict and ListAllMembers read the whole set for repair work and go to the
// primary store alone.

// GetMember returns the member and its revision.
func (s *Store) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	member, revision, err := s.CommitteeReaderWriter.GetMember(ctx, uid)
	if s.sampled() {
		s.compareRecord(ctx, "get_member", member, revision, err, func(ctx context.Context) (any, uint64, error) { return s.dst.GetMember(ctx, uid) })
	}
	return member, revision, err
}

// GetMemberRevision returns the revision of the member.
func (s *Store) GetMemberRevision(ctx context.Context, uid string) (uint64, error) {
	revision, err := s.CommitteeReaderWriter.GetMemberRevision(ctx, uid)
	if s.sampled() && (err == nil || isNotFound(err)) {
		s.compareValue(ctx, "get_member_revision", func(ctx context.Context) (bool, error) {
			other, otherErr := s.dst.GetMemberRevision(ctx, uid)
			if otherErr != nil && !isNotFound(otherErr) {
				return false, otherErr
			}
			return isNotFound(err) == isNotFound(otherErr) && s.sameRevision(revision, other), nil
		})
	}
	return revision, err
}

// ListMembersByCommittee returns the members of the committee.
func (s *Store) ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByCommittee(ctx, committeeUID)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_committee", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.dst.ListMembersByCommittee(ctx, committeeUID)
		})
	}
	return members, err
}

// ListMembersByOrganization returns the members held by the organization.
func (s *Store) ListMembersByOrganization(ctx context.Context, orgSFID string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByOrganization(ctx, orgSFID)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_organization", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.dst.ListMembersByOrganization(ctx, orgSFID)
		})
	}
	return members, err
}

// ListMembersByEmail returns the members with the email.
func (s *Store) ListMembersByEmail(ctx context.Context, email string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByEmail(ctx, email)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_email", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.dst.ListMembersByEmail(ctx, email)
		})
	}
	return members, err
}

// ListMembersByUsername returns the members with the username.
func (s *Store) ListMembersByUsername(ctx context.Context, username string) ([]*model.CommitteeMember, error) {
	members, err := s.CommitteeReaderWriter.ListMembersByUsername(ctx, username)
	if s.sampled() {
		compareRecords(ctx, s, "list_members_by_username", members, err, func(ctx context.Context) ([]*model.CommitteeMember, error) {
			return s.dst.ListMembersByUsername(ctx, username)
		})
	}
	return members, err
}

// ListAllMembers returns every member. It reads the whole set, for repair work, and goes to the primary store alone, as
// EachMember does: a comparison of the whole set is what storage verify is for.
func (s *Store) ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error) {
	return s.CommitteeReaderWriter.ListAllMembers(ctx)
}

// CreateMemberRecoverable forwards bulk completion to the authoritative store.
// Only a committed member may be copied; pending recovery state is never replicated.
func (s *Store) CreateMemberRecoverable(ctx context.Context, member *model.CommitteeMember, intent port.MemberCreateIntent) (*port.MemberCreateResult, error) {
	writer, ok := s.CommitteeReaderWriter.(port.RecoverableCommitteeMemberWriter)
	if !ok {
		return nil, errs.NewServiceUnavailable("recoverable member creation is unavailable")
	}
	outcome, err := writer.CreateMemberRecoverable(ctx, member, intent)
	if err != nil {
		return nil, err
	}
	if outcome != nil && outcome.Member != nil && s.mode.HasSecondary() {
		uid := outcome.Member.UID
		s.replicate(ctx, "create_member", uid, func(ctx context.Context) error {
			if _, _, err := s.dst.GetMember(ctx, uid); !isNotFound(err) {
				if err != nil {
					return err
				}
				return s.copyMemberUpdate(ctx, uid)
			}
			return copyCreated(ctx, &s.core, func(ctx context.Context) (*model.CommitteeMember, uint64, error) { return s.src.GetMember(ctx, uid) },
				s.dst.CreateMember, func(ctx context.Context, revision uint64) error { return s.deleteMemberInSecondary(ctx, uid, revision) },
				func(ctx context.Context) error { return s.copyMemberUpdate(ctx, uid) })
		})
	}
	return outcome, nil
}

// FindMemberCreate selects only from the authoritative store before validation.
func (s *Store) FindMemberCreate(ctx context.Context, committeeUID, email string) (*port.MemberCreatePreparation, error) {
	writer, ok := s.CommitteeReaderWriter.(port.RecoverableCommitteeMemberWriter)
	if !ok {
		return nil, errs.NewServiceUnavailable("recoverable member creation is unavailable")
	}
	return writer.FindMemberCreate(ctx, committeeUID, email)
}

// AbortMemberCreate fences a rejected pending operation in the authoritative store.
func (s *Store) AbortMemberCreate(ctx context.Context, member *model.CommitteeMember) error {
	writer, ok := s.CommitteeReaderWriter.(port.RecoverableCommitteeMemberWriter)
	if !ok {
		return errs.NewServiceUnavailable("recoverable member creation is unavailable")
	}
	return writer.AbortMemberCreate(ctx, member)
}

// SupportsMemberCreateRecovery reports the authoritative store's capability.
func (s *Store) SupportsMemberCreateRecovery() bool {
	writer, ok := s.CommitteeReaderWriter.(port.RecoverableCommitteeMemberWriter)
	return ok && writer.SupportsMemberCreateRecovery()
}

// AbortMemberCreatesForIdentity also clears old KV preparations when KV is secondary.
func (s *Store) AbortMemberCreatesForIdentity(ctx context.Context, username, email string) error {
	for _, store := range []port.CommitteeReaderWriter{s.src, s.dst} {
		if writer, ok := store.(port.RecoverableCommitteeMemberWriter); ok {
			if err := writer.AbortMemberCreatesForIdentity(ctx, username, email); err != nil {
				return err
			}
		}
	}
	return nil
}
