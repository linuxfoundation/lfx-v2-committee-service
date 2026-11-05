// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// rig is a composite over a real KV store and a real PostgreSQL store, and the metrics it counts into.
type rig struct {
	store  *Store
	kv, pg port.CommitteeReaderWriter
	reader *sdkmetric.ManualReader
}

// newRig builds the composite for the mode. wrapPG, when given, wraps the PostgreSQL store the composite uses, to make it
// fail or slow down, while the rig's pg stays the real one.
func newRig(t *testing.T, mode Mode, wrapPG func(port.CommitteeReaderWriter) port.CommitteeReaderWriter, opts ...Option) *rig {
	t.Helper()
	kv, pg := natstest.NewStore(t), postgrestest.NewStore(t)
	reader := sdkmetric.NewManualReader()
	metrics := newMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test"))
	var used port.CommitteeReaderWriter = pg
	if wrapPG != nil {
		used = wrapPG(pg)
	}
	store, err := New(mode, kv, used, append([]Option{WithMetrics(metrics)}, opts...)...)
	require.NoError(t, err)
	return &rig{store: store, kv: kv, pg: pg, reader: reader}
}

// counts returns what a counter holds, by the operation it was counted for.
func (r *rig) counts(t *testing.T, name string) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, r.reader.Collect(context.Background(), &rm))
	out := map[string]int64{}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != name || !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				operation, _ := point.Attributes.Value("operation")
				out[operation.AsString()] += point.Value
			}
		}
	}
	return out
}

const (
	failures   = "committee.storage.secondary_write_failures"
	mismatches = "committee.storage.read_compare_mismatches"
	compareErr = "committee.storage.read_compare_errors"
)

// sameRecord fails the test unless both stores hold the same committee and settings, with the same revisions.
func sameRecord(t *testing.T, r *rig, uid string) {
	t.Helper()
	ctx := context.Background()
	kvBase, kvRev, err := r.kv.GetBase(ctx, uid)
	require.NoError(t, err)
	pgBase, pgRev, err := r.pg.GetBase(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, kvRev, pgRev, "the revision of the base is the one KV gave it")
	kvDigest, err := Digest(kvBase)
	require.NoError(t, err)
	pgDigest, err := Digest(pgBase)
	require.NoError(t, err)
	assert.Equal(t, kvDigest, pgDigest, "the base is the same")

	kvSettings, kvSetRev, kvErr := r.kv.GetSettings(ctx, uid)
	pgSettings, pgSetRev, pgErr := r.pg.GetSettings(ctx, uid)
	if kvErr != nil {
		assert.True(t, isNotFound(kvErr) && isNotFound(pgErr), "settings are in both stores or in neither")
		return
	}
	require.NoError(t, pgErr)
	assert.Equal(t, kvSetRev, pgSetRev, "the revision of the settings is the one KV gave it")
	kvDigest, err = Digest(kvSettings)
	require.NoError(t, err)
	pgDigest, err = Digest(pgSettings)
	require.NoError(t, err)
	assert.Equal(t, kvDigest, pgDigest, "the settings are the same")
}

func TestNew(t *testing.T) {
	kv, pg := natstest.NewStore(t), postgrestest.NewStore(t)

	for _, tc := range []struct {
		mode   Mode
		kv, pg port.CommitteeReaderWriter
		ok     bool
	}{
		{ModeKV, kv, nil, true},
		{ModeKV, nil, pg, false},
		{ModeDual, kv, pg, true},
		{ModeDual, kv, nil, false},
		{ModeDual, nil, pg, false},
		{ModePostgres, nil, pg, true},
		{ModePostgres, kv, nil, false},
		{ModeDualPG, kv, pg, false},
		{Mode("other"), kv, pg, false},
	} {
		_, err := New(tc.mode, tc.kv, tc.pg)
		assert.Equal(t, tc.ok, err == nil, "%s: %v", tc.mode, err)
	}
}

