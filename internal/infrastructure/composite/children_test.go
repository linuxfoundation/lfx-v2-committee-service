// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// Whatever the mode, the composite behaves as one store does for the members, the invites and the applications: the same
// suites the KV store and the PostgreSQL store pass. In dual mode every read of the suite was compared and every write
// copied, and the stores never disagreed and nothing failed.
func TestContract_Children(t *testing.T) {
	suites := map[string]func(*testing.T, storetest.Backend, storetest.CommitteeFactory){
		"members":      storetest.RunMemberContract,
		"invites":      storetest.RunInviteContract,
		"applications": storetest.RunApplicationContract,
	}
	for name, run := range suites {
		for _, mode := range []Mode{ModeKV, ModeDual, ModeDualPG, ModePostgres} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				run(t, storetest.Backend{Name: "composite-" + string(mode)}, func(t *testing.T) port.CommitteeReaderWriter {
					r := newRig(t, mode, nil, WithSampleRate(1))
					t.Cleanup(func() {
						assert.Empty(t, r.counts(t, mismatches), "no false mismatch")
						assert.Empty(t, r.counts(t, compareErr))
						assert.Empty(t, r.counts(t, failures))
					})
					return r.store
				})
			})
		}
	}
}

// child is one kind of record the composite copies, as the tests below see it.
type child struct {
	name string
	// create makes a record through the composite, in a committee that exists, and returns its uid.
	create func(t *testing.T, r *rig) string
	// update changes the record through the composite.
	update func(t *testing.T, r *rig, uid string)
	// remove deletes it through the composite; nil for a record that has no delete.
	remove func(t *testing.T, r *rig, uid string)
	// read returns what a store holds for the uid, or the error.
	read func(store port.CommitteeReaderWriter, uid string) (any, uint64, error)
	// createOp, updateOp and removeOp name what is counted when the copy fails.
	createOp, updateOp, removeOp string
}

func newCommittee(t *testing.T, r *rig) {
	t.Helper()
	require.NoError(t, r.store.Create(context.Background(), storetest.NewCommittee("uid-1", "project-1", "Committee One")))
}

func children() []child {
	ctx := context.Background()
	return []child{
		{
			name: "member",
			create: func(t *testing.T, r *rig) string {
				newCommittee(t, r)
				require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
				return "m-1"
			},
			update: func(t *testing.T, r *rig, uid string) {
				member, revision, err := r.store.GetMember(ctx, uid)
				require.NoError(t, err)
				member.JobTitle += " changed"
				_, err = r.store.UpdateMember(ctx, member, revision)
				require.NoError(t, err)
			},
			remove: func(t *testing.T, r *rig, uid string) {
				_, revision, err := r.store.GetMember(ctx, uid)
				require.NoError(t, err)
				require.NoError(t, r.store.DeleteMember(ctx, uid, revision))
			},
			read:     func(s port.CommitteeReaderWriter, uid string) (any, uint64, error) { return s.GetMember(ctx, uid) },
			createOp: "create_member", updateOp: "update_member", removeOp: "delete_member",
		},
		{
			name: "invite",
			create: func(t *testing.T, r *rig) string {
				newCommittee(t, r)
				require.NoError(t, r.store.CreateInvite(ctx, storetest.NewInvite("i-1", "uid-1", "first.last@example.com")))
				return "i-1"
			},
			update: func(t *testing.T, r *rig, uid string) {
				invite, revision, err := r.store.GetInvite(ctx, uid)
				require.NoError(t, err)
				invite.Role += " changed"
				require.NoError(t, r.store.UpdateInvite(ctx, invite, revision))
			},
			read:     func(s port.CommitteeReaderWriter, uid string) (any, uint64, error) { return s.GetInvite(ctx, uid) },
			createOp: "create_invite", updateOp: "update_invite",
		},
		{
			name: "application",
			create: func(t *testing.T, r *rig) string {
				newCommittee(t, r)
				require.NoError(t, r.store.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))
				return "a-1"
			},
			update: func(t *testing.T, r *rig, uid string) {
				application, revision, err := r.store.GetApplication(ctx, uid)
				require.NoError(t, err)
				application.Message += " changed"
				require.NoError(t, r.store.UpdateApplication(ctx, application, revision))
			},
			remove: func(t *testing.T, r *rig, uid string) {
				application, _, err := r.store.GetApplication(ctx, uid)
				require.NoError(t, err)
				require.NoError(t, r.store.DeleteApplication(ctx, application))
			},
			read:     func(s port.CommitteeReaderWriter, uid string) (any, uint64, error) { return s.GetApplication(ctx, uid) },
			createOp: "create_application", updateOp: "update_application", removeOp: "delete_application",
		},
	}
}

