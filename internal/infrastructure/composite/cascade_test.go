// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// cascadeRig is the composite of the committees over a real KV store and a real PostgreSQL store, with a committee and
// everything that belongs to it in PostgreSQL, and in KV the committee, its settings and an application.
type cascadeRig struct {
	store  *Store
	writer port.CommitteeReaderWriter
	kv     natstest.Backend
	pg     *postgres.Store
	reader *sdkmetric.ManualReader
}

func newCascadeRig(t *testing.T, mode Mode) *cascadeRig {
	t.Helper()
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)
	metrics, reader := newMetricsReader()
	store, err := New(mode, kv, pg, WithMetrics(metrics))
	require.NoError(t, err)
	writer, err := WithCascade(store, pg)
	require.NoError(t, err)
	return &cascadeRig{store: store, writer: writer, kv: kv, pg: pg, reader: reader}
}

func (r *cascadeRig) counts(t *testing.T, name string) map[string]int64 {
	t.Helper()
	return countsOf(t, r.reader, name)
}

// seed puts a committee with one child of each kind in PostgreSQL, and the committee and an application in KV as well.
func (r *cascadeRig) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	require.NoError(t, r.pg.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
	require.NoError(t, r.pg.CreateInvite(ctx, storetest.NewInvite("i-1", "uid-1", "first.last@example.com")))
	require.NoError(t, r.pg.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))
	require.NoError(t, r.pg.CreateLink(ctx, storetest.NewLink("l-1", "uid-1")))
	require.NoError(t, r.pg.CreateLinkFolder(ctx, storetest.NewFolder("f-1", "uid-1", "Charters")))
	require.NoError(t, r.pg.CreateDocumentMetadata(ctx, storetest.NewDocument("d-1", "uid-1", "Minutes")))
	_, err := r.pg.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	_, err = r.pg.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)

	require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	require.NoError(t, r.kv.CreateApplication(ctx, storetest.NewApplication("a-1", "uid-1", "first.last@example.com")))
	require.NoError(t, r.kv.CreateMember(ctx, storetest.NewMember("m-1", "uid-1", "first.last@example.com")))
}

func (r *cascadeRig) revision(t *testing.T) uint64 {
	t.Helper()
	_, revision, err := r.pg.GetBase(context.Background(), "uid-1")
	require.NoError(t, err)
	return revision
}

func TestWithCascade_OnlyAModeWithPostgreSQLAsItsPrimaryStoreCascades(t *testing.T) {
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)
	for mode, cascades := range map[Mode]bool{ModeKV: false, ModeDual: false, ModeDualPG: true, ModePostgres: true} {
		t.Run(string(mode), func(t *testing.T) {
			store, err := New(mode, kv, pg)
			require.NoError(t, err)

			writer, err := WithCascade(store, pg)

			require.NoError(t, err)
			_, ok := writer.(port.CommitteeCascadeDeleter)
			assert.Equal(t, cascades, ok)
			if !cascades {
				assert.Same(t, store, writer, "the committee delete the service has always had is untouched")
			}
		})
	}
}

func TestWithCascade_AModeThatCascadesNeedsTheCascadingDeleteOfPostgreSQL(t *testing.T) {
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)
	for _, mode := range []Mode{ModeKV, ModeDual, ModeDualPG, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			store, err := New(mode, kv, pg)
			require.NoError(t, err)

			_, err = WithCascade(store, nil)

			assert.Equal(t, mode.PostgresIsPrimary(), err != nil)
		})
	}
}

func TestCascade_PostgreSQLRemovesEverythingAndReportsWhatItRemoved(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []Mode{ModeDualPG, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			r := newCascadeRig(t, mode)
			r.seed(t)

			removed, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t))

			require.NoError(t, err)
			assert.Equal(t, []string{"m-1"}, removed.MemberUIDs)
			assert.Equal(t, []string{"i-1"}, removed.InviteUIDs)
			assert.Equal(t, []string{"l-1"}, removed.LinkUIDs)
			assert.Equal(t, []string{"f-1"}, removed.FolderUIDs)
			assert.Equal(t, []string{"d-1"}, removed.DocumentUIDs)
			assert.Equal(t, []string{"b-1"}, removed.BriefUIDs)
			require.Len(t, removed.Applications, 1)
			assert.Equal(t, "a-1", removed.Applications[0].UID)
			_, _, baseErr := r.pg.GetBase(ctx, "uid-1")
			assert.True(t, isNotFound(baseErr), "the committee is gone from PostgreSQL")
			_, _, memberErr := r.pg.GetMember(ctx, "m-1")
			assert.True(t, isNotFound(memberErr), "and so are its children")
			_, _, docErr := r.pg.GetDocumentMetadata(ctx, "uid-1", "d-1")
			assert.True(t, isNotFound(docErr))
			throttle, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
			require.NoError(t, err)
			assert.Nil(t, throttle)
			assert.Empty(t, r.counts(t, failures))
		})
	}
}