// The defaults bound what the secondary store can cost a request: a sampled read at most the compare timeout, a write at
// most the write timeout, and only a small fraction of reads is sampled.
func TestNew_Defaults(t *testing.T) {
	kv, pg := natstest.NewStore(t), postgrestest.NewStore(t)

	s, err := New(ModeDual, kv, pg)

	require.NoError(t, err)
	assert.Equal(t, 0.01, s.rate)
	assert.Equal(t, 2*time.Second, s.compareTimeout)
	assert.Equal(t, 3*time.Second, s.writeTimeout)

	s, err = New(ModeDual, kv, pg, WithSampleRate(5), WithTimeouts(-time.Second, 0))
	require.NoError(t, err)
	assert.Equal(t, 1.0, s.rate, "a rate above 1 is 1")
	assert.Equal(t, 2*time.Second, s.compareTimeout, "a timeout that is not positive keeps the default")
	s, err = New(ModeDual, kv, pg, WithSampleRate(-1))
	require.NoError(t, err)
	assert.Equal(t, 0.0, s.rate, "a rate below 0 is 0")
}

// Whatever the mode, the composite behaves as one store does: the same suite the KV store and the PostgreSQL store pass.
func TestContract_Committee(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModeDual, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			storetest.RunCommitteeContract(t, storetest.Backend{Name: "composite-" + string(mode)}, func(t *testing.T) port.CommitteeReaderWriter {
				r := newRig(t, mode, nil, WithSampleRate(1))
				t.Cleanup(func() {
					// Every read of the suite was compared, and every write copied: the stores never disagreed and nothing failed.
					assert.Empty(t, r.counts(t, mismatches), "no false mismatch")
					assert.Empty(t, r.counts(t, compareErr))
					assert.Empty(t, r.counts(t, failures))
				})
				return r.store
			})
		})
	}
}

func TestStore_EachModeWritesToItsStores(t *testing.T) {
	ctx := context.Background()
	for mode, want := range map[Mode]struct{ kv, pg bool }{ModeKV: {true, false}, ModeDual: {true, true}, ModePostgres: {false, true}} {
		t.Run(string(mode), func(t *testing.T) {
			r := newRig(t, mode, nil)

			require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

			_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
			_, _, pgErr := r.pg.GetBase(ctx, "uid-1")
			assert.Equal(t, want.kv, kvErr == nil, "KV holds the committee: %v", kvErr)
			assert.Equal(t, want.pg, pgErr == nil, "PostgreSQL holds the committee: %v", pgErr)
		})
	}
}

func TestStore_DualWritesCarryTheKVRevisionToPostgreSQL(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	sameRecord(t, r, "uid-1")

	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "changed"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	sameRecord(t, r, "uid-1")
	_, updated, err := r.pg.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.Greater(t, updated, revision, "a token from before the update is stale in both stores")

	settings, settingsRevision, err := r.store.GetSettings(ctx, "uid-1")
	require.NoError(t, err)
	settings.ShowMeetingAttendees = !settings.ShowMeetingAttendees
	require.NoError(t, r.store.UpdateSetting(ctx, settings, settingsRevision))
	sameRecord(t, r, "uid-1")

	changed, written, err := r.store.UpdateTotalMembers(ctx, "uid-1", 7)
	require.NoError(t, err)
	require.True(t, written)
	assert.Equal(t, 7, changed.TotalMembers)
	sameRecord(t, r, "uid-1")

	_, written, err = r.store.UpdateHasMailingList(ctx, "uid-1", !base.HasMailingList)
	require.NoError(t, err)
	require.True(t, written)
	sameRecord(t, r, "uid-1")

	_, written, err = r.store.UpdateTotalMembers(ctx, "uid-1", 7)
	require.NoError(t, err)
	assert.False(t, written, "an unchanged counter writes nothing, in either store")
	sameRecord(t, r, "uid-1")

	_, revision, err = r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	require.NoError(t, r.store.Delete(ctx, "uid-1", revision))
	_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
	_, _, pgErr := r.pg.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(kvErr) && isNotFound(pgErr), "the committee is gone from both")
	assert.Empty(t, r.counts(t, failures), "nothing failed on the way")
}

