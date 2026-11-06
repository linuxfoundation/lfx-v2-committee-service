// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// In dual-pg mode PostgreSQL decides and KV follows. KV assigns revisions of its own and cannot be given the one
// PostgreSQL gave, so what the copy guarantees is the content, not the revision: the tests below look at the content, at
// what guards the write to KV, and at what is counted when the copy to KV fails.

func TestCore_TheSecondaryStoreOfEachMode(t *testing.T) {
	for mode, want := range map[Mode]struct {
		name       string
		postgres   bool
		hasSecond  bool
		carryEqual bool
	}{
		ModeKV:       {"postgres", false, false, true},
		ModeDual:     {"postgres", true, true, false},
		ModeDualPG:   {"kv", false, true, true},
		ModePostgres: {"postgres", false, false, true},
	} {
		t.Run(string(mode), func(t *testing.T) {
			c := newCore(mode, nil)

			assert.Equal(t, want.name, c.secondaryName())
			assert.Equal(t, want.postgres, c.secondaryIsPostgres())
			assert.Equal(t, want.hasSecond, mode.HasSecondary())
			carried, err := c.carry(context.Background(), 7)
			require.NoError(t, err)
			assert.Equal(t, want.carryEqual, carried == context.Background(), "only dual mode gives PostgreSQL the revision")
			assert.True(t, c.sameRevision(7, 7))
			assert.Equal(t, !c.secondaryIsPostgres(), c.sameRevision(7, 8), "revisions are compared only where they are carried over")
		})
	}
}

func contentInBoth(t *testing.T, r *rig, uid string) {
	t.Helper()
	ctx := context.Background()
	kv, _, err := r.kv.GetBase(ctx, uid)
	require.NoError(t, err, "KV holds the committee")
	pg, _, err := r.pg.GetBase(ctx, uid)
	require.NoError(t, err)
	same, err := sameContent(kv, pg)
	require.NoError(t, err)
	assert.True(t, same, "the committee is the same in both stores")
}

func TestDualPG_PostgreSQLDecidesAndKVFollows(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil, WithSampleRate(1))

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	contentInBoth(t, r, "uid-1")
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	_, pgRevision, err := r.pg.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.Equal(t, pgRevision, revision, "the token a client holds is PostgreSQL's")

	base.Description = "changed"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	contentInBoth(t, r, "uid-1")
	_, revision, err = r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)

	require.NoError(t, r.store.Delete(ctx, "uid-1", revision))
	_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
	_, _, pgErr := r.pg.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(kvErr) && isNotFound(pgErr), "the delete reached both stores")
	assert.Empty(t, r.counts(t, failures))
	assert.Empty(t, r.counts(t, mismatches), "the stores agree in content, whatever their revisions")
	assert.Empty(t, r.counts(t, compareErr))
}

func TestDualPG_AFailedCopyToKVIsCountedAndNeverFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	r.store.dst = failingPG{r.kv}

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err, "PostgreSQL decided")
	base.Description = "changed"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	assert.Equal(t, map[string]int64{"create_committee": 1, "update_committee": 1}, r.counts(t, failures))
	_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(kvErr), "KV never got it")
}

// A copy that was missed is made up for by the next update of the same committee, which copies what PostgreSQL holds then.
func TestDualPG_TheNextUpdateBringsKVUpToWhatPostgreSQLHolds(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	r.store.dst = failingPG{r.kv}
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "missed by KV"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	r.store.dst = r.kv

	base, revision, err = r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "newer"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	contentInBoth(t, r, "uid-1")
	assert.Equal(t, map[string]int64{"update_committee": 1}, r.counts(t, failures), "only the missed copy")
}

// KV has revisions of its own, so a delete is guarded by the one KV has, not by the token the client sent.
func TestDualPG_ADeleteRemovesWhatKVHoldsWhateverItsRevision(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	behind, kvRevision, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	behind.Description = "an update only KV has"
	require.NoError(t, r.kv.UpdateBase(ctx, &model.Committee{CommitteeBase: *behind}, kvRevision))
	_, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)

	require.NoError(t, r.store.Delete(ctx, "uid-1", revision))

	_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(kvErr), "removed from KV whatever its revision")
	assert.Empty(t, r.counts(t, failures))
}