// sameChild fails the test unless both stores hold the record with the same revision and content.
func sameChild(t *testing.T, c child, r *rig, uid string) {
	t.Helper()
	kv, kvRevision, err := c.read(r.kv, uid)
	require.NoError(t, err)
	pg, pgRevision, err := c.read(r.pg, uid)
	require.NoError(t, err)
	assert.Equal(t, kvRevision, pgRevision, "the revision is the one KV gave it")
	kvDigest, err := Digest(kv)
	require.NoError(t, err)
	pgDigest, err := Digest(pg)
	require.NoError(t, err)
	assert.Equal(t, kvDigest, pgDigest, "the record is the same")
}

func goneFrom(c child, store port.CommitteeReaderWriter, uid string) bool {
	_, _, err := c.read(store, uid)
	return isNotFound(err)
}

func TestChildren_EachModeWritesToItsStores(t *testing.T) {
	for _, c := range children() {
		for mode, want := range map[Mode]struct{ kv, pg bool }{ModeKV: {true, false}, ModeDual: {true, true}, ModeDualPG: {true, true}, ModePostgres: {false, true}} {
			t.Run(c.name+"/"+string(mode), func(t *testing.T) {
				r := newRig(t, mode, nil)

				uid := c.create(t, r)

				assert.Equal(t, want.kv, !goneFrom(c, r.kv, uid), "KV holds the %s", c.name)
				assert.Equal(t, want.pg, !goneFrom(c, r.pg, uid), "PostgreSQL holds the %s", c.name)
			})
		}
	}
}

func TestChildren_DualWritesCarryTheKVRevisionToPostgreSQL(t *testing.T) {
	for _, c := range children() {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, ModeDual, nil)

			uid := c.create(t, r)
			sameChild(t, c, r, uid)
			_, created, err := c.read(r.pg, uid)
			require.NoError(t, err)

			c.update(t, r, uid)
			sameChild(t, c, r, uid)
			_, updated, err := c.read(r.pg, uid)
			require.NoError(t, err)
			assert.Greater(t, updated, created, "a token from before the update is stale in both stores")

			if c.remove != nil {
				c.remove(t, r, uid)
				assert.True(t, goneFrom(c, r.kv, uid) && goneFrom(c, r.pg, uid), "the %s is gone from both", c.name)
			}
			assert.Empty(t, r.counts(t, failures), "nothing failed on the way")
		})
	}
}

// failingChildPG is a PostgreSQL store whose writes of the members, the invites and the applications fail.
type failingChildPG struct{ port.CommitteeReaderWriter }

func (failingChildPG) CreateMember(context.Context, *model.CommitteeMember) error { return errPG }
func (failingChildPG) UpdateMember(context.Context, *model.CommitteeMember, uint64) (*model.CommitteeMember, error) {
	return nil, errPG
}
func (failingChildPG) DeleteMember(context.Context, string, uint64) error         { return errPG }
func (failingChildPG) CreateInvite(context.Context, *model.CommitteeInvite) error { return errPG }
func (failingChildPG) UpdateInvite(context.Context, *model.CommitteeInvite, uint64) error {
	return errPG
}
func (failingChildPG) CreateApplication(context.Context, *model.CommitteeApplication) error {
	return errPG
}
func (failingChildPG) UpdateApplication(context.Context, *model.CommitteeApplication, uint64) error {
	return errPG
}
func (failingChildPG) DeleteApplication(context.Context, *model.CommitteeApplication) error {
	return errPG
}

func TestChildren_DualASecondaryFailureIsCountedAndNeverFailsTheRequest(t *testing.T) {
	for _, c := range children() {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return failingChildPG{pg} })

			uid := c.create(t, r)
			c.update(t, r, uid)
			want := map[string]int64{c.createOp: 1, c.updateOp: 1}
			if c.remove != nil {
				c.remove(t, r, uid)
				want[c.removeOp] = 1
			}

			assert.Equal(t, want, r.counts(t, failures))
			assert.Equal(t, c.remove != nil, goneFrom(c, r.kv, uid), "KV decided every one of them, the delete included")
		})
	}
}