// noopKV is a KV store whose updates report success and write nothing, as an update does when the stored bytes already
// equal it after a lost swap.
type noopKV struct{ port.CommitteeReaderWriter }

func (noopKV) UpdateBase(context.Context, *model.Committee, uint64) error            { return nil }
func (noopKV) UpdateSetting(context.Context, *model.CommitteeSettings, uint64) error { return nil }

// An update that KV reports as done without writing leaves its revision where it was, so there is nothing to carry over,
// and PostgreSQL, which would refuse a revision that is not above the one it holds, is not asked.
func TestStore_DualAnUpdateKVDidNotWriteIsNotCarriedOverAndNotCountedAsAFailure(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	settings, settingsRevision, err := r.store.GetSettings(ctx, "uid-1")
	require.NoError(t, err)
	r.store.CommitteeReaderWriter = noopKV{r.kv}

	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	require.NoError(t, r.store.UpdateSetting(ctx, settings, settingsRevision))

	assert.Empty(t, r.counts(t, failures))
	sameRecord(t, r, "uid-1")
}

func TestStore_DualACommitteeWithoutSettingsIsCopiedWithoutThem(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	committee := storetest.NewCommittee("uid-1", "project-1", "Committee One")
	committee.CommitteeSettings = nil

	require.NoError(t, r.store.Create(ctx, committee))

	assert.Empty(t, r.counts(t, failures))
	sameRecord(t, r, "uid-1")
	_, _, err := r.pg.GetSettings(ctx, "uid-1")
	assert.True(t, isNotFound(err), "there are no settings to copy")
}

// A delete leaves nothing behind in PostgreSQL even when PostgreSQL was behind, because KV has decided the committee is gone
// and storage migrate never deletes.
func TestStore_DualADeleteRemovesWhatPostgreSQLHoldsWhateverItsRevision(t *testing.T) {
	ctx := context.Background()

	t.Run("a row PostgreSQL holds at another revision", func(t *testing.T) {
		r := newRig(t, ModeDual, nil)
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
		base, revision, err := r.store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		behind, pgRevision, err := r.pg.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		behind.Description = "an update PostgreSQL has and KV has not"
		wctx, err := postgres.WithRevision(ctx, pgRevision+1000)
		require.NoError(t, err)
		require.NoError(t, r.pg.UpdateBase(wctx, &model.Committee{CommitteeBase: *behind}, pgRevision))
		_ = base

		require.NoError(t, r.store.Delete(ctx, "uid-1", revision))

		_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
		_, _, pgErr := r.pg.GetBase(ctx, "uid-1")
		_, _, settingsErr := r.pg.GetSettings(ctx, "uid-1")
		assert.True(t, isNotFound(kvErr) && isNotFound(pgErr) && isNotFound(settingsErr), "the committee and its settings are gone from both")
		assert.Empty(t, r.counts(t, failures))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-2", "project-1", "Committee One")), "the name is free again")
		assert.Empty(t, r.counts(t, failures), "and is free in PostgreSQL too")
	})

	t.Run("a row PostgreSQL does not have is not a failure", func(t *testing.T) {
		r := newRig(t, ModeDual, nil)
		require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")), "in KV only")
		_, revision, err := r.kv.GetBase(ctx, "uid-1")
		require.NoError(t, err)

		require.NoError(t, r.store.Delete(ctx, "uid-1", revision))

		assert.Empty(t, r.counts(t, failures), "it is not there, which is where a delete leaves it")
	})
}

// deletingKV is a KV store that deletes the committee as its settings are read back for the copy, which is where a delete
// that finishes while a create is being copied lands.
type deletingKV struct {
	port.CommitteeReaderWriter
	once sync.Once
}

func (d *deletingKV) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	settings, revision, err := d.CommitteeReaderWriter.GetSettings(ctx, uid)
	d.once.Do(func() {
		if _, current, getErr := d.GetBase(ctx, uid); getErr == nil {
			_ = d.Delete(ctx, uid, current)
		}
	})
	return settings, revision, err
}