func TestDualPG_ADeleteOfACommitteeKVNeverHadIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One"))) // never copied
	_, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)

	require.NoError(t, r.store.Delete(ctx, "uid-1", revision))

	assert.Empty(t, r.counts(t, failures), "an absent record is where a delete leaves it")
}

// A copy of what KV already holds writes nothing, since KV would give the record a new revision for the same content.
func TestDualPG_ACopyOfUnchangedContentWritesNothing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	_, before, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)

	require.NoError(t, r.store.replicateBase(ctx, "uid-1"))

	_, after, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.Equal(t, before, after, "KV was not written")
}

func TestDualPG_CompareLooksAtContentNotRevisions(t *testing.T) {
	ctx := context.Background()

	t.Run("the same content at other revisions is no mismatch", func(t *testing.T) {
		r := newRig(t, ModeDualPG, nil, WithSampleRate(1))
		require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-0", "project-1", "Shifts the revisions of KV")))
		require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
		require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
		_, kvRevision, _ := r.kv.GetBase(ctx, "uid-1")
		_, pgRevision, _ := r.pg.GetBase(ctx, "uid-1")
		require.NotEqual(t, kvRevision, pgRevision, "the stores hold the committee at other revisions")

		_, _, _ = r.store.GetBase(ctx, "uid-1")

		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("other content is a mismatch", func(t *testing.T) {
		r := newRig(t, ModeDualPG, nil, WithSampleRate(1))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
		base, revision, err := r.kv.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		base.Description = "changed only in KV"
		require.NoError(t, r.kv.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

		_, _, _ = r.store.GetBase(ctx, "uid-1")

		assert.Equal(t, map[string]int64{"get_committee": 1}, r.counts(t, mismatches))
	})
}

func TestDualPG_MembersFollowInKV(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil, WithSampleRate(1))
	newCommittee(t, r)

	require.NoError(t, r.store.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
	member, revision, err := r.store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	member.JobTitle = "changed"
	_, err = r.store.UpdateMember(ctx, member, revision)
	require.NoError(t, err)

	inKV, _, err := r.kv.GetMember(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "changed", inKV.JobTitle, "KV follows the update")

	// KV moves on by itself; the delete is still guarded by the revision KV has.
	inKV.JobTitle = "only in KV"
	_, err = r.kv.UpdateMember(ctx, inKV, mustRevisionOf(t, r, "m-1"))
	require.NoError(t, err)
	_, revision, err = r.store.GetMember(ctx, "m-1")
	require.NoError(t, err)
	require.NoError(t, r.store.DeleteMember(ctx, "m-1", revision))
	_, _, kvErr := r.kv.GetMember(ctx, "m-1")
	assert.True(t, isNotFound(kvErr), "removed from KV")
	assert.Empty(t, r.counts(t, failures))
	assert.Equal(t, map[string]int64{"get_member": 1}, r.counts(t, mismatches), "only the read after KV changed by itself")
}

func mustRevisionOf(t *testing.T, r *rig, uid string) uint64 {
	t.Helper()
	_, revision, err := r.kv.GetMember(context.Background(), uid)
	require.NoError(t, err)
	return revision
}

func TestDualPG_LinksFollowInKVAndAFailedCopyIsCounted(t *testing.T) {
	ctx := context.Background()
	r := newLinkRig(t, ModeDualPG, nil, WithSampleRate(1))
	require.NoError(t, r.links.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
	link, revision, err := r.links.GetLink(ctx, "uid-1", "l-1")
	require.NoError(t, err)
	link.Name += " changed"
	require.NoError(t, r.links.UpdateLink(ctx, link, revision))

	inKV, _, err := r.kv.GetLink(ctx, "uid-1", "l-1")
	require.NoError(t, err)
	assert.Equal(t, link.Name, inKV.Name, "KV follows")
	assert.Empty(t, r.counts(t, failures))
	assert.Empty(t, r.counts(t, mismatches))

	r.links.dst = failingLinkPG{r.kv}
	_, revision, err = r.links.GetLink(ctx, "uid-1", "l-1")
	require.NoError(t, err)
	require.NoError(t, r.links.DeleteLink(ctx, "uid-1", "l-1", revision), "PostgreSQL decided")
	assert.Equal(t, map[string]int64{"delete_link": 1}, r.counts(t, failures), "the failed copy to KV is counted")
}

func TestDualPG_BriefsAndThrottlesFollowInKVWithTheirOwnRevisions(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDualPG, nil, WithSampleRate(1))

	brief, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	brief.BriefText = "changed"
	_, err = r.briefs.PutGroupWeeklyBrief(ctx, brief) // with the revision PostgreSQL gave it: a swap there
	require.NoError(t, err)
	throttle, err := r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	throttle.GeneratesUsed++
	_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, throttle)
	require.NoError(t, err)

	inKV, _, err := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	require.NotNil(t, inKV, "KV holds the brief")
	assert.Equal(t, "changed", inKV.BriefText)
	throttleInKV, err := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	require.NotNil(t, throttleInKV)
	assert.Equal(t, throttle.GeneratesUsed, throttleInKV.GeneratesUsed)
	assert.Empty(t, r.counts(t, failures))

	// Reads are compared by content, without the update time KV stamps itself and without the revisions.
	_, _, _ = r.briefs.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	_, _ = r.briefs.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	assert.Empty(t, r.counts(t, mismatches))
	assert.Empty(t, r.counts(t, compareErr))
}

// A copy of the content KV already holds writes nothing, though KV would stamp a new update time on the brief.
func TestDualPG_ACopyOfAnUnchangedBriefOrThrottleWritesNothing(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDualPG, nil)
	_, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	_, _, _ = r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	briefBefore, _, err := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	throttleBefore, err := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)

	require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow))
	require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))

	briefAfter, _, err := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	throttleAfter, err := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	assert.Equal(t, briefBefore.Revision, briefAfter.Revision, "the brief in KV was not written")
	assert.Equal(t, throttleBefore.Revision, throttleAfter.Revision, "the throttle entry in KV was not written")
}

