// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestStore_RecoverableMembers(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModeDual} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			var secondary port.CommitteeReaderWriter
			if mode.HasSecondary() {
				// Exercise the destination port with real KV, without requiring
				// a live PostgreSQL server or claiming SQL/revision parity.
				secondary = natstest.NewStore(t)
			}
			store, err := New(mode, natstest.NewStore(t), secondary)
			require.NoError(t, err)
			member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "member-1", CommitteeUID: "committee-1", Email: "first.last@example.com"}}
			selected, err := store.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
			require.NoError(t, err)
			require.Nil(t, selected)
			outcome, err := store.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{SkipNotification: true})
			require.NoError(t, err)
			require.True(t, outcome.Added)
			require.True(t, outcome.Intent.SkipNotification)
			if secondary != nil {
				copied, _, err := secondary.GetMember(ctx, member.UID)
				require.NoError(t, err, "a missing secondary must be CREATED, not updated")
				require.Equal(t, member.Email, copied.Email)
			}
			selected, err = store.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.False(t, selected.Pending)
			require.Equal(t, member.UID, selected.Member.UID)
			if secondary != nil {
				_, revision, err := secondary.GetMember(ctx, member.UID)
				require.NoError(t, err)
				require.NoError(t, secondary.DeleteMember(ctx, member.UID, revision))
			}
			outcome, err = store.CreateMemberRecoverable(ctx, member, port.MemberCreateIntent{})
			require.NoError(t, err)
			require.False(t, outcome.Added)
			if secondary != nil {
				_, _, err := secondary.GetMember(ctx, member.UID)
				require.NoError(t, err, "an already-member outcome repairs a missing secondary")
			}
			// The normal bulk already-member path must also reach convergence,
			// without enrichment, replacement validation, or publications.
			if secondary != nil {
				_, revision, err := secondary.GetMember(ctx, member.UID)
				require.NoError(t, err)
				require.NoError(t, secondary.DeleteMember(ctx, member.UID, revision))
			}
			base := model.CommitteeBase{UID: member.CommitteeUID, Name: "Example Committee", Category: "Other", EnableVoting: true}
			require.NoError(t, store.Create(ctx, &model.Committee{CommitteeBase: base, CommitteeSettings: &model.CommitteeSettings{}}))
			publisher := &mock.MockCommitteePublisher{}
			writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(store), service.WithCommitteeRetriever(store), service.WithCommitteePublisher(publisher))
			results, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{{CommitteeMemberBase: model.CommitteeMemberBase{Email: member.Email}}},
				service.BulkMemberCreateOptions{Enrich: func(context.Context, *model.CommitteeMember) { t.Error("already-member must skip enrichment") }})
			require.NoError(t, err)
			require.Equal(t, model.BulkReasonAlreadyMember, results[0].Reason)
			require.Equal(t, member.UID, results[0].MemberUID)
			require.Empty(t, publisher.IndexerMessages)
			require.Zero(t, publisher.MemberPutCallCount)
			require.Nil(t, publisher.LastEvent)
			if secondary != nil {
				_, _, err := secondary.GetMember(ctx, member.UID)
				require.NoError(t, err, "bulk discovery repairs the secondary")
			}
			require.Error(t, store.AbortMemberCreate(ctx, member), "a committed membership cannot be aborted")
			stored, _, err := store.GetMember(ctx, member.UID)
			require.NoError(t, err)
			require.Equal(t, member.Email, stored.Email)
		})
	}
	for _, mode := range []Mode{ModeDualPG, ModePostgres} {
		for _, conflict := range []bool{false, true} {
			name := string(mode)
			if conflict {
				name += " concurrent duplicate"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				// Use the primary port's non-recoverable contract, not a live SQL server.
				// This verifies orchestration/capability selection, not PostgreSQL transactions.
				primary := &transactionalPrimaryForBulk{CommitteeReaderWriter: natstest.NewStore(t), conflictAfterWrite: conflict}
				base := model.CommitteeBase{UID: "committee-1", Name: "Example Committee", Category: "Other"}
				require.NoError(t, primary.Create(ctx, &model.Committee{CommitteeBase: base, CommitteeSettings: &model.CommitteeSettings{}}))
				store, err := New(mode, natstest.NewStore(t), primary)
				require.NoError(t, err)
				require.False(t, store.SupportsMemberCreateRecovery())
				publisher := &mock.MockCommitteePublisher{}
				wrapped, err := WithCascade(store, primary)
				require.NoError(t, err)
				writer := service.NewCommitteeWriterOrchestrator(service.WithCommitteeWriter(wrapped), service.WithCommitteeRetriever(store), service.WithCommitteePublisher(publisher))
				row := func() *model.CommitteeMember {
					return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{Email: "first.last@example.com", FirstName: "First", LastName: "Last"}}
				}
				results, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row()}, service.BulkMemberCreateOptions{SkipEnrichment: true})
				require.NoError(t, err)
				if conflict {
					require.Equal(t, model.BulkReasonAlreadyMember, results[0].Reason)
					require.Empty(t, publisher.IndexerMessages)
				} else {
					require.Equal(t, model.BulkMemberAdded, results[0].Status)
				}
				repeated, err := writer.CreateMembers(ctx, base.UID, []*model.CommitteeMember{row()}, service.BulkMemberCreateOptions{SkipEnrichment: true})
				require.NoError(t, err)
				require.Equal(t, model.BulkReasonAlreadyMember, repeated[0].Reason)
				members, err := primary.ListMembersByCommittee(ctx, base.UID)
				require.NoError(t, err)
				require.Len(t, members, 1)
			})
		}
	}
}