// flakyChildPG is a PostgreSQL store whose updates of the members, the invites and the applications fail while fail is set.
type flakyChildPG struct {
	port.CommitteeReaderWriter
	fail *atomic.Bool
}

func (f flakyChildPG) UpdateMember(ctx context.Context, m *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	if f.fail.Load() {
		return nil, errPG
	}
	return f.CommitteeReaderWriter.UpdateMember(ctx, m, revision)
}

func (f flakyChildPG) UpdateInvite(ctx context.Context, i *model.CommitteeInvite, revision uint64) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.UpdateInvite(ctx, i, revision)
}

func (f flakyChildPG) UpdateApplication(ctx context.Context, a *model.CommitteeApplication, revision uint64) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.UpdateApplication(ctx, a, revision)
}

// A copy that was missed is made up for by the next update of the same record, which writes what KV holds then.
func TestChildren_DualTheNextUpdateBringsPostgreSQLUpToWhatKVHolds(t *testing.T) {
	for _, c := range children() {
		t.Run(c.name, func(t *testing.T) {
			var fail atomic.Bool
			r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return flakyChildPG{pg, &fail} })
			uid := c.create(t, r)

			fail.Store(true)
			c.update(t, r, uid)
			assert.Equal(t, map[string]int64{c.updateOp: 1}, r.counts(t, failures), "the copy was missed")

			fail.Store(false)
			c.update(t, r, uid)

			assert.Equal(t, map[string]int64{c.updateOp: 1}, r.counts(t, failures), "nothing more failed")
			sameChild(t, c, r, uid)
		})
	}
}

// noopChildKV is a KV store whose updates of the members, the invites and the applications report success and write nothing,
// as an update does when the stored bytes already equal it after a lost swap.
type noopChildKV struct{ port.CommitteeReaderWriter }

func (noopChildKV) UpdateMember(_ context.Context, m *model.CommitteeMember, _ uint64) (*model.CommitteeMember, error) {
	return m, nil
}
func (noopChildKV) UpdateInvite(context.Context, *model.CommitteeInvite, uint64) error { return nil }
func (noopChildKV) UpdateApplication(context.Context, *model.CommitteeApplication, uint64) error {
	return nil
}

// An update that KV reports as done without writing leaves its revision where it was, so there is nothing to carry over, and
// PostgreSQL, which would refuse a revision that is not above the one it holds, is not asked.
func TestChildren_DualAnUpdateKVDidNotWriteIsNotCarriedOverAndNotCountedAsAFailure(t *testing.T) {
	for _, c := range children() {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, ModeDual, nil)
			uid := c.create(t, r)
			r.store.CommitteeReaderWriter = noopChildKV{r.kv}

			c.update(t, r, uid)

			assert.Empty(t, r.counts(t, failures))
			sameChild(t, c, r, uid)
		})
	}
}

// A read that fails in the primary store is not compared with anything.
func TestChildren_DualAReadThatFailedIsNotCompared(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil, WithSampleRate(1))

	_, err := r.store.ListMembersByEmail(ctx, "") // an empty email is a validation error
	require.Error(t, err)

	assert.Empty(t, r.counts(t, mismatches))
	assert.Empty(t, r.counts(t, compareErr))
}

// deletingChildKV is a KV store that deletes the member or the application as the copy of its creation reads it back, which
// is where a delete that finishes while a create is being copied lands.
type deletingChildKV struct {
	port.CommitteeReaderWriter
	once sync.Once
}

func (d *deletingChildKV) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	member, revision, err := d.CommitteeReaderWriter.GetMember(ctx, uid)
	d.once.Do(func() { _ = d.DeleteMember(ctx, uid, revision) })
	return member, revision, err
}

func (d *deletingChildKV) GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error) {
	application, revision, err := d.CommitteeReaderWriter.GetApplication(ctx, uid)
	d.once.Do(func() { _ = d.DeleteApplication(ctx, application) })
	return application, revision, err
}

