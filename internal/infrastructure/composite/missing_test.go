// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// An update copies what the primary store holds to the secondary store. When the secondary store lacks the record, because the
// copy of its create failed, the update creates it, the way the copy of a create does: it reads the record from the primary
// store again afterwards, so a record deleted meanwhile is not brought back.

func TestCopyUpdated_ARecordTheSecondaryStoreLacksIsCreatedNotUpdated(t *testing.T) {
	c := newCore(ModeDual, nil)
	created, updated := 0, 0
	readSrc := func(context.Context) (string, uint64, error) { return "B", 2, nil }
	readDst := func(context.Context) (string, uint64, error) { return "", 0, errs.NewNotFound("not found") }
	updateDst := func(context.Context, string, uint64) error { updated++; return nil }

	err := copyUpdated(context.Background(), &c, readSrc, readDst, updateDst, func(context.Context) error { created++; return nil })

	require.NoError(t, err)
	assert.Equal(t, 1, created)
	assert.Zero(t, updated)
}

func TestCopyUpdated_ARecordTheSecondaryStoreLacksIsAFailureWhereItCannotBeCreated(t *testing.T) {
	c := newCore(ModeDual, nil)
	readSrc := func(context.Context) (string, uint64, error) { return "B", 2, nil }
	readDst := func(context.Context) (string, uint64, error) { return "", 0, errs.NewNotFound("not found") }

	err := copyUpdated(context.Background(), &c, readSrc, readDst, func(context.Context, string, uint64) error { return nil }, nil)

	require.Error(t, err)
	assert.True(t, isNotFound(err))
}

func TestCopyUpdated_ARecordBothStoresLackIsNothingToCopy(t *testing.T) {
	c := newCore(ModeDual, nil)
	calls := 0
	readSrc := func(context.Context) (string, uint64, error) {
		calls++
		if calls == 1 {
			return "B", 2, nil
		}
		return "", 0, errs.NewNotFound("not found") // deleted meanwhile
	}
	readDst := func(context.Context) (string, uint64, error) { return "", 0, errs.NewNotFound("not found") }
	created := 0

	err := copyUpdated(context.Background(), &c, readSrc, readDst, func(context.Context, string, uint64) error { return nil },
		func(context.Context) error { created++; return nil })

	require.NoError(t, err)
	assert.Zero(t, created, "a record the primary store lost is not created")
}

// The create the update makes does not make another if the record is missing once more: a record that keeps going missing
// is a failure, not a loop.
func TestCopyUpdated_TheCreateIsMadeAtMostOncePerCopy(t *testing.T) {
	c := newCore(ModeDual, nil)
	created := 0
	readSrc := func(context.Context) (string, uint64, error) { return "B", 2, nil }
	readDst := func(context.Context) (string, uint64, error) { return "", 0, errs.NewNotFound("not found") }
	var create func(context.Context) error
	create = func(ctx context.Context) error {
		created++
		// What the copy of a create does when the record has moved on: it catches up with an update, which finds the record
		// missing again.
		return copyUpdated(ctx, &c, readSrc, readDst, func(context.Context, string, uint64) error { return nil }, create)
	}

	err := copyUpdated(context.Background(), &c, readSrc, readDst, func(context.Context, string, uint64) error { return nil }, create)

	require.Error(t, err)
	assert.Equal(t, 1, created)
}

// flakyCreatePG is a store whose creates fail while fail is set, as a copy does that PostgreSQL did not take.
type flakyCreatePG struct {
	port.CommitteeReaderWriter
	fail *atomic.Bool
}

func (f flakyCreatePG) CreateMember(ctx context.Context, m *model.CommitteeMember) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.CreateMember(ctx, m)
}

func (f flakyCreatePG) CreateInvite(ctx context.Context, i *model.CommitteeInvite) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.CreateInvite(ctx, i)
}

func (f flakyCreatePG) CreateApplication(ctx context.Context, a *model.CommitteeApplication) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.CreateApplication(ctx, a)
}

func TestChildren_AnUpdateCreatesTheRecordWhoseCreateWasNotCopied(t *testing.T) {
	for _, c := range children() {
		t.Run(c.name, func(t *testing.T) {
			var fail atomic.Bool
			r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return flakyCreatePG{pg, &fail} })
			fail.Store(true)
			uid := c.create(t, r)
			require.True(t, goneFrom(c, r.pg, uid), "the copy of the create was missed")

			fail.Store(false)
			c.update(t, r, uid)

			sameChild(t, c, r, uid)
			assert.Equal(t, map[string]int64{c.createOp: 1}, r.counts(t, failures), "only the missed create was counted")
		})
	}
}

// vanishingAfterKV answers a read of a member as not found once it has been read the given number of times, as when a delete
// finishes in the middle of the copy.
type vanishingAfterKV struct {
	port.CommitteeReaderWriter
	reads atomic.Int32
	after int32
}

func (v *vanishingAfterKV) GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error) {
	if v.reads.Add(1) > v.after {
		return nil, 0, errs.NewNotFound("member not found")
	}
	return v.CommitteeReaderWriter.GetMember(ctx, uid)
}

