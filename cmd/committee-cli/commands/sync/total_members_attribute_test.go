// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// mockReader implements port.CommitteeReader with just the fields syncOne touches.
type mockReader struct {
	uids       []string
	listUIDErr error

	bases    map[string]*model.CommitteeBase
	revision map[string]uint64
	baseErr  map[string]error

	members    map[string][]*model.CommitteeMember
	membersErr map[string]error

	settings    map[string]*model.CommitteeSettings
	settingsErr map[string]error

	invites            []*model.CommitteeInvite
	invitesByCommittee map[string][]*model.CommitteeInvite
	inviteRevision     map[string]uint64
	inviteGetErr       map[string]error
	getBaseCalls       int
}

func (r *mockReader) ListAllUIDs(_ context.Context) ([]string, error) {
	return r.uids, r.listUIDErr
}

func (r *mockReader) GetBase(_ context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	r.getBaseCalls++
	if err, ok := r.baseErr[uid]; ok {
		return nil, 0, err
	}
	return r.bases[uid], r.revision[uid], nil
}

func (r *mockReader) GetRevision(_ context.Context, uid string) (uint64, error) {
	return r.revision[uid], nil
}

func (r *mockReader) ListMembersByCommittee(_ context.Context, uid string) ([]*model.CommitteeMember, error) {
	if err, ok := r.membersErr[uid]; ok {
		return nil, err
	}
	return r.members[uid], nil
}

func (r *mockReader) ListMembersByOrganization(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	return nil, nil
}

// Stub methods required to satisfy port.CommitteeReader.

func (r *mockReader) GetMember(_ context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	for _, members := range r.members {
		for _, m := range members {
			if m != nil && m.UID == uid {
				return m, 1, nil
			}
		}
	}
	return nil, 0, errors.New("member not found")
}
func (r *mockReader) GetMemberRevision(_ context.Context, _ string) (uint64, error) {
	return 0, nil
}
func (r *mockReader) GetInvite(_ context.Context, uid string) (*model.CommitteeInvite, uint64, error) {
	if err, ok := r.inviteGetErr[uid]; ok {
		return nil, 0, err
	}
	rev := r.inviteRevision[uid]
	for _, inv := range r.invites {
		if inv.UID == uid {
			return inv, rev, nil
		}
	}
	return nil, rev, nil
}
func (r *mockReader) ListInvites(_ context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	if r.invitesByCommittee != nil {
		return r.invitesByCommittee[committeeUID], nil
	}
	var out []*model.CommitteeInvite
	for _, inv := range r.invites {
		if inv.CommitteeUID == committeeUID {
			out = append(out, inv)
		}
	}
	return out, nil
}
func (r *mockReader) ListAllInvites(_ context.Context) ([]*model.CommitteeInvite, error) {
	return r.invites, nil
}
func (r *mockReader) GetApplication(_ context.Context, _ string) (*model.CommitteeApplication, uint64, error) {
	return nil, 0, nil
}
func (r *mockReader) ListApplications(_ context.Context, _ string) ([]*model.CommitteeApplication, error) {
	return nil, nil
}
func (r *mockReader) GetSettings(_ context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	if err, ok := r.settingsErr[uid]; ok {
		return nil, 0, err
	}
	if r.settings != nil {
		return r.settings[uid], 0, nil
	}
	return nil, 0, nil
}

func (r *mockReader) ListAllMembers(_ context.Context) ([]*model.CommitteeMember, error) {
	var all []*model.CommitteeMember
	for _, members := range r.members {
		all = append(all, members...)
	}
	return all, nil
}

func (r *mockReader) EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	members, err := r.ListAllMembers(ctx)
	if err != nil {
		return err
	}
	for _, m := range members {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

// mockWriter implements service.CommitteeWriter, recording Update calls.
type mockWriter struct {
	updateErr   error
	updated     []*model.Committee
	updatedRevs []uint64
}

func (w *mockWriter) Update(_ context.Context, c *model.Committee, rev uint64, _ bool) (*model.Committee, error) {
	if w.updateErr != nil {
		return nil, w.updateErr
	}
	w.updated = append(w.updated, c)
	w.updatedRevs = append(w.updatedRevs, rev)
	return c, nil
}

func (w *mockWriter) Create(_ context.Context, c *model.Committee, _ bool) (*model.Committee, error) {
	return c, nil
}
func (w *mockWriter) UpdateSettings(_ context.Context, s *model.CommitteeSettings, _ uint64, _ bool) (*model.CommitteeSettings, error) {
	return s, nil
}
func (w *mockWriter) Delete(_ context.Context, _ string, _ uint64, _ bool) error { return nil }
func (w *mockWriter) CreateMember(_ context.Context, m *model.CommitteeMember, _ bool) (*model.CommitteeMember, error) {
	return m, nil
}
func (w *mockWriter) UpdateMember(_ context.Context, m *model.CommitteeMember, _ uint64, _ bool) (*model.CommitteeMember, error) {
	return m, nil
}
func (w *mockWriter) DeleteMember(_ context.Context, _ string, _ uint64, _ bool) error { return nil }
func (w *mockWriter) ReassignMember(_ context.Context, _ string, _ uint64, m *model.CommitteeMember, _ bool) (*model.CommitteeMember, error) {
	return m, nil
}

// helpers

func run(t *testing.T, r *mockReader, w *mockWriter, args ...string) error {
	t.Helper()
	s := &totalMembersAttributeSubcommand{}
	return s.Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
		Args:                        args,
	})
}