// A delete that finishes in KV while a create is being copied finds nothing to remove in PostgreSQL, so the copy must not
// leave the record behind.
func TestChildren_DualACreateCopiedWhileADeleteFinishesDoesNotBringTheRecordBack(t *testing.T) {
	ctx := context.Background()
	for _, c := range children() {
		if c.remove == nil {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, ModeDual, nil)
			newCommittee(t, r)
			r.store.src = &deletingChildKV{CommitteeReaderWriter: r.kv} // what the copy reads from; the primary store is still the real one

			switch c.name {
			case "member":
				require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
				assert.True(t, goneFrom(c, r.kv, "m-1") && goneFrom(c, r.pg, "m-1"), "the member is in neither store")
			case "application":
				require.NoError(t, r.store.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))
				assert.True(t, goneFrom(c, r.kv, "a-1") && goneFrom(c, r.pg, "a-1"), "the application is in neither store")
			}
			assert.Empty(t, r.counts(t, failures))
		})
	}
}

// A member that PostgreSQL holds at another revision, because an earlier copy failed, is still removed by a delete.
func TestChildren_DualADeleteRemovesWhatPostgreSQLHoldsWhateverItsRevision(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	newCommittee(t, r)
	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
	_, revision, err := r.store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	behind, pgRevision, err := r.pg.GetMember(ctx, "m-1")
	require.NoError(t, err)
	behind.JobTitle = "an update PostgreSQL has and KV has not"
	wctx, err := postgres.WithRevision(ctx, pgRevision+1000)
	require.NoError(t, err)
	_, err = r.pg.UpdateMember(wctx, behind, pgRevision)
	require.NoError(t, err)

	require.NoError(t, r.store.DeleteMember(ctx, "m-1", revision))

	_, _, pgErr := r.pg.GetMember(ctx, "m-1")
	assert.True(t, isNotFound(pgErr))
	assert.Empty(t, r.counts(t, failures))

	t.Run("a member PostgreSQL does not have is not a failure", func(t *testing.T) {
		r := newRig(t, ModeDual, nil)
		require.NoError(t, r.kv.CreateMember(ctx, storetest.NewMember("m-2", "uid-1", "first.last2@example.com")), "in KV only")
		_, revision, err := r.kv.GetMember(ctx, "m-2")
		require.NoError(t, err)

		require.NoError(t, r.store.DeleteMember(ctx, "m-2", revision))

		assert.Empty(t, r.counts(t, failures))
	})
}