// A delete that finishes in KV while a create is being copied finds nothing to remove in PostgreSQL, so the copy must not
// leave the committee behind.
func TestStore_DualACreateCopiedWhileADeleteFinishesDoesNotBringTheCommitteeBack(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	r.store.kv = &deletingKV{CommitteeReaderWriter: r.kv} // what the copy reads from; the primary store is still the real one

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	_, _, kvErr := r.kv.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(kvErr), "the delete finished in KV while the copy was being made")
	_, _, pgErr := r.pg.GetBase(ctx, "uid-1")
	_, _, settingsErr := r.pg.GetSettings(ctx, "uid-1")
	assert.True(t, isNotFound(pgErr) && isNotFound(settingsErr), "the committee and its settings are in neither store")
	assert.Empty(t, r.counts(t, failures))
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-2", "project-1", "Committee One")), "the name is free again")
	assert.Empty(t, r.counts(t, failures), "and is free in PostgreSQL too")
}

// vanishingPG is a PostgreSQL store whose delete reports a stale revision and whose row is gone when it is read again, as
// when another instance has just removed it.
type vanishingPG struct{ port.CommitteeReaderWriter }

func (vanishingPG) Delete(context.Context, string, uint64) error {
	return errs.NewConflict("committee has been modified by another process")
}

func (vanishingPG) GetBase(context.Context, string) (*model.CommitteeBase, uint64, error) {
	return nil, 0, errs.NewNotFound("committee not found")
}

func TestStore_DualARowThatGoesAwayDuringAConvergingDeleteIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return vanishingPG{pg} })
	require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	_, revision, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)

	require.NoError(t, r.store.Delete(ctx, "uid-1", revision))

	assert.Empty(t, r.counts(t, failures), "the delete converged: there is nothing left in PostgreSQL")
}

// flakyPG is a PostgreSQL store whose updates fail while fail is set.
type flakyPG struct {
	port.CommitteeReaderWriter
	fail *atomic.Bool
}

func (f flakyPG) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.UpdateBase(ctx, committee, revision)
}

func (f flakyPG) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	if f.fail.Load() {
		return errPG
	}
	return f.CommitteeReaderWriter.UpdateSetting(ctx, settings, revision)
}

// A copy that was missed is made up for by the next update of the same record, which writes what KV holds then.
func TestStore_DualTheNextUpdateBringsPostgreSQLUpToWhatKVHolds(t *testing.T) {
	ctx := context.Background()
	var fail atomic.Bool
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return flakyPG{pg, &fail} })
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	settings, settingsRevision, err := r.store.GetSettings(ctx, "uid-1")
	require.NoError(t, err)

	fail.Store(true)
	base.Description = "first"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	settings.ShowMeetingAttendees = !settings.ShowMeetingAttendees
	require.NoError(t, r.store.UpdateSetting(ctx, settings, settingsRevision))
	assert.Equal(t, map[string]int64{"update_committee": 1, "update_settings": 1}, r.counts(t, failures), "both copies were missed")

	fail.Store(false)
	base, revision, err = r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "second"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	settings, settingsRevision, err = r.store.GetSettings(ctx, "uid-1")
	require.NoError(t, err)
	settings.ShowMeetingAttendees = !settings.ShowMeetingAttendees
	require.NoError(t, r.store.UpdateSetting(ctx, settings, settingsRevision))

	assert.Equal(t, map[string]int64{"update_committee": 1, "update_settings": 1}, r.counts(t, failures), "nothing more failed")
	sameRecord(t, r, "uid-1")
}