type transactionalPrimaryForBulk struct {
	port.CommitteeReaderWriter
	port.CommitteeCascadeDeleter // No committee delete is invoked by these member tests.
	conflictAfterWrite           bool
}

func (s *transactionalPrimaryForBulk) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	if err := s.CommitteeReaderWriter.CreateMember(ctx, member); err != nil {
		return err
	}
	if s.conflictAfterWrite {
		return errs.NewConflict("another create committed the membership")
	}
	return nil
}

// pendingMemberSelection isolates discovery: no pending row may be mirrored.
type pendingMemberSelection struct {
	port.CommitteeReaderWriter
	port.RecoverableCommitteeMemberWriter
	member *model.CommitteeMember
}

func (s *pendingMemberSelection) FindMemberCreate(context.Context, string, string) (*port.MemberCreatePreparation, error) {
	return &port.MemberCreatePreparation{Member: s.member, Pending: true}, nil
}

func TestStore_FindMemberCreate(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "legacy committed discovery"
		if pending {
			name = "pending discovery"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			primary, secondary := natstest.NewStore(t), natstest.NewStore(t)
			member := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: "member-1", CommitteeUID: "committee-1", Email: "first.last@example.com", FirstName: "First", LastName: "Last"}}
			source := primary
			var before uint64
			if pending {
				source = &pendingMemberSelection{CommitteeReaderWriter: primary, RecoverableCommitteeMemberWriter: primary.(port.RecoverableCommitteeMemberWriter), member: member}
			} else {
				require.NoError(t, primary.CreateMember(ctx, member))
				_, before, _ = primary.GetMember(ctx, member.UID)
			}
			store, err := New(ModeDual, source, secondary)
			require.NoError(t, err)
			selected, err := store.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
			require.NoError(t, err)
			require.Equal(t, pending, selected.Pending)
			copied, _, err := secondary.GetMember(ctx, member.UID)
			if pending {
				var notFound errs.NotFound
				require.ErrorAs(t, err, &notFound)
			} else {
				require.NoError(t, err)
				require.Equal(t, member.Email, copied.Email)
				_, after, err := primary.GetMember(ctx, member.UID)
				require.NoError(t, err)
				require.Greater(t, after, before, "discovery fences the legacy primary before copying")
			}
		})
	}
}