func TestCascade_APostgresDeleteDoesNotTouchKV(t *testing.T) {
	ctx := context.Background()
	r := newCascadeRig(t, ModePostgres)
	r.seed(t)

	_, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t))

	require.NoError(t, err)
	_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
	assert.NoError(t, kvErr, "KV is not a store of this mode")
}

// In dual-pg mode the committee, its settings and the applications are removed from KV as the service does with KV in a
// committee delete, and the other children are left, as a delete in KV mode leaves them.
func TestCascade_DualPGRemovesWhatTheServiceRemovesFromKV(t *testing.T) {
	ctx := context.Background()
	r := newCascadeRig(t, ModeDualPG)
	r.seed(t)

	_, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t))

	require.NoError(t, err)
	_, _, baseErr := r.kv.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(baseErr), "the committee is gone from KV")
	_, _, appErr := r.kv.GetApplication(ctx, "a-1")
	assert.True(t, isNotFound(appErr), "and its application")
	_, _, memberErr := r.kv.GetMember(ctx, "m-1")
	assert.NoError(t, memberErr, "the other children are left in KV, as a delete in KV mode leaves them")
	assert.Empty(t, r.counts(t, failures))
}

// KV has revisions of its own, so the committee is removed under the one KV has, not the token PostgreSQL gave.
func TestCascade_DualPGRemovesTheCommitteeFromKVWhateverItsRevision(t *testing.T) {
	ctx := context.Background()
	r := newCascadeRig(t, ModeDualPG)
	r.seed(t)
	base, revision, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "an update only KV has"
	require.NoError(t, r.kv.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	_, err = r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t))

	require.NoError(t, err)
	_, _, baseErr := r.kv.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(baseErr))
	assert.Empty(t, r.counts(t, failures))
}

func TestCascade_DualPGAFailedCopyToKVIsCountedAndNeverFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	r := newCascadeRig(t, ModeDualPG)
	r.seed(t)
	r.store.dst = failingPG{r.kv}

	removed, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t))

	require.NoError(t, err, "PostgreSQL decided")
	assert.Len(t, removed.MemberUIDs, 1)
	assert.Equal(t, map[string]int64{"delete_committee_cascade": 1}, r.counts(t, failures))
}

// A revision that is not current removes nothing, from PostgreSQL or from KV.
func TestCascade_AStaleRevisionRemovesNothing(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []Mode{ModeDualPG, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			r := newCascadeRig(t, mode)
			r.seed(t)

			_, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t)+1000)

			var conflict errs.Conflict
			require.ErrorAs(t, err, &conflict)
			_, _, baseErr := r.pg.GetBase(ctx, "uid-1")
			assert.NoError(t, baseErr, "the committee is still in PostgreSQL")
			_, _, memberErr := r.pg.GetMember(ctx, "m-1")
			assert.NoError(t, memberErr, "and its children")
			_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
			assert.NoError(t, kvErr, "and in KV")
			assert.Empty(t, r.counts(t, failures))
		})
	}
}

func TestCascade_ACommitteeThatIsNotThereIsNotFound(t *testing.T) {
	ctx := context.Background()
	r := newCascadeRig(t, ModeDualPG)

	_, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "missing", 1)

	assert.True(t, isNotFound(err))
}

// A committee that KV never got, because the copy of its create failed, is where a delete leaves it: not a failure.
func TestCascade_DualPGACommitteeKVNeverHadIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	r := newCascadeRig(t, ModeDualPG)
	require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One"))) // never copied to KV

	_, err := r.writer.(port.CommitteeCascadeDeleter).DeleteCommitteeCascade(ctx, "uid-1", r.revision(t))

	require.NoError(t, err)
	assert.Empty(t, r.counts(t, failures))
}