func TestDualPG_ADocumentThatMovesToAnotherCommitteeIsCopiedAndItsFileGoesWithIt(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDualPG, nil, WithSampleRate(1))
	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	require.NoError(t, r.docs.PutDocumentFile(ctx, "d-1", []byte("file")))
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)

	doc.CommitteeUID = "uid-2"
	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))

	inKV, _, err := r.kv.GetDocumentMetadata(ctx, "uid-2", "d-1")
	require.NoError(t, err, "KV holds the document under the new committee")
	assert.Equal(t, "uid-2", inKV.CommitteeUID)
	assert.Empty(t, r.counts(t, failures))

	_, revision, err = r.docs.GetDocumentMetadata(ctx, "uid-2", "d-1")
	require.NoError(t, err)
	require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-2", "d-1", revision))
	_, _, kvErr := r.kv.GetDocumentMetadata(ctx, "uid-2", "d-1")
	assert.True(t, isNotFound(kvErr), "the metadata is gone from KV")
	_, fileErr := r.kv.GetDocumentFile(ctx, "d-1")
	assert.True(t, isNotFound(fileErr), "the file went with it")
	assert.Empty(t, r.counts(t, failures))
	assert.Empty(t, r.counts(t, mismatches))
}

// The file is removed by the composite itself when PostgreSQL decided the delete and the copy of it to KV, which would
// remove the file as well, failed.
func TestDualPG_TheFileIsRemovedEvenWhenTheCopyOfTheDeleteToKVFails(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDualPG, nil)
	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	require.NoError(t, r.docs.PutDocumentFile(ctx, "d-1", []byte("file")))
	r.docs.dst = failingDocumentPG{r.kv}
	_, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)

	require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision))

	_, fileErr := r.kv.GetDocumentFile(ctx, "d-1")
	assert.True(t, isNotFound(fileErr), "the file is gone")
	assert.Equal(t, map[string]int64{"delete_document": 1}, r.counts(t, failures), "the failed copy is counted")
}

// afterFirstRead runs a side effect once, right after the copy has read from the primary store, which is the moment another
// update and its copy can overtake it.
type afterFirstReadBaseStore struct {
	port.CommitteeReaderWriter
	after func()
	fired bool
}

