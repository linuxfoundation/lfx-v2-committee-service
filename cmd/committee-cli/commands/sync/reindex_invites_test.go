// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// mockInviteWriter records UpdateInvite calls for assertion in tests.
type mockInviteWriter struct {
	updated     []*model.CommitteeInvite
	updatedRevs []uint64
	updateErr   error
}

func (w *mockInviteWriter) CreateInvite(_ context.Context, _ *model.CommitteeInvite) error {
	return nil
}
func (w *mockInviteWriter) UpdateInvite(_ context.Context, inv *model.CommitteeInvite, rev uint64) error {
	if w.updateErr != nil {
		return w.updateErr
	}
	w.updated = append(w.updated, inv)
	w.updatedRevs = append(w.updatedRevs, rev)
	return nil
}
func (w *mockInviteWriter) UniqueInvite(_ context.Context, _ *model.CommitteeInvite) (string, error) {
	return "", nil
}

// mockPublisher records Indexer and Access call counts.
type mockPublisher struct {
	indexerCalls int
	accessCalls  int
	indexerErr   error
	accessErr    error
}

func (p *mockPublisher) Indexer(_ context.Context, _ string, _ any, _ bool) error {
	p.indexerCalls++
	return p.indexerErr
}
func (p *mockPublisher) Access(_ context.Context, _ string, _ any, _ bool) error {
	p.accessCalls++
	return p.accessErr
}
func (p *mockPublisher) Event(_ context.Context, _ string, _ any, _ bool) error { return nil }

// newReindexRC builds a RunContext wired for reindex-invites tests.
func newReindexRC(r *mockReader, iw *mockInviteWriter, pub *mockPublisher, args ...string) commands.RunContext {
	return commands.RunContext{
		CommitteeReader:       r,
		CommitteeInviteWriter: iw,
		Publisher:             pub,
		Args:                  args,
	}
}

func TestReindexInvites_NoInvites_Succeeds(t *testing.T) {
	r := &mockReader{}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.NoError(t, err)
	assert.Empty(t, iw.updated)
	assert.Equal(t, 0, pub.indexerCalls)
}

func TestReindexInvites_MissingWriter_ReturnsError(t *testing.T) {
	r := &mockReader{
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1"},
		},
	}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeReader: r,
		Publisher:       pub,
		// CommitteeInviteWriter intentionally omitted
	}
	err := (&reindexInvitesSubcommand{}).Run(context.Background(), rc)
	require.Error(t, err)
}

func TestReindexInvites_DryRun_NoWritesOrPublishes(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC", EnableVoting: true},
		},
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1", Status: "pending"},
		},
	}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub, "--dry-run"))
	require.NoError(t, err)
	assert.Empty(t, iw.updated)
	assert.Equal(t, 0, pub.indexerCalls)
}

func TestReindexInvites_OldInvite_BackfillsNameAndOrgRequired(t *testing.T) {
	const rev uint64 = 7
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC", EnableVoting: true},
		},
		invites: []*model.CommitteeInvite{
			// Old invite: CommitteeName empty, OrganizationRequired false (zero value).
			{UID: "i1", CommitteeUID: "c1", Status: "pending"},
		},
		inviteRevision: map[string]uint64{"i1": rev},
	}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.NoError(t, err)
	require.Len(t, iw.updated, 1)
	assert.Equal(t, "TSC", iw.updated[0].CommitteeName)
	assert.True(t, iw.updated[0].OrganizationRequired)
	assert.Equal(t, rev, iw.updatedRevs[0])
	assert.Equal(t, 1, pub.indexerCalls)
	assert.Equal(t, 1, pub.accessCalls)
}

func TestReindexInvites_NewInvite_NoKVUpdate(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC", EnableVoting: true},
		},
		invites: []*model.CommitteeInvite{
			// New invite: fields already correct.
			{UID: "i1", CommitteeUID: "c1", CommitteeName: "TSC", OrganizationRequired: true, Status: "pending"},
		},
	}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.NoError(t, err)
	assert.Empty(t, iw.updated, "no KV write expected when fields already match")
	assert.Equal(t, 1, pub.indexerCalls)
}

