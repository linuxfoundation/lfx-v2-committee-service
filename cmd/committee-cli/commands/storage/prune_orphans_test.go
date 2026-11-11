// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// deletedMember is a DeleteMember call.
type deletedMember struct {
	uid      string
	revision uint64
}

// recordingMemberWriter records the deletes and fails the uids in failWith.
type recordingMemberWriter struct {
	deleted  []deletedMember
	failWith map[string]error
}

func (w *recordingMemberWriter) CreateMember(context.Context, *model.CommitteeMember) error {
	return errors.New("prune-orphans must not create members")
}

func (w *recordingMemberWriter) UpdateMember(context.Context, *model.CommitteeMember, uint64) (*model.CommitteeMember, error) {
	return nil, errors.New("prune-orphans must not update members")
}

func (w *recordingMemberWriter) DeleteMember(_ context.Context, uid string, revision uint64) error {
	if err := w.failWith[uid]; err != nil {
		return err
	}
	w.deleted = append(w.deleted, deletedMember{uid: uid, revision: revision})
	return nil
}

// recordingPublisher records the indexer messages and everything else that is published, which a prune must not do.
type recordingPublisher struct {
	indexer    []string // the subjects
	indexerErr error
	other      []string // the other publishes
}

func (p *recordingPublisher) Indexer(_ context.Context, subject string, _ any, _ bool) error {
	if p.indexerErr != nil {
		return p.indexerErr
	}
	p.indexer = append(p.indexer, subject)
	return nil
}
func (p *recordingPublisher) UpdateAccess(context.Context, any) error {
	p.other = append(p.other, "UpdateAccess")
	return nil
}
func (p *recordingPublisher) DeleteAccess(context.Context, any) error {
	p.other = append(p.other, "DeleteAccess")
	return nil
}
func (p *recordingPublisher) MemberPut(context.Context, any) error {
	p.other = append(p.other, "MemberPut")
	return nil
}
func (p *recordingPublisher) MemberRemove(context.Context, any) error {
	p.other = append(p.other, "MemberRemove")
	return nil
}
func (p *recordingPublisher) Event(_ context.Context, subject string, _ any, _ bool) error {
	p.other = append(p.other, "Event "+subject)
	return nil
}

// pruneFixture has two committees in KV, three members of them and three members of a committee that is gone.
func pruneFixture() *fakeSource {
	return &fakeSource{
		committees: []port.CommitteeExport{kvCommittee("c-1", "One", 10, 11), kvCommittee("c-2", "Two", 12, 13)},
		members: []memberRecord{
			kvMember("m-1", "c-1", "first.last1@example.com", 20),
			kvMember("m-2", "c-2", "first.last2@example.com", 21),
			kvMember("o-1", "c-gone", "first.last3@example.com", 30),
			kvMember("o-2", "c-gone", "first.last4@example.com", 31),
			kvMember("o-3", "c-gone-2", "first.last5@example.com", 32),
		},
	}
}

// runPrune runs the subcommand and returns what it printed.
func runPrune(t *testing.T, src *fakeSource, writer *recordingMemberWriter, publisher *recordingPublisher, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	sub := &pruneOrphansSubcommand{out: &out}
	err := sub.Run(context.Background(), commands.RunContext{
		KVExport:              src,
		CommitteeMemberWriter: writer,
		Publisher:             publisher,
		Args:                  args,
	})
	return out.String(), err
}

func TestPruneOrphans_DryRunDeletesNothing(t *testing.T) {
	writer, publisher := &recordingMemberWriter{}, &recordingPublisher{}

	out, err := runPrune(t, pruneFixture(), writer, publisher)

	require.NoError(t, err)
	assert.Empty(t, writer.deleted)
	assert.Empty(t, publisher.indexer)
	assert.Empty(t, publisher.other)
	assert.Contains(t, out, "orphan members (committee not in KV): 3 in 2 committees")
	assert.Contains(t, out, "committee c-gone: 2")
	assert.Contains(t, out, "nothing was deleted")
}