// Writers that race on one committee leave PostgreSQL behind KV at worst, never ahead of it, and the next write makes it
// the same.
func TestStore_DualConcurrentWritersNeverLeavePostgreSQLAheadAndTheNextWriteHeals(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	var wg sync.WaitGroup
	for writer := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 5 {
				_, _, _ = r.store.UpdateTotalMembers(ctx, "uid-1", writer*100+i+1)
				if base, revision, err := r.store.GetBase(ctx, "uid-1"); err == nil {
					base.Description = fmt.Sprintf("writer %d, %d", writer, i)
					_ = r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision) // a stale token is refused by KV
				}
			}
		}()
	}
	wg.Wait()

	_, kvRevision, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	_, pgRevision, err := r.pg.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	assert.LessOrEqual(t, pgRevision, kvRevision, "PostgreSQL is never ahead of KV")

	_, written, err := r.store.UpdateTotalMembers(ctx, "uid-1", 9999)
	require.NoError(t, err)
	require.True(t, written)
	sameRecord(t, r, "uid-1")
}

// panickingReadPG is a PostgreSQL store that panics when it is read.
type panickingReadPG struct{ port.CommitteeReaderWriter }

func (panickingReadPG) GetBase(context.Context, string) (*model.CommitteeBase, uint64, error) {
	panic("boom")
}

func TestStore_DualAPanicInASampledReadIsCountedAndNeverFailsTheRead(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return panickingReadPG{pg} }, WithSampleRate(1))
	require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	base, _, err := r.store.GetBase(ctx, "uid-1")

	require.NoError(t, err)
	assert.Equal(t, "Committee One", base.Name, "the KV result is returned")
	assert.Equal(t, map[string]int64{"get_committee": 1}, r.counts(t, compareErr))
}

// slowWritePG is a PostgreSQL store whose updates answer only when the request is cancelled.
type slowWritePG struct{ port.CommitteeReaderWriter }

func (slowWritePG) UpdateBase(ctx context.Context, _ *model.Committee, _ uint64) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStore_DualASlowSecondaryWriteAddsAtMostTheWriteTimeoutAndIsCounted(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return slowWritePG{pg} },
		WithTimeouts(0, 200*time.Millisecond))
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "changed"

	started := time.Now()
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	assert.Less(t, time.Since(started), 3*time.Second, "the request is not held by the secondary store")
	assert.Equal(t, map[string]int64{"update_committee": 1}, r.counts(t, failures))
}

func TestStore_DualAFailedWriteIsRefusedLikeKVRefusesIt(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)

	base.Description = "changed"
	err = r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision-1)

	require.Error(t, err, "a stale token is refused by KV, which decides")
	sameRecord(t, r, "uid-1")
	assert.Empty(t, r.counts(t, failures), "KV's refusal is not a secondary failure, and nothing is written to PostgreSQL")
	require.Error(t, r.store.Create(ctx, storetest.NewCommittee("uid-2", "project-1", "Committee One")), "a taken name is a conflict in KV")
	_, _, pgErr := r.pg.GetBase(ctx, "uid-2")
	assert.True(t, isNotFound(pgErr))
}

// failingPG is a PostgreSQL store whose writes fail.
type failingPG struct{ port.CommitteeReaderWriter }

var errPG = errors.New("connection reset")

func (failingPG) Create(context.Context, *model.Committee) error { return errPG }
func (failingPG) UpdateBase(context.Context, *model.Committee, uint64) error {
	return errPG
}
func (failingPG) UpdateSetting(context.Context, *model.CommitteeSettings, uint64) error { return errPG }
func (failingPG) Delete(context.Context, string, uint64) error                          { return errPG }
func (failingPG) UpdateHasMailingList(context.Context, string, bool) (*model.CommitteeBase, bool, error) {
	return nil, false, errPG
}
func (failingPG) UpdateTotalMembers(context.Context, string, int) (*model.CommitteeBase, bool, error) {
	return nil, false, errPG
}

func TestStore_DualASecondaryFailureIsCountedAndNeverFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return failingPG{pg} })

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "changed"
	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))
	settings, settingsRevision, err := r.store.GetSettings(ctx, "uid-1")
	require.NoError(t, err)
	require.NoError(t, r.store.UpdateSetting(ctx, settings, settingsRevision))
	_, _, err = r.store.UpdateTotalMembers(ctx, "uid-1", 3)
	require.NoError(t, err)
	current, _, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	_, written, err := r.store.UpdateHasMailingList(ctx, "uid-1", !current.HasMailingList)
	require.NoError(t, err)
	require.True(t, written)
	_, revision, err = r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	require.NoError(t, r.store.Delete(ctx, "uid-1", revision))

	_, _, err = r.kv.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(err), "KV decided every one of them, the delete included")
	assert.Equal(t, map[string]int64{
		"create_committee": 1, "update_committee": 1, "update_settings": 1, "update_total_members": 1,
		"update_has_mailing_list": 1, "delete_committee": 1,
	}, r.counts(t, failures))
}