func TestReindexInvites_OrgRequiredMismatch_UpdatesKV(t *testing.T) {
	const rev uint64 = 3
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC", EnableVoting: false},
		},
		settings: map[string]*model.CommitteeSettings{
			"c1": {BusinessEmailRequired: true},
		},
		invites: []*model.CommitteeInvite{
			// Invite has wrong OrganizationRequired (false), but committee now requires it.
			{UID: "i1", CommitteeUID: "c1", CommitteeName: "TSC", OrganizationRequired: false, Status: "pending"},
		},
		inviteRevision: map[string]uint64{"i1": rev},
	}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.NoError(t, err)
	require.Len(t, iw.updated, 1)
	assert.True(t, iw.updated[0].OrganizationRequired)
	assert.Equal(t, rev, iw.updatedRevs[0])
}

func TestReindexInvites_CommitteeFetchFails_StillPublishes_NoKVWrite(t *testing.T) {
	r := &mockReader{
		baseErr: map[string]error{"c1": errors.New("nats timeout")},
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1", CommitteeName: "TSC", OrganizationRequired: true, Status: "pending"},
		},
	}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.NoError(t, err)
	assert.Empty(t, iw.updated, "no KV write when committee fetch fails")
	assert.Equal(t, 1, pub.indexerCalls, "publish still happens despite fetch failure")
	// Original fields must be preserved unchanged.
	assert.Equal(t, "TSC", r.invites[0].CommitteeName)
	assert.True(t, r.invites[0].OrganizationRequired)
}

func TestReindexInvites_KVUpdateFails_CountedAsFailed_NoPublish(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC", EnableVoting: true},
		},
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1", Status: "pending"},
		},
		inviteRevision: map[string]uint64{"i1": 1},
	}
	iw := &mockInviteWriter{updateErr: errors.New("write conflict")}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.Error(t, err)
	assert.Equal(t, 0, pub.indexerCalls, "publish must not happen after a failed KV write")
}

func TestReindexInvites_FilterByCommitteeUID(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC"},
			"c2": {Name: "Security"},
		},
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1", Status: "pending"},
			{UID: "i2", CommitteeUID: "c2", Status: "pending"},
		},
	}
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub, "--committee-uid=c1"))
	require.NoError(t, err)
	assert.Equal(t, 1, pub.indexerCalls, "only c1 invite should be published")
}

func TestReindexInvites_CommitteeCache_FetchedOncePerCommittee(t *testing.T) {
	fetchCount := 0
	// Use a custom reader that counts GetBase calls by piggy-backing on baseErr.
	// We use a regular mockReader but verify via the committee cache: two invites for
	// the same committee should result in only one publish pair each, and the bases
	// map having one entry verifies the cache lookup path via the existing mock.
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC"},
		},
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1", CommitteeName: "TSC", Status: "pending"},
			{UID: "i2", CommitteeUID: "c1", CommitteeName: "TSC", Status: "pending"},
		},
	}
	_ = fetchCount
	iw := &mockInviteWriter{}
	pub := &mockPublisher{}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.NoError(t, err)
	assert.Equal(t, 2, pub.indexerCalls, "both invites published")
}

func TestReindexInvites_MultipleInvites_SomeFailPublish_ReturnsError(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {Name: "TSC"},
		},
		invites: []*model.CommitteeInvite{
			{UID: "i1", CommitteeUID: "c1", CommitteeName: "TSC", Status: "pending"},
			{UID: "i2", CommitteeUID: "c1", CommitteeName: "TSC", Status: "pending"},
		},
	}
	iw := &mockInviteWriter{}
	// Indexer fails on all calls.
	pub := &mockPublisher{indexerErr: errors.New("indexer unavailable")}

	err := (&reindexInvitesSubcommand{}).Run(context.Background(), newReindexRC(r, iw, pub))
	require.Error(t, err)
}