// A record that is deleted in KV while an update creates its missing copy is not left in PostgreSQL, where the update would
// have brought it back.
func TestChildren_AnUpdateThatCreatesARecordDeletedMeanwhileDoesNotBringItBack(t *testing.T) {
	ctx := context.Background()
	var fail atomic.Bool
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return flakyCreatePG{pg, &fail} })
	newCommittee(t, r)
	fail.Store(true)
	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
	fail.Store(false)
	member, revision, err := r.store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	member.JobTitle += " changed"
	// The reads of the copy: the record, the check that KV still has it, the record again for the create, and the read after
	// the insert, which is the one that finds it gone.
	r.store.src = &vanishingAfterKV{CommitteeReaderWriter: r.kv, after: 3}

	_, err = r.store.UpdateMember(ctx, member, revision)

	require.NoError(t, err)
	_, _, pgErr := r.pg.GetMember(ctx, "m-1")
	assert.True(t, isNotFound(pgErr), "the deleted member was taken out again")
	assert.Equal(t, map[string]int64{"create_member": 1}, r.counts(t, failures), "only the missed create was counted")
}

func TestStore_DualAnUpdateCreatesTheCommitteeThatWasNeverCopied(t *testing.T) {
	ctx := context.Background()
	for name, update := range map[string]func(r *rig, base *model.CommitteeBase, settings *model.CommitteeSettings, revision uint64) error{
		"the base": func(r *rig, base *model.CommitteeBase, _ *model.CommitteeSettings, revision uint64) error {
			return r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision)
		},
		"the settings": func(r *rig, _ *model.CommitteeBase, settings *model.CommitteeSettings, _ uint64) error {
			_, revision, err := r.store.GetSettings(ctx, settings.UID)
			require.NoError(t, err)
			return r.store.UpdateSetting(ctx, settings, revision)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, ModeDual, nil)
			require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")), "in KV only: never copied")
			base, revision, err := r.store.GetBase(ctx, "uid-1")
			require.NoError(t, err)
			settings, _, err := r.store.GetSettings(ctx, "uid-1")
			require.NoError(t, err)
			base.Description = "changed"
			settings.BusinessEmailRequired = !settings.BusinessEmailRequired

			require.NoError(t, update(r, base, settings, revision))

			assert.Empty(t, r.counts(t, failures))
			sameRecord(t, r, "uid-1")
			_, _, settingsErr := r.pg.GetSettings(ctx, "uid-1")
			assert.NoError(t, settingsErr, "the settings came with the committee")
		})
	}
}

func TestStore_DualPGAnUpdateCreatesTheCommitteeKVNeverGot(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")), "in PostgreSQL only: never copied")
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "changed"

	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	assert.Empty(t, r.counts(t, failures))
	contentInBoth(t, r, "uid-1")
}

// goneAfterCommitteeKV answers a read of the committee base as not found once it has been read the given number of times.
type goneAfterCommitteeKV struct {
	port.CommitteeReaderWriter
	reads atomic.Int32
	after int32
}

func (g *goneAfterCommitteeKV) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	if g.reads.Add(1) > g.after {
		return nil, 0, errs.NewNotFound("committee not found")
	}
	return g.CommitteeReaderWriter.GetBase(ctx, uid)
}

func TestStore_DualAnUpdateThatCreatesACommitteeDeletedMeanwhileDoesNotBringItBack(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")), "in KV only: never copied")
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "changed"
	// The reads of the copy: the committee, the check that KV still has it, the committee again for the create, and the read
	// after the insert, which is the one that finds it gone.
	r.store.src = &goneAfterCommitteeKV{CommitteeReaderWriter: r.kv, after: 3}

	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	_, _, pgErr := r.pg.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(pgErr), "the deleted committee was taken out again")
	assert.Empty(t, r.counts(t, failures))
}

func TestLinks_DualAnUpdateCreatesTheLinkOrFolderThatWasNeverCopied(t *testing.T) {
	ctx := context.Background()
	t.Run("link", func(t *testing.T) {
		r := newLinkRig(t, ModeDual, nil)
		require.NoError(t, r.kv.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")), "in KV only")
		link, revision, err := r.kv.GetLink(ctx, "uid-1", "l-1")
		require.NoError(t, err)
		link.Name += " changed"

		require.NoError(t, r.links.UpdateLink(ctx, link, revision))

		assert.Empty(t, r.counts(t, failures))
		for _, k := range linkKinds() {
			if k.name == "link" {
				sameLinkRecord(t, k, r, "l-1")
			}
		}
	})
	t.Run("folder", func(t *testing.T) {
		r := newLinkRig(t, ModeDual, nil)
		require.NoError(t, r.kv.CreateLinkFolder(ctx, storetest.NewFolder("f-1", "uid-1", "Folder")), "in KV only")
		folder, revision, err := r.kv.GetLinkFolder(ctx, "uid-1", "f-1")
		require.NoError(t, err)
		folder.Name += " changed"

		require.NoError(t, r.links.UpdateLinkFolder(ctx, folder, revision))

		assert.Empty(t, r.counts(t, failures))
		for _, k := range linkKinds() {
			if k.name == "folder" {
				sameLinkRecord(t, k, r, "f-1")
			}
		}
	})
}

func TestDocuments_DualAnUpdateCreatesTheMetadataThatWasNeverCopied(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDual, nil)
	require.NoError(t, r.kv.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")), "in KV only")
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)
	doc.Description = "changed"

	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))

	sameDocumentInBoth(t, r, "d-1")
	assert.Empty(t, r.counts(t, failures))
}