// panickingPG is a PostgreSQL store that panics on every call.
type panickingPG struct{ port.CommitteeReaderWriter }

func (panickingPG) Create(context.Context, *model.Committee) error { panic("boom") }

func TestStore_DualAPanicInTheSecondaryStoreIsCountedAndNeverFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return panickingPG{pg} })

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	_, _, err := r.kv.GetBase(ctx, "uid-1")
	require.NoError(t, err, "KV decided")
	assert.Equal(t, map[string]int64{"create_committee": 1}, r.counts(t, failures))
}

// A secondary store that is behind, because the record was never copied, is a failure to count and not an error.
func TestStore_DualAWriteTheSecondaryCannotApplyIsCounted(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, nil)
	require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")), "in KV only: it was never copied")
	base, revision, err := r.store.GetBase(ctx, "uid-1")
	require.NoError(t, err)
	base.Description = "changed"

	require.NoError(t, r.store.UpdateBase(ctx, &model.Committee{CommitteeBase: *base}, revision))

	assert.Equal(t, map[string]int64{"update_committee": 1}, r.counts(t, failures))
	_, _, err = r.pg.GetBase(ctx, "uid-1")
	assert.True(t, isNotFound(err), "storage migrate is what brings it over")
}

// cancelingKV cancels the request's context as soon as the KV write has succeeded, like a client that went away.
type cancelingKV struct {
	port.CommitteeReaderWriter
	cancel context.CancelFunc
}

func (k cancelingKV) Create(ctx context.Context, committee *model.Committee) error {
	err := k.CommitteeReaderWriter.Create(ctx, committee)
	k.cancel()
	return err
}

func TestStore_DualTheSecondaryWriteDoesNotDependOnTheRequestStayingOpen(t *testing.T) {
	r := newRig(t, ModeDual, nil)
	ctx, cancel := context.WithCancel(context.Background())
	r.store.kv, r.store.CommitteeReaderWriter = cancelingKV{r.kv, cancel}, cancelingKV{r.kv, cancel}

	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	assert.Empty(t, r.counts(t, failures))
	sameRecord(t, r, "uid-1")
}

func TestStore_ReadsComeFromThePrimaryStore(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []Mode{ModeKV, ModeDual, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			r := newRig(t, mode, nil, WithSampleRate(0))
			// The same uid holds different names in the two stores, so each read says which one it came from.
			require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "In KV")))
			require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "In PostgreSQL")))

			base, _, err := r.store.GetBase(ctx, "uid-1")
			require.NoError(t, err)
			uid, err := r.store.FindUIDByProjectAndName(ctx, "project-1", map[Mode]string{ModeKV: "In KV", ModeDual: "In KV", ModePostgres: "In PostgreSQL"}[mode])
			require.NoError(t, err)

			want := map[Mode]string{ModeKV: "In KV", ModeDual: "In KV", ModePostgres: "In PostgreSQL"}[mode]
			assert.Equal(t, want, base.Name)
			assert.Equal(t, "uid-1", uid)
		})
	}
}