// A read the stores agree on counts nothing, and one they do not agree on is counted under the read.
func TestChildren_DualCompareCountsWhatDiffers(t *testing.T) {
	ctx := context.Background()

	t.Run("two stores that agree count nothing", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		newCommittee(t, r)
		require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
		require.NoError(t, r.store.CreateInvite(ctx, storetest.NewInvite("i-1", "uid-1", "first.last@example.com")))
		require.NoError(t, r.store.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))
		member := storetest.NewMember("m-1", "uid-1", "first.last@example.com")

		_, _, _ = r.store.GetMember(ctx, "m-1")
		_, _ = r.store.GetMemberRevision(ctx, "m-1")
		_, _ = r.store.ListMembersByCommittee(ctx, "uid-1")
		_, _ = r.store.ListMembersByOrganization(ctx, member.Organization.ID)
		_, _ = r.store.ListMembersByEmail(ctx, member.Email)
		_, _ = r.store.ListMembersByUsername(ctx, member.Username)
		_, _ = r.store.ListAllMembers(ctx)
		_, _, _ = r.store.GetInvite(ctx, "i-1")
		_, _ = r.store.ListInvites(ctx, "uid-1")
		_, _ = r.store.ListAllInvites(ctx)
		_, _, _ = r.store.GetApplication(ctx, "a-1")
		_, _ = r.store.ListApplications(ctx, "uid-1")
		_, _ = r.store.ListAllApplications(ctx)
		_, _, _ = r.store.GetMember(ctx, "m-missing")
		_, _ = r.store.GetMemberRevision(ctx, "m-missing")

		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("the reads of the whole set are not compared", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.kv.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")), "in KV only")
		require.NoError(t, r.kv.CreateInvite(ctx, storetest.NewInvite("i-1", "uid-1", "first.last@example.com")))
		require.NoError(t, r.kv.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))

		members, err := r.store.ListAllMembers(ctx)
		require.NoError(t, err)
		invites, err := r.store.ListAllInvites(ctx)
		require.NoError(t, err)
		applications, err := r.store.ListAllApplications(ctx)
		require.NoError(t, err)

		assert.Len(t, members, 1)
		assert.Len(t, invites, 1)
		assert.Len(t, applications, 1)
		assert.Empty(t, r.counts(t, mismatches), "a whole-set read is not made twice on a request; storage verify compares them")
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("a record only KV has is a mismatch in every read of it", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		member := storetest.NewMember("m-1", "uid-1", "first.last@example.com")
		require.NoError(t, r.kv.CreateMember(ctx, member))
		require.NoError(t, r.kv.CreateInvite(ctx, storetest.NewInvite("i-1", "uid-1", "first.last@example.com")))
		require.NoError(t, r.kv.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))

		_, _, _ = r.store.GetMember(ctx, "m-1")
		_, _ = r.store.GetMemberRevision(ctx, "m-1")
		_, _ = r.store.ListMembersByCommittee(ctx, "uid-1")
		_, _ = r.store.ListMembersByOrganization(ctx, member.Organization.ID)
		_, _ = r.store.ListMembersByEmail(ctx, member.Email)
		_, _ = r.store.ListMembersByUsername(ctx, member.Username)
		_, _ = r.store.ListAllMembers(ctx)
		_, _, _ = r.store.GetInvite(ctx, "i-1")
		_, _ = r.store.ListInvites(ctx, "uid-1")
		_, _ = r.store.ListAllInvites(ctx)
		_, _, _ = r.store.GetApplication(ctx, "a-1")
		_, _ = r.store.ListApplications(ctx, "uid-1")
		_, _ = r.store.ListAllApplications(ctx)

		assert.Equal(t, map[string]int64{
			"get_member": 1, "get_member_revision": 1, "list_members_by_committee": 1, "list_members_by_organization": 1,
			"list_members_by_email": 1, "list_members_by_username": 1,
			"get_invite": 1, "list_invites": 1,
			"get_application": 1, "list_applications": 1,
		}, r.counts(t, mismatches))
	})

	t.Run("a revision that differs alone is a mismatch in the read of the record and of its revision", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		newCommittee(t, r)
		require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
		same, pgRevision, err := r.pg.GetMember(ctx, "m-1")
		require.NoError(t, err)
		wctx, err := postgres.WithRevision(ctx, pgRevision+1000)
		require.NoError(t, err)
		_, err = r.pg.UpdateMember(wctx, same, pgRevision) // the same content under another revision
		require.NoError(t, err)

		_, _, err = r.store.GetMember(ctx, "m-1")
		require.NoError(t, err)
		_, err = r.store.GetMemberRevision(ctx, "m-1")
		require.NoError(t, err)

		assert.Equal(t, map[string]int64{"get_member": 1, "get_member_revision": 1}, r.counts(t, mismatches))
	})

	t.Run("a record that differs in content alone is a mismatch in a list", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		newCommittee(t, r)
		require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
		member, pgRevision, err := r.pg.GetMember(ctx, "m-1")
		require.NoError(t, err)
		member.JobTitle = "drifted"
		wctx, err := postgres.WithRevision(ctx, pgRevision+1000)
		require.NoError(t, err)
		_, err = r.pg.UpdateMember(wctx, member, pgRevision)
		require.NoError(t, err)

		_, err = r.store.ListMembersByCommittee(ctx, "uid-1")
		require.NoError(t, err, "the KV result is returned")

		assert.Equal(t, map[string]int64{"list_members_by_committee": 1}, r.counts(t, mismatches))
	})
}

func TestRecords_DigestsAreSortedAndByContent(t *testing.T) {
	a, b := storetest.NewMember("m-1", "uid-1", "first.last@example.com"), storetest.NewMember("m-2", "uid-1", "first.last2@example.com")

	first, err := digests([]*model.CommitteeMember{a, b})
	require.NoError(t, err)
	second, err := digests([]*model.CommitteeMember{b, a})
	require.NoError(t, err)

	assert.Equal(t, first, second, "the order of the records does not matter")
	changed := *a
	changed.JobTitle = "changed"
	third, err := digests([]*model.CommitteeMember{&changed, b})
	require.NoError(t, err)
	assert.NotEqual(t, first, third)
}

// goneKV is a KV store that answers every read of a record as not found, as it does once a delete has finished.
type goneKV struct{ port.CommitteeReaderWriter }

func (goneKV) GetMember(context.Context, string) (*model.CommitteeMember, uint64, error) {
	return nil, 0, errs.NewNotFound("member not found")
}