// tests

func TestMutualExclusivity(t *testing.T) {
	err := run(t, &mockReader{}, &mockWriter{}, "--committee-uid", "uid-1", "--project-uid", "proj-1")
	if err == nil {
		t.Fatal("expected error when both flags are set")
	}
}

func TestSingleUID_ResolvesWithoutListAllUIDs(t *testing.T) {
	uid := "uid-1"
	r := &mockReader{
		bases:    map[string]*model.CommitteeBase{uid: {TotalMembers: 2}},
		revision: map[string]uint64{uid: 1},
		members:  map[string][]*model.CommitteeMember{uid: {{}, {}}},
	}
	w := &mockWriter{}
	if err := run(t, r, w, "--committee-uid", uid); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// TotalMembers already correct → no write
	if len(w.updated) != 0 {
		t.Fatalf("expected no updates, got %d", len(w.updated))
	}
}

func TestProjectFilter_Skips(t *testing.T) {
	uid := "uid-1"
	r := &mockReader{
		uids:     []string{uid},
		bases:    map[string]*model.CommitteeBase{uid: {ProjectUID: "proj-other", TotalMembers: 0}},
		revision: map[string]uint64{uid: 1},
		members:  map[string][]*model.CommitteeMember{uid: {{}}},
	}
	w := &mockWriter{}
	if err := run(t, r, w, "--project-uid", "proj-want"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.updated) != 0 {
		t.Fatalf("expected no updates due to project filter, got %d", len(w.updated))
	}
}

func TestNoDrift_Skips(t *testing.T) {
	uid := "uid-1"
	r := &mockReader{
		uids:     []string{uid},
		bases:    map[string]*model.CommitteeBase{uid: {TotalMembers: 3}},
		revision: map[string]uint64{uid: 7},
		members:  map[string][]*model.CommitteeMember{uid: {{}, {}, {}}},
	}
	w := &mockWriter{}
	if err := run(t, r, w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.updated) != 0 {
		t.Fatalf("expected no updates, got %d", len(w.updated))
	}
}

func TestDrift_DryRun_NoWrite(t *testing.T) {
	uid := "uid-1"
	r := &mockReader{
		uids:     []string{uid},
		bases:    map[string]*model.CommitteeBase{uid: {TotalMembers: 1}},
		revision: map[string]uint64{uid: 5},
		members:  map[string][]*model.CommitteeMember{uid: {{}, {}, {}}},
	}
	w := &mockWriter{}
	if err := run(t, r, w, "--dry-run"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.updated) != 0 {
		t.Fatal("dry-run must not write")
	}
}

func TestDrift_WritesCorrectValue(t *testing.T) {
	uid := "uid-1"
	const rev uint64 = 42
	r := &mockReader{
		uids:     []string{uid},
		bases:    map[string]*model.CommitteeBase{uid: {TotalMembers: 1}},
		revision: map[string]uint64{uid: rev},
		members:  map[string][]*model.CommitteeMember{uid: {{}, {}, {}}},
	}
	w := &mockWriter{}
	if err := run(t, r, w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.updated) != 1 {
		t.Fatalf("expected 1 update, got %d", len(w.updated))
	}
	if w.updated[0].TotalMembers != 3 {
		t.Errorf("expected TotalMembers=3, got %d", w.updated[0].TotalMembers)
	}
	if w.updatedRevs[0] != rev {
		t.Errorf("expected revision=%d, got %d", rev, w.updatedRevs[0])
	}
}