func (h *afterFirstReadBaseStore) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	base, revision, err := h.CommitteeReaderWriter.GetBase(ctx, uid)
	if !h.fired {
		h.fired = true
		h.after()
	}
	return base, revision, err
}

// A copy that has read the primary store and is overtaken by a newer update and its copy must not write the older record
// over the newer one in KV, which refuses nothing but a stale guard.
func TestDualPG_ASlowCopyNeverLeavesAnOlderRecordOverANewerOne(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.pg.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "v1"
	require.NoError(t, r.pg.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision)) // PostgreSQL only: KV is behind
	r.store.src = &afterFirstReadBaseStore{CommitteeReaderWriter: r.pg, after: func() {
		newer, rev, err := r.pg.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		newer.Description = "v2"
		require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *newer}, rev)) // the newer update and its copy
	}}

	require.NoError(t, r.store.replicateBase(ctx, "uid-1"))

	contentInBoth(t, r, "uid-1")
	inKV, _, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.Equal(t, "v2", inKV.Description, "KV holds the newer record")
	assert.Empty(t, r.counts(t, failures))
}

type afterFirstReadBriefStore struct {
	BriefBackend
	after func()
	fired bool
}

func (h *afterFirstReadBriefStore) GetGroupWeeklyBriefForWindow(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	brief, throttle, err := h.BriefBackend.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
	if !h.fired {
		h.fired = true
		h.after()
	}
	return brief, throttle, err
}

// The first copy of a brief to KV is a put without a guard, which an overlapping copy of an older brief could overwrite: the
// copy looks again and makes the newer one the one KV holds.
func TestDualPG_ASlowFirstCopyOfABriefNeverLeavesAnOlderBriefInKV(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDualPG, nil)
	v1 := storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow)
	v1.BriefText = "v1"
	_, err := r.pg.PutGroupWeeklyBrief(ctx, v1) // PostgreSQL only: KV has no brief for the window
	require.NoError(t, err)
	r.briefs.src = &afterFirstReadBriefStore{BriefBackend: r.pg, after: func() {
		v2 := storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow)
		v2.BriefText = "v2"
		_, err := r.briefs.PutGroupWeeklyBrief(ctx, v2) // the newer put and its copy
		require.NoError(t, err)
	}}

	require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow))

	inKV, _, err := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	require.NotNil(t, inKV)
	assert.Equal(t, "v2", inKV.BriefText, "KV holds the newer brief")
}

type afterFirstReadThrottleStore struct {
	BriefBackend
	after func()
	fired bool
}

func (h *afterFirstReadThrottleStore) GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	throttle, err := h.BriefBackend.GetGroupWeeklyBriefThrottle(ctx, committeeUID, windowStart)
	if !h.fired {
		h.fired = true
		h.after()
	}
	return throttle, err
}

func TestDualPG_ASlowFirstCopyOfAThrottleNeverLeavesAnOlderEntryInKV(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDualPG, nil)
	v1 := storetest.NewThrottle("uid-1", storetest.FixtureWindow)
	v1.GeneratesUsed = 1
	_, err := r.pg.PutGroupWeeklyBriefThrottle(ctx, v1) // PostgreSQL only
	require.NoError(t, err)
	r.briefs.src = &afterFirstReadThrottleStore{BriefBackend: r.pg, after: func() {
		current, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
		require.NoError(t, err)
		current.GeneratesUsed = 2
		_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, current) // the newer put and its copy
		require.NoError(t, err)
	}}

	require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))

	inKV, err := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	require.NotNil(t, inKV)
	assert.Equal(t, 2, inKV.GeneratesUsed, "KV holds the newer entry")
}

// afterFirstDocumentRead runs a side effect once, right after the first read of a document from the primary store, which is
// the moment a move or a delete can overtake the copy of a create.
type afterFirstDocumentRead struct {
	DocumentMetadataBackend
	after func()
	fired bool
}

func (h *afterFirstDocumentRead) GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error) {
	doc, revision, err := h.DocumentMetadataBackend.GetDocumentMetadata(ctx, committeeUID, documentUID)
	if !h.fired {
		h.fired = true
		h.after()
	}
	return doc, revision, err
}