func (goneKV) GetInvite(context.Context, string) (*model.CommitteeInvite, uint64, error) {
	return nil, 0, errs.NewNotFound("invite not found")
}

func (goneKV) GetApplication(context.Context, string) (*model.CommitteeApplication, uint64, error) {
	return nil, 0, errs.NewNotFound("application not found")
}

func (goneKV) GetBase(context.Context, string) (*model.CommitteeBase, uint64, error) {
	return nil, 0, errs.NewNotFound("committee not found")
}

func (goneKV) GetSettings(context.Context, string) (*model.CommitteeSettings, uint64, error) {
	return nil, 0, errs.NewNotFound("committee settings not found")
}

// A record that KV no longer has when its copy is made was deleted meanwhile: there is nothing to copy, which is not a failure.
func TestChildren_DualARecordKVHasLostIsNothingToCopy(t *testing.T) {
	ctx := context.Background()

	t.Run("an update", func(t *testing.T) {
		for _, c := range children() {
			r := newRig(t, ModeDual, nil)
			uid := c.create(t, r)
			r.store.src = goneKV{r.kv}

			c.update(t, r, uid)

			assert.Empty(t, r.counts(t, failures), c.name)
		}
	})

	t.Run("a create", func(t *testing.T) {
		r := newRig(t, ModeDual, nil)
		newCommittee(t, r)
		r.store.src = goneKV{r.kv}

		require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
		require.NoError(t, r.store.CreateInvite(ctx, storetest.NewInvite("i-1", "uid-1", "first.last@example.com")))
		require.NoError(t, r.store.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))

		assert.Empty(t, r.counts(t, failures))
		for _, c := range children() {
			assert.True(t, goneFrom(c, r.pg, map[string]string{"member": "m-1", "invite": "i-1", "application": "a-1"}[c.name]), "nothing was copied: %s", c.name)
		}
	})
}

// vanishingKV is a KV store that answers a read of a member as found the first time and as not found after, as when a delete
// finishes in the middle of a copy.
type vanishingKV struct {
	port.CommitteeReaderWriter
	calls atomic.Int32
}

func (v *vanishingKV) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	if v.calls.Add(1) > 1 {
		return nil, 0, errs.NewNotFound("member not found")
	}
	return v.CommitteeReaderWriter.GetMember(ctx, uid)
}

// A member that PostgreSQL does not have is missing there, unless KV has lost it too while the copy was made.
func TestChildren_DualAMemberDeletedInBothStoresWhileItWasBeingCopiedIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	member := storetest.NewMember("m-1", "uid-1", "first.last@example.com")
	require.NoError(t, r.kv.CreateMember(ctx, member), "in KV only, so PostgreSQL has nothing to update")
	_, revision, err := r.kv.GetMember(ctx, "m-1")
	require.NoError(t, err)
	r.store.src = &vanishingKV{CommitteeReaderWriter: r.kv}

	_, err = r.store.UpdateMember(ctx, member, revision)

	require.NoError(t, err)
	assert.Empty(t, r.counts(t, failures))
}

// failingRecheckKV is a KV store whose second read of a member fails, which is the read that follows the copy of a create.
type failingRecheckKV struct {
	port.CommitteeReaderWriter
	calls atomic.Int32
}

func (f *failingRecheckKV) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	if f.calls.Add(1) == 2 {
		return nil, 0, errPG
	}
	return f.CommitteeReaderWriter.GetMember(ctx, uid)
}

// A read of KV after the copy that cannot be made is a failure to count: whether the record is still there is not known.
func TestChildren_DualAReadOfKVAfterTheCopyThatFailsIsCounted(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	newCommittee(t, r)
	r.store.src = &failingRecheckKV{CommitteeReaderWriter: r.kv}

	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))

	assert.Equal(t, map[string]int64{"create_member": 1}, r.counts(t, failures))
}

// updatingKV is a KV store that updates the member as the copy of its creation reads it back for the second time, which is
// where an update that finishes while a create is being copied lands.
type updatingKV struct {
	port.CommitteeReaderWriter
	calls atomic.Int32
}

func (u *updatingKV) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	if u.calls.Add(1) == 2 {
		if member, revision, err := u.CommitteeReaderWriter.GetMember(ctx, uid); err == nil {
			member.JobTitle = "updated meanwhile"
			_, _ = u.UpdateMember(ctx, member, revision)
		}
	}
	return u.CommitteeReaderWriter.GetMember(ctx, uid)
}