func TestStore_DualCompareCountsWhatDiffers(t *testing.T) {
	ctx := context.Background()

	t.Run("two stores that agree count nothing", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

		_, _, _ = r.store.GetBase(ctx, "uid-1")
		_, _, _ = r.store.GetSettings(ctx, "uid-1")
		_, _ = r.store.ListAllUIDs(ctx)
		_, _ = r.store.FindUIDByProjectAndName(ctx, "project-1", "Committee One")
		_, _, _ = r.store.GetBase(ctx, "uid-missing")
		_, _ = r.store.FindUIDByProjectAndName(ctx, "project-1", "No such committee")

		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("a content difference, a revision difference and a record only one store has are mismatches", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-2", "project-1", "Committee Two")))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-3", "project-1", "Committee Three")))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-4", "project-1", "Committee Four")))
		// uid-1: PostgreSQL differs in content. uid-2: only in KV. uid-3: the same content at another revision. uid-4 has
		// another name in PostgreSQL, and the settings of uid-1 differ there.
		other, rev1, err := r.pg.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		other.Description = "drifted"
		withRevision, err := postgres.WithRevision(ctx, rev1+1000)
		require.NoError(t, err)
		require.NoError(t, r.pg.UpdateBase(withRevision, &model.Committee{CommitteeBase: *other}, rev1))
		_, rev2, err := r.pg.GetBase(ctx, "uid-2")
		require.NoError(t, err)
		require.NoError(t, r.pg.Delete(ctx, "uid-2", rev2))
		same, rev3, err := r.pg.GetBase(ctx, "uid-3")
		require.NoError(t, err)
		withRevision, err = postgres.WithRevision(ctx, rev3+1000)
		require.NoError(t, err)
		require.NoError(t, r.pg.UpdateBase(withRevision, &model.Committee{CommitteeBase: *same}, rev3))

		renamed, rev4, err := r.pg.GetBase(ctx, "uid-4")
		require.NoError(t, err)
		renamed.Name = "Renamed"
		withRevision, err = postgres.WithRevision(ctx, rev4+1000)
		require.NoError(t, err)
		require.NoError(t, r.pg.UpdateBase(withRevision, &model.Committee{CommitteeBase: *renamed}, rev4))
		drifted, settingsRev, err := r.pg.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		drifted.ShowMeetingAttendees = !drifted.ShowMeetingAttendees
		withRevision, err = postgres.WithRevision(ctx, settingsRev+1000)
		require.NoError(t, err)
		require.NoError(t, r.pg.UpdateSetting(withRevision, drifted, settingsRev))

		// uid-5 and uid-9 are different committees with one name in the two stores, so both find the name under another uid.
		// uid-6 is the same revision in both stores with other content in PostgreSQL.
		require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-5", "project-1", "Committee Five")))
		require.NoError(t, r.pg.Create(ctx, storetest.NewCommittee("uid-9", "project-1", "Committee Five")))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-6", "project-1", "Committee Six")))
		sixBase, sixRevision, err := r.pg.GetBase(ctx, "uid-6")
		require.NoError(t, err)
		require.NoError(t, r.pg.Delete(ctx, "uid-6", sixRevision))
		sixBase.Description = "same revision, other content"
		withRevision, err = postgres.WithRevision(ctx, sixRevision)
		require.NoError(t, err)
		require.NoError(t, r.pg.Create(withRevision, &model.Committee{CommitteeBase: *sixBase}))

		for _, uid := range []string{"uid-1", "uid-2", "uid-3", "uid-6"} {
			_, _, err := r.store.GetBase(ctx, uid)
			require.NoError(t, err, "the KV result is returned whatever PostgreSQL says")
		}
		_, _, err = r.store.GetSettings(ctx, "uid-1")
		require.NoError(t, err)
		uids, err := r.store.ListAllUIDs(ctx)
		require.NoError(t, err)
		assert.Len(t, uids, 6)
		found, err := r.store.FindUIDByProjectAndName(ctx, "project-1", "Committee Four")
		require.NoError(t, err)
		assert.Equal(t, "uid-4", found, "the KV answer is returned")
		found, err = r.store.FindUIDByProjectAndName(ctx, "project-1", "Committee Five")
		require.NoError(t, err)
		assert.Equal(t, "uid-5", found, "the KV answer is returned")

		assert.Equal(t, map[string]int64{"get_committee": 4, "get_settings": 1, "list_committees": 1, "find_committee": 2}, r.counts(t, mismatches))
	})

	t.Run("a secondary store that cannot be read is counted apart from a mismatch", func(t *testing.T) {
		r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return unreadablePG{pg} }, WithSampleRate(1))
		require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

		base, _, err := r.store.GetBase(ctx, "uid-1")

		require.NoError(t, err, "the read still succeeds")
		assert.Equal(t, "Committee One", base.Name)
		assert.Equal(t, map[string]int64{"get_committee": 1}, r.counts(t, compareErr))
		assert.Empty(t, r.counts(t, mismatches))
	})

	t.Run("a read that failed in the primary store is not compared", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(1))
		_, _, err := r.store.GetSettings(ctx, "") // an empty uid is a validation error
		require.Error(t, err)
		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})
}