// The copy of a create that finds the document gone from its committee meanwhile cannot tell a delete from a move, and a delete
// in KV removes the file as well: a document the primary store still has is not deleted, and what it is now is copied.
func TestDualPG_ACreateCopyThatFindsTheDocumentMovedCopiesTheMoveAndKeepsTheFile(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDualPG, nil)
	require.NoError(t, r.docs.PutDocumentFile(ctx, "d-1", []byte("file")))
	r.docs.src = &afterFirstDocumentRead{DocumentMetadataBackend: r.pg, after: func() {
		doc, revision, err := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
		require.NoError(t, err)
		doc.CommitteeUID = "uid-2"
		require.NoError(t, r.pg.UpdateDocumentMetadata(ctx, doc, revision)) // the move, before the copy has written KV
	}}

	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))

	moved, _, err := r.kv.GetDocumentMetadata(ctx, "uid-2", "d-1")
	require.NoError(t, err, "KV holds the document under the committee it was moved to")
	assert.Equal(t, "uid-2", moved.CommitteeUID)
	_, _, oldErr := r.kv.GetDocumentMetadata(ctx, "uid-1", "d-1")
	assert.True(t, isNotFound(oldErr), "and no longer under the old one")
	file, err := r.kv.GetDocumentFile(ctx, "d-1")
	require.NoError(t, err, "its file is still there")
	assert.Equal(t, []byte("file"), file)
	assert.Empty(t, r.counts(t, failures))
}

func TestDualPG_ADocumentThatWasReallyDeletedMeanwhileIsTakenOutOfKV(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDualPG, nil)
	r.docs.src = &afterFirstDocumentRead{DocumentMetadataBackend: r.pg, after: func() {
		_, revision, err := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
		require.NoError(t, err)
		require.NoError(t, r.pg.DeleteDocumentMetadata(ctx, "uid-1", "d-1", revision)) // deleted before the copy has written KV
	}}

	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))

	_, _, err := r.kv.GetDocumentMetadata(ctx, "uid-1", "d-1")
	assert.True(t, isNotFound(err), "the delete reached KV")
	assert.Empty(t, r.counts(t, failures))
}

// A move that PostgreSQL decided and KV did not get leaves the document in KV under its old committee; a delete under the new
// committee still removes it.
func TestDualPG_ADeleteAfterAMoveThatWasNotCopiedRemovesTheDocumentFromKV(t *testing.T) {
	ctx := context.Background()
	r := newDocumentRig(t, ModeDualPG, nil)
	require.NoError(t, r.docs.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	doc, revision, err := r.docs.GetDocumentMetadata(ctx, "uid-1", "d-1")
	require.NoError(t, err)
	r.docs.dst = failingDocumentPG{r.kv}
	doc.CommitteeUID = "uid-2"
	require.NoError(t, r.docs.UpdateDocumentMetadata(ctx, doc, revision))
	r.docs.dst = r.kv
	_, revision, err = r.docs.GetDocumentMetadata(ctx, "uid-2", "d-1")
	require.NoError(t, err)

	require.NoError(t, r.docs.DeleteDocumentMetadata(ctx, "uid-2", "d-1", revision))

	_, _, oldErr := r.kv.GetDocumentMetadata(ctx, "uid-1", "d-1")
	assert.True(t, isNotFound(oldErr), "the metadata under the old committee is gone from KV")
	assert.Equal(t, map[string]int64{"update_document": 1}, r.counts(t, failures), "only the move that was not copied")
}

// A record that went from A to B and back to A while a copy of B was delayed: the copy that found KV at A and the primary at A
// wrote nothing, and the delayed copy of B still holds a valid guard. The copy looks at the primary store again after it
// writes, and puts A back.
func TestDualPG_ADelayedCopyOfAnOlderStateIsCorrectedWhenTheRecordWentBackToWhatKVHeld(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDualPG, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.pg.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	original := base.Description
	base.Description = "B"
	require.NoError(t, r.pg.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision)) // PostgreSQL moved to B
	r.store.src = &afterFirstReadBaseStore{CommitteeReaderWriter: r.pg, after: func() {
		back, rev, err := r.pg.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		back.Description = original
		require.NoError(t, r.pg.UpdateBase(ctx, &model.Committee{CommitteeBase: *back}, rev)) // and back to what KV holds
		require.NoError(t, r.store.replicateBase(ctx, "uid-1"))                               // its copy finds nothing to write
	}}

	require.NoError(t, r.store.replicateBase(ctx, "uid-1"))

	contentInBoth(t, r, "uid-1")
	inKV, _, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.Equal(t, original, inKV.Description, "KV holds what PostgreSQL holds again")
	assert.Empty(t, r.counts(t, failures))
}