func TestListMembersError_FailsAndContinues(t *testing.T) {
	uid1, uid2 := "uid-1", "uid-2"
	r := &mockReader{
		uids: []string{uid1, uid2},
		bases: map[string]*model.CommitteeBase{
			uid1: {TotalMembers: 0},
			uid2: {TotalMembers: 0},
		},
		revision:   map[string]uint64{uid1: 1, uid2: 2},
		membersErr: map[string]error{uid1: errors.New("kv timeout")},
		members:    map[string][]*model.CommitteeMember{uid2: {{}}},
	}
	w := &mockWriter{}
	err := run(t, r, w)
	if err == nil {
		t.Fatal("expected error due to failed committee")
	}
	// uid2 still processed and updated
	if len(w.updated) != 1 {
		t.Fatalf("expected uid2 to be updated, got %d updates", len(w.updated))
	}
}

func TestUpdateError_FailsAndContinues(t *testing.T) {
	uid1, uid2 := "uid-1", "uid-2"
	r := &mockReader{
		uids: []string{uid1, uid2},
		bases: map[string]*model.CommitteeBase{
			uid1: {TotalMembers: 0},
			uid2: {TotalMembers: 0},
		},
		revision: map[string]uint64{uid1: 1, uid2: 2},
		members: map[string][]*model.CommitteeMember{
			uid1: {{}},
			uid2: {{}},
		},
	}
	w := &conditionalFailWriter{inner: &mockWriter{}, failOn: 0}
	s := &totalMembersAttributeSubcommand{}
	err := s.Run(context.Background(), commands.RunContext{
		CommitteeReader:             r,
		CommitteeWriterOrchestrator: w,
	})
	if err == nil {
		t.Fatal("expected error due to failed update")
	}
	// uid2 should still be attempted and succeed
	if len(w.inner.updated) != 1 {
		t.Fatalf("expected uid2 to succeed, got %d updates", len(w.inner.updated))
	}
}

func TestNoFailures_ReturnsNil(t *testing.T) {
	uid := "uid-1"
	r := &mockReader{
		uids:     []string{uid},
		bases:    map[string]*model.CommitteeBase{uid: {TotalMembers: 0}},
		revision: map[string]uint64{uid: 1},
		members:  map[string][]*model.CommitteeMember{uid: {{}}},
	}
	w := &mockWriter{}
	if err := run(t, r, w); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestSomeFailures_ReturnsError(t *testing.T) {
	uid := "uid-1"
	r := &mockReader{
		uids:       []string{uid},
		bases:      map[string]*model.CommitteeBase{uid: {TotalMembers: 0}},
		revision:   map[string]uint64{uid: 1},
		membersErr: map[string]error{uid: errors.New("boom")},
	}
	w := &mockWriter{}
	if err := run(t, r, w); err == nil {
		t.Fatal("expected non-nil error when failures > 0")
	}
}

// conditionalFailWriter wraps mockWriter and returns an error on a specific call index.
type conditionalFailWriter struct {
	inner  *mockWriter
	failOn int
	calls  int
}

func (c *conditionalFailWriter) Update(ctx context.Context, committee *model.Committee, rev uint64, sync bool) (*model.Committee, error) {
	idx := c.calls
	c.calls++
	if idx == c.failOn {
		return nil, errors.New("write error")
	}
	return c.inner.Update(ctx, committee, rev, sync)
}

func (c *conditionalFailWriter) Create(ctx context.Context, committee *model.Committee, sync bool) (*model.Committee, error) {
	return c.inner.Create(ctx, committee, sync)
}
func (c *conditionalFailWriter) UpdateSettings(ctx context.Context, s *model.CommitteeSettings, rev uint64, sync bool) (*model.CommitteeSettings, error) {
	return c.inner.UpdateSettings(ctx, s, rev, sync)
}
func (c *conditionalFailWriter) Delete(ctx context.Context, uid string, rev uint64, sync bool) error {
	return c.inner.Delete(ctx, uid, rev, sync)
}
func (c *conditionalFailWriter) CreateMember(ctx context.Context, m *model.CommitteeMember, sync bool) (*model.CommitteeMember, error) {
	return c.inner.CreateMember(ctx, m, sync)
}
func (c *conditionalFailWriter) UpdateMember(ctx context.Context, m *model.CommitteeMember, rev uint64, sync bool) (*model.CommitteeMember, error) {
	return c.inner.UpdateMember(ctx, m, rev, sync)
}
func (c *conditionalFailWriter) DeleteMember(ctx context.Context, uid string, rev uint64, sync bool) error {
	return c.inner.DeleteMember(ctx, uid, rev, sync)
}
func (c *conditionalFailWriter) ReassignMember(ctx context.Context, oldUID string, oldRev uint64, m *model.CommitteeMember, sync bool) (*model.CommitteeMember, error) {
	return c.inner.ReassignMember(ctx, oldUID, oldRev, m, sync)
}