// unreadablePG is a PostgreSQL store that cannot be read.
type unreadablePG struct{ port.CommitteeReaderWriter }

func (unreadablePG) GetBase(context.Context, string) (*model.CommitteeBase, uint64, error) {
	return nil, 0, errs.NewUnexpected("connection reset")
}

func TestStore_DualOnlyASampledFractionOfReadsIsCompared(t *testing.T) {
	ctx := context.Background()
	draws := []float64{0.001, 0.5, 0.009, 0.99, 0.8}
	next := 0
	r := newRig(t, ModeDual, nil, WithSampleRate(0.01), WithRandom(func() float64 { v := draws[next%len(draws)]; next++; return v }))
	require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")), "KV only, so every comparison is a mismatch")

	for range draws {
		_, _, err := r.store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
	}

	assert.Equal(t, map[string]int64{"get_committee": 2}, r.counts(t, mismatches), "the two draws under the rate were compared, and not the three above it")

	t.Run("a rate of zero never compares and is not asked to draw", func(t *testing.T) {
		r := newRig(t, ModeDual, nil, WithSampleRate(0), WithRandom(func() float64 { t.Fatal("drew with a rate of zero"); return 0 }))
		require.NoError(t, r.kv.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
		_, _, err := r.store.GetBase(ctx, "uid-1")
		require.NoError(t, err)
		assert.Empty(t, r.counts(t, mismatches))
	})

	t.Run("only dual mode compares", func(t *testing.T) {
		for _, mode := range []Mode{ModeKV, ModePostgres} {
			r := newRig(t, mode, nil, WithSampleRate(1))
			require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))
			_, _, err := r.store.GetBase(ctx, "uid-1")
			require.NoError(t, err)
			assert.Empty(t, r.counts(t, mismatches), mode)
		}
	})
}

// slowPG is a PostgreSQL store that answers a read only when the request is cancelled.
type slowPG struct{ port.CommitteeReaderWriter }

func (slowPG) GetBase(ctx context.Context, _ string) (*model.CommitteeBase, uint64, error) {
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

func TestStore_DualASlowSecondaryStoreCostsASampledReadAtMostTheCompareTimeout(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, ModeDual, func(pg port.CommitteeReaderWriter) port.CommitteeReaderWriter { return slowPG{pg} },
		WithSampleRate(1), WithTimeouts(100*time.Millisecond, 0))
	require.NoError(t, r.store.Create(ctx, storetest.NewCommittee("uid-1", "project-1", "Committee One")))

	started := time.Now()
	base, _, err := r.store.GetBase(ctx, "uid-1")

	require.NoError(t, err)
	assert.Equal(t, "Committee One", base.Name)
	assert.Less(t, time.Since(started), 2*time.Second)
	assert.Equal(t, map[string]int64{"get_committee": 1}, r.counts(t, compareErr))
}

func TestSameSet(t *testing.T) {
	assert.True(t, sameSet(nil, nil))
	assert.True(t, sameSet([]string{"b", "a"}, []string{"a", "b"}), "the order does not matter")
	assert.False(t, sameSet([]string{"a"}, []string{"a", "b"}))
	assert.False(t, sameSet([]string{"a", "a"}, []string{"a", "b"}))
	a := []string{"b", "a"}
	sameSet(a, []string{"a", "b"})
	assert.Equal(t, []string{"b", "a"}, a, "the lists are not reordered")
}