func TestPruneOrphans_ApplyDeletesOnlyTheOrphans(t *testing.T) {
	writer, publisher := &recordingMemberWriter{}, &recordingPublisher{}

	out, err := runPrune(t, pruneFixture(), writer, publisher, "--apply")

	require.NoError(t, err)
	assert.Equal(t, []deletedMember{{"o-1", 30}, {"o-2", 31}, {"o-3", 32}}, writer.deleted, "each orphan is deleted at the revision KV holds it at, and the members of a committee that exists stay")
	assert.Equal(t, []string{constants.IndexCommitteeMemberSubject, constants.IndexCommitteeMemberSubject, constants.IndexCommitteeMemberSubject}, publisher.indexer)
	assert.Empty(t, publisher.other, "no deleted event (it notifies the person) and no access message")
	assert.Contains(t, out, "deleted=3 already_gone=0 failed=0 indexer_failed=0")
}

func TestPruneOrphans_AlreadyGoneIsNotAFailure(t *testing.T) {
	writer := &recordingMemberWriter{failWith: map[string]error{"o-2": errs.NewNotFound("member not found")}}
	publisher := &recordingPublisher{}

	out, err := runPrune(t, pruneFixture(), writer, publisher, "--apply")

	require.NoError(t, err)
	assert.Contains(t, out, "deleted=2 already_gone=1 failed=0")
	assert.Len(t, publisher.indexer, 3, "the indexer is told about a member that was already gone too, in case its document stayed")
}

func TestPruneOrphans_AFailedDeleteIsReportedAndSendsNoIndexerMessage(t *testing.T) {
	writer := &recordingMemberWriter{failWith: map[string]error{"o-2": errs.NewConflict("modified")}}
	publisher := &recordingPublisher{}

	out, err := runPrune(t, pruneFixture(), writer, publisher, "--apply")

	require.Error(t, err)
	assert.Contains(t, out, "deleted=2 already_gone=0 failed=1")
	assert.Len(t, publisher.indexer, 2, "a member that is still there keeps its document")
}

func TestPruneOrphans_AFailedIndexerMessageFailsTheRun(t *testing.T) {
	writer := &recordingMemberWriter{}
	publisher := &recordingPublisher{indexerErr: errors.New("nats down")}

	out, err := runPrune(t, pruneFixture(), writer, publisher, "--apply")

	require.Error(t, err)
	assert.Len(t, writer.deleted, 3, "the records are deleted whatever the indexer does")
	assert.Contains(t, out, "indexer_failed=3")
}

func TestPruneOrphans_RefusesMoreThanMax(t *testing.T) {
	writer, publisher := &recordingMemberWriter{}, &recordingPublisher{}

	_, err := runPrune(t, pruneFixture(), writer, publisher, "--apply", "--max=2")

	require.Error(t, err)
	assert.Empty(t, writer.deleted)
	assert.Empty(t, publisher.indexer)
}

func TestPruneOrphans_RefusesWhenKVHasNoCommittees(t *testing.T) {
	src := pruneFixture()
	src.committees = nil
	writer := &recordingMemberWriter{}

	_, err := runPrune(t, src, writer, &recordingPublisher{}, "--apply")

	require.Error(t, err)
	assert.Empty(t, writer.deleted, "an empty committee list must not make every member an orphan")
}

func TestPruneOrphans_APartialScanDeletesNothing(t *testing.T) {
	src := pruneFixture()
	src.memberErr = errors.New("a member could not be read")
	writer := &recordingMemberWriter{}

	_, err := runPrune(t, src, writer, &recordingPublisher{}, "--apply")

	require.Error(t, err)
	assert.Empty(t, writer.deleted)
}

func TestPruneOrphans_CommitteeListFailureDeletesNothing(t *testing.T) {
	src := pruneFixture()
	src.listErr = errors.New("kv unavailable")
	writer := &recordingMemberWriter{}

	_, err := runPrune(t, src, writer, &recordingPublisher{}, "--apply")

	require.Error(t, err)
	assert.Empty(t, writer.deleted)
}

func TestPruneOrphans_RejectsAStrayArgumentBeforeReadingAnything(t *testing.T) {
	src := pruneFixture()
	writer := &recordingMemberWriter{}

	_, err := runPrune(t, src, writer, &recordingPublisher{}, "members", "--apply")

	require.Error(t, err)
	assert.Zero(t, src.listCalls)
	assert.Empty(t, writer.deleted)
}

func TestPruneOrphans_NeedsItsWiring(t *testing.T) {
	err := (&pruneOrphansSubcommand{}).Run(context.Background(), commands.RunContext{})

	require.Error(t, err)
}