// An update that finishes in KV while a create is being copied failed to copy, for there was nothing in PostgreSQL to
// update yet, and the insert would leave the older version. The copy catches up with what KV holds.
func TestChildren_DualACreateCopiedWhileAnUpdateFinishesLeavesTheNewVersion(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	newCommittee(t, r)
	r.store.src = &updatingKV{CommitteeReaderWriter: r.kv}

	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))

	member, _, err := r.pg.GetMember(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "updated meanwhile", member.JobTitle, "PostgreSQL holds the newer version")
	sameChild(t, children()[0], r, "m-1")
	assert.Empty(t, r.counts(t, failures))
}

// markerKV is a KV store whose update of a member returns a member of its own, to tell its result from the caller's.
type markerKV struct{ port.CommitteeReaderWriter }

func (m markerKV) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	if _, err := m.CommitteeReaderWriter.UpdateMember(ctx, member, revision); err != nil {
		return nil, err
	}
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: member.UID, JobTitle: "from the primary store"}}, nil
}

func TestChildren_UpdateMemberReturnsWhatThePrimaryStoreReturns(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModeDual, ModeDualPG, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			r := newRig(t, mode, nil)
			newCommittee(t, r)
			require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
			member, revision, err := r.store.GetMember(ctx, "m-1")
			require.NoError(t, err)
			r.store.CommitteeReaderWriter = markerKV{r.store.CommitteeReaderWriter}

			updated, err := r.store.UpdateMember(ctx, member, revision)

			require.NoError(t, err)
			assert.Equal(t, "from the primary store", updated.JobTitle)
		})
	}
}

// An invite is never deleted on its own, which is why the copy of a created invite has nothing to take out again if a
// delete finishes meanwhile. If the port ever gets a delete, the copy of a create has to take the record out as the
// members' and the applications' do.
func TestChildren_TheInviteWriterHasNoDelete(t *testing.T) {
	writer := reflect.TypeFor[port.CommitteeInviteWriter]()

	names := make([]string, 0, writer.NumMethod())
	for i := range writer.NumMethod() {
		names = append(names, writer.Method(i).Name)
	}

	assert.Equal(t, []string{"CreateInvite", "UpdateInvite"}, names, "see Store.CreateInvite before adding a method")
}

// A row that a failed delete copy left in PostgreSQL can block a member that is added again with the same email, which KV
// accepts. The request succeeds, the copy is counted, and the new member is not in PostgreSQL until the stale row is gone.
func TestChildren_DualAStaleRowInPostgreSQLBlocksTheCopyOfAMemberKVAccepted(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	newCommittee(t, r)
	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
	_, revision, err := r.kv.GetMember(ctx, "m-1")
	require.NoError(t, err)
	require.NoError(t, r.kv.DeleteMember(ctx, "m-1", revision), "deleted in KV alone, as when the copy of the delete failed")

	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-2", "uid-1", "first.last@example.com")), "KV accepts it")

	assert.Equal(t, map[string]int64{"create_member": 1}, r.counts(t, failures))
	_, _, kvErr := r.kv.GetMember(ctx, "m-2")
	_, _, pgErr := r.pg.GetMember(ctx, "m-2")
	assert.NoError(t, kvErr)
	assert.True(t, isNotFound(pgErr), "it is not in PostgreSQL, whose stale row holds the email")

	member, revision, err := r.store.GetMember(ctx, "m-2")
	require.NoError(t, err)
	member.JobTitle = "changed"
	_, err = r.store.UpdateMember(ctx, member, revision)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"create_member": 1, "update_member": 1}, r.counts(t, failures), "and each later copy of it fails until the row is removed")
}

// A failed copy is logged with the uid of the record, so the row that was left behind can be found, and with nothing else of it.
func TestChildren_DualAFailedCopyIsLoggedWithTheUIDAndNoPersonalData(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return failingChildPG{pg} })
	newCommittee(t, r)

	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))

	assert.Contains(t, logs.String(), "operation=create_member")
	assert.Contains(t, logs.String(), "uid=m-1")
	assert.NotContains(t, logs.String(), "first.last@example.com")
	assert.NotContains(t, logs.String(), "First")
}