func TestDualPG_ADelayedCopyOfAnOlderBriefIsCorrectedWhenTheBriefWentBack(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDualPG, nil)
	a := storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow)
	a.BriefText = "A"
	_, err := r.briefs.PutGroupWeeklyBrief(ctx, a) // both stores hold A
	require.NoError(t, err)
	b := storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow)
	b.BriefText = "B"
	_, err = r.pg.PutGroupWeeklyBrief(ctx, b) // PostgreSQL moved to B
	require.NoError(t, err)
	r.briefs.src = &afterFirstReadBriefStore{BriefBackend: r.pg, after: func() {
		back := storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow)
		back.BriefText = "A"
		_, err := r.pg.PutGroupWeeklyBrief(ctx, back) // and back to A
		require.NoError(t, err)
		require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow)) // its copy finds nothing to write
	}}

	require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow))

	inKV, _, err := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	require.NotNil(t, inKV)
	assert.Equal(t, "A", inKV.BriefText, "KV holds what PostgreSQL holds again")
}

func TestDualPG_ADelayedCopyOfAnOlderThrottleEntryIsCorrectedWhenTheEntryWentBack(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDualPG, nil)
	a := storetest.NewThrottle("uid-1", storetest.FixtureWindow)
	a.GeneratesUsed = 1
	_, err := r.briefs.PutGroupWeeklyBriefThrottle(ctx, a) // both stores hold A
	require.NoError(t, err)
	current, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	current.GeneratesUsed = 2
	_, err = r.pg.PutGroupWeeklyBriefThrottle(ctx, current) // PostgreSQL moved to B
	require.NoError(t, err)
	r.briefs.src = &afterFirstReadThrottleStore{BriefBackend: r.pg, after: func() {
		back, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
		require.NoError(t, err)
		back.GeneratesUsed = 1
		_, err = r.pg.PutGroupWeeklyBriefThrottle(ctx, back) // and back to A
		require.NoError(t, err)
		require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow)) // its copy finds nothing to write
	}}

	require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))

	inKV, err := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	require.NotNil(t, inKV)
	assert.Equal(t, 1, inKV.GeneratesUsed, "KV holds what PostgreSQL holds again")
}

// A copy that is overtaken again after it was made once more has not brought KV up to the primary store, and is a failure
// that gets counted, not a success.
func TestCopyUpdated_AnOvertakenCopyThatStaysBehindIsAFailure(t *testing.T) {
	c := newCore(ModeDualPG, nil)
	reads, writes := 0, 0
	readSrc := func(context.Context) (string, uint64, error) {
		reads++
		if reads%2 == 1 {
			return "B", 2, nil // what the copy writes
		}
		return "A", 3, nil // and what the primary store holds when it looks again: it keeps moving
	}
	readDst := func(context.Context) (string, uint64, error) { return "older", 1, nil }
	updateDst := func(context.Context, string, uint64) error { writes++; return nil }

	err := copyUpdated(context.Background(), &c, readSrc, readDst, updateDst)

	require.Error(t, err)
	assert.Equal(t, 2, writes, "it was made once more, and no more than that")
}

func TestCopyUpdated_ACopyThatIsNotOvertakenIsMadeOnce(t *testing.T) {
	c := newCore(ModeDualPG, nil)
	writes := 0
	readSrc := func(context.Context) (string, uint64, error) { return "B", 2, nil }
	readDst := func(context.Context) (string, uint64, error) { return "older", 1, nil }
	updateDst := func(context.Context, string, uint64) error { writes++; return nil }

	require.NoError(t, copyUpdated(context.Background(), &c, readSrc, readDst, updateDst))

	assert.Equal(t, 1, writes)
}
