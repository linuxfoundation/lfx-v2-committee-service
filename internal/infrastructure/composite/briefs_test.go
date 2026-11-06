// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// briefRig is a composite of the weekly briefs over a real KV store and a real PostgreSQL store.
type briefRig struct {
	briefs *Briefs
	kv     natstest.Backend
	pg     *postgres.Store
	reader *sdkmetric.ManualReader
}

func newBriefRig(t *testing.T, mode Mode, wrapPG func(BriefBackend) BriefBackend, opts ...Option) *briefRig {
	t.Helper()
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)
	metrics, reader := newMetricsReader()
	var used BriefBackend = pg
	if wrapPG != nil {
		used = wrapPG(pg)
	}
	briefs, err := NewBriefs(mode, kv, used, append([]Option{WithMetrics(metrics)}, opts...)...)
	require.NoError(t, err)
	return &briefRig{briefs: briefs, kv: kv, pg: pg, reader: reader}
}

func (r *briefRig) counts(t *testing.T, name string) map[string]int64 {
	t.Helper()
	return countsOf(t, r.reader, name)
}

var briefWindow = model.GroupWeeklyBrief{WindowStart: storetest.FixtureWindow}

// sameBriefInBoth fails the test unless both stores hold the committee's brief for the window with the same revision and
// content, and the same throttle entry.
func sameBriefInBoth(t *testing.T, r *briefRig) {
	t.Helper()
	ctx := context.Background()
	kv, _, err := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	pg, _, err := r.pg.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	require.NotNil(t, kv)
	require.NotNil(t, pg, "PostgreSQL holds the brief")
	assert.Equal(t, kv.Revision, pg.Revision, "the revision is the one KV gave it")
	same, err := sameBrief(kv, pg)
	require.NoError(t, err)
	assert.True(t, same, "the brief is the same")
}

func sameThrottleInBoth(t *testing.T, r *briefRig) {
	t.Helper()
	ctx := context.Background()
	kv, err := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	pg, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	require.NotNil(t, kv)
	require.NotNil(t, pg, "PostgreSQL holds the throttle entry")
	same, err := sameThrottle(kv, pg)
	require.NoError(t, err)
	assert.True(t, same, "the throttle entry is the same, with the revision KV gave it")
}

// Whatever the mode, the composite behaves as one store does for the weekly briefs and their throttle entries: the same
// suite the KV store and the PostgreSQL store pass. In dual mode every read of the suite was compared, and the stores never
// disagreed, with one exception: the suite also puts a second brief for a window, which KV lets take the window over and
// PostgreSQL refuses. That copy is counted as a failure, and a read of that window then differs between the two stores,
// which is the difference docs/postgres-schema.md describes. Nothing else is counted.
// secondBriefSubtest is the contract case that puts a second brief for a window, the one place the stores differ.
const secondBriefSubtest = "a_second_brief_with_another_uid_for_a_window_that_has_one_leaves_the_window_with_one_brief"

func TestContract_Briefs(t *testing.T) {
	for _, mode := range []Mode{ModeKV, ModeDual, ModePostgres} {
		t.Run(string(mode), func(t *testing.T) {
			storetest.RunBriefContract(t, storetest.Backend{Name: "composite-" + string(mode)}, func(t *testing.T) storetest.BriefStore {
				r := newBriefRig(t, mode, nil, WithSampleRate(1))
				t.Cleanup(func() {
					assert.Empty(t, r.counts(t, compareErr))
					wantMismatches, wantFailures := map[string]int64{}, map[string]int64{}
					if mode == ModeDual && strings.HasSuffix(t.Name(), secondBriefSubtest) {
						wantFailures["put_weekly_brief"] = 1
						wantMismatches["get_weekly_brief"] = 1
					}
					assert.Equal(t, wantFailures, r.counts(t, failures), "only a second brief for a window is refused")
					assert.Equal(t, wantMismatches, r.counts(t, mismatches), "only the window of the second brief differs")
				})
				return r.briefs
			})
		})
	}
}

func TestBriefs_EachModeWritesToItsStores(t *testing.T) {
	ctx := context.Background()
	for mode, want := range map[Mode]struct{ kv, pg bool }{ModeKV: {true, false}, ModeDual: {true, true}, ModePostgres: {false, true}} {
		t.Run(string(mode), func(t *testing.T) {
			r := newBriefRig(t, mode, nil)

			_, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
			require.NoError(t, err)
			_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
			require.NoError(t, err)

			inKV, _, _ := r.kv.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
			inPG, _, _ := r.pg.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
			throttleKV, _ := r.kv.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
			throttlePG, _ := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
			assert.Equal(t, want.kv, inKV != nil && throttleKV != nil, "KV holds them")
			assert.Equal(t, want.pg, inPG != nil && throttlePG != nil, "PostgreSQL holds them")
		})
	}
}

func TestBriefs_DualWritesCarryTheKVRevisionToPostgreSQL(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDual, nil)

	brief, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	sameBriefInBoth(t, r)
	created := brief.Revision

	brief.BriefText = "changed"
	updated, err := r.briefs.PutGroupWeeklyBrief(ctx, brief) // with the revision it carries: a swap
	require.NoError(t, err)
	sameBriefInBoth(t, r)
	assert.Greater(t, updated.Revision, created, "a token from before the update is stale in both stores")

	throttle, err := r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	sameThrottleInBoth(t, r)
	throttle.GeneratesUsed++
	_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, throttle)
	require.NoError(t, err)
	sameThrottleInBoth(t, r)

	assert.Empty(t, r.counts(t, failures), "nothing failed on the way")
}

// failingBriefPG is a PostgreSQL store whose puts fail.
type failingBriefPG struct{ BriefBackend }

func (failingBriefPG) PutGroupWeeklyBrief(context.Context, *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	return nil, errPG
}

func (failingBriefPG) PutGroupWeeklyBriefThrottle(context.Context, *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	return nil, errPG
}

func TestBriefs_DualASecondaryFailureIsCountedAndNeverFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDual, func(pg BriefBackend) BriefBackend { return failingBriefPG{pg} })

	brief, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)

	assert.NotZero(t, brief.Revision, "KV decided")
	assert.Equal(t, map[string]int64{"put_weekly_brief": 1, "put_weekly_brief_throttle": 1}, r.counts(t, failures))
}

// flakyBriefPG is a PostgreSQL store whose puts fail while fail is set.
type flakyBriefPG struct {
	BriefBackend
	fail *atomic.Bool
}

func (f flakyBriefPG) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	if f.fail.Load() {
		return nil, errPG
	}
	return f.BriefBackend.PutGroupWeeklyBrief(ctx, brief)
}

func (f flakyBriefPG) PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	if f.fail.Load() {
		return nil, errPG
	}
	return f.BriefBackend.PutGroupWeeklyBriefThrottle(ctx, throttle)
}

// A copy that was missed is made up for by the next write of the same brief or entry, which copies what KV returned then.
func TestBriefs_DualTheNextPutBringsPostgreSQLUpToWhatKVHolds(t *testing.T) {
	ctx := context.Background()
	var fail atomic.Bool
	r := newBriefRig(t, ModeDual, func(pg BriefBackend) BriefBackend { return flakyBriefPG{pg, &fail} })
	brief, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	throttle, err := r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)

	fail.Store(true)
	brief.BriefText = "first"
	brief, err = r.briefs.PutGroupWeeklyBrief(ctx, brief)
	require.NoError(t, err)
	throttle.GeneratesUsed++
	throttle, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, throttle)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"put_weekly_brief": 1, "put_weekly_brief_throttle": 1}, r.counts(t, failures), "the copies were missed")

	fail.Store(false)
	brief.BriefText = "second"
	_, err = r.briefs.PutGroupWeeklyBrief(ctx, brief)
	require.NoError(t, err)
	throttle.GeneratesUsed++
	_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, throttle)
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{"put_weekly_brief": 1, "put_weekly_brief_throttle": 1}, r.counts(t, failures), "nothing more failed")
	sameBriefInBoth(t, r)
	sameThrottleInBoth(t, r)
}

// KV lets a second brief for a window take the window over, and PostgreSQL holds one brief per window, so it refuses the copy:
// the request succeeds and the copy is counted.
func TestBriefs_DualASecondBriefForAWindowIsRefusedByPostgreSQLAndCounted(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDual, nil)
	_, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)

	_, err = r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-2", "uid-1", storetest.FixtureWindow))

	require.NoError(t, err, "KV accepts it")
	assert.Equal(t, map[string]int64{"put_weekly_brief": 1}, r.counts(t, failures))
}

// A copy of what PostgreSQL already holds writes nothing, and a copy never goes back when PostgreSQL holds something newer.
func TestBriefs_CopiesAreIdempotentAndNeverGoBack(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDual, nil)
	brief, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	throttle, err := r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)

	require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow), "the same revision again")
	require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow), "the same revision again")
	sameBriefInBoth(t, r)
	sameThrottleInBoth(t, r)

	// PostgreSQL moves ahead of KV (a revision KV has not reached): the copy leaves it alone.
	ahead, _, err := r.pg.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	ahead.BriefText = "newer in PostgreSQL"
	wctx, err := postgres.WithRevision(ctx, brief.Revision+1000)
	require.NoError(t, err)
	ahead.Revision = 0
	_, err = r.pg.PutGroupWeeklyBrief(wctx, ahead)
	require.NoError(t, err)
	aheadThrottle, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	aheadThrottle.GeneratesUsed = 99
	wctx, err = postgres.WithRevision(ctx, throttle.Revision+1000)
	require.NoError(t, err)
	_, err = r.pg.PutGroupWeeklyBriefThrottle(wctx, aheadThrottle)
	require.NoError(t, err)

	require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow))
	require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))

	gotBrief, _, err := r.pg.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
	require.NoError(t, err)
	assert.Equal(t, "newer in PostgreSQL", gotBrief.BriefText)
	gotThrottle, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	assert.Equal(t, 99, gotThrottle.GeneratesUsed)
	assert.Empty(t, r.counts(t, failures))
}

// racingBriefPG makes the copy lose a race: right before its write goes through, another copy writes the same record.
type racingBriefPG struct {
	BriefBackend
	before func(context.Context)
}

func (p racingBriefPG) PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	if p.before != nil {
		p.before(ctx)
	}
	return p.BriefBackend.PutGroupWeeklyBrief(ctx, brief)
}

func (p racingBriefPG) PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	if p.before != nil {
		p.before(ctx)
	}
	return p.BriefBackend.PutGroupWeeklyBriefThrottle(ctx, throttle)
}

// When two copies of the same brief or entry overlap, PostgreSQL refuses the second one's revision as not above the first's;
// that is a copy that is already there, not a failure.
func TestBriefs_DualOverlappingCopiesAreNotFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("brief", func(t *testing.T) {
		var r *briefRig
		fired := false
		r = newBriefRig(t, ModeDual, func(pg BriefBackend) BriefBackend {
			return racingBriefPG{BriefBackend: pg, before: func(ctx context.Context) {
				if fired {
					return
				}
				fired = true
				require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow)) // the other copy
			}}
		})
		_, err := r.kv.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
		require.NoError(t, err)

		require.NoError(t, r.briefs.copyBrief(ctx, "uid-1", briefWindow))

		sameBriefInBoth(t, r)
	})

	t.Run("throttle", func(t *testing.T) {
		var r *briefRig
		fired := false
		r = newBriefRig(t, ModeDual, func(pg BriefBackend) BriefBackend {
			return racingBriefPG{BriefBackend: pg, before: func(ctx context.Context) {
				if fired {
					return
				}
				fired = true
				require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))
			}}
		})
		_, err := r.kv.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
		require.NoError(t, err)

		require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))

		sameThrottleInBoth(t, r)
	})
}

// Two puts of the same throttle entry whose copies run in the wrong order leave PostgreSQL on the newest, because a copy
// writes what KV holds when it runs and not what its put returned.
func TestBriefs_DualACopyWritesWhatKVHoldsNotWhatItsPutReturned(t *testing.T) {
	ctx := context.Background()
	r := newBriefRig(t, ModeDual, nil)
	first, err := r.kv.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
	require.NoError(t, err)
	first.GeneratesUsed = 7
	_, err = r.kv.PutGroupWeeklyBriefThrottle(ctx, first) // KV is now ahead of what the first put returned

	require.NoError(t, err)
	require.NoError(t, r.briefs.copyThrottle(ctx, "uid-1", storetest.FixtureWindow))

	sameThrottleInBoth(t, r)
	got, err := r.pg.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
	require.NoError(t, err)
	assert.Equal(t, 7, got.GeneratesUsed)
}

func TestBriefs_DualCompareCountsWhatDiffers(t *testing.T) {
	ctx := context.Background()

	t.Run("two stores that agree count nothing", func(t *testing.T) {
		r := newBriefRig(t, ModeDual, nil, WithSampleRate(1))
		_, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
		require.NoError(t, err)
		_, err = r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
		require.NoError(t, err)

		_, _, _ = r.briefs.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
		_, _ = r.briefs.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)
		_, _, _ = r.briefs.GetGroupWeeklyBriefForWindow(ctx, "uid-2", briefWindow) // a miss in both
		_, _ = r.briefs.GetGroupWeeklyBriefThrottle(ctx, "uid-2", storetest.FixtureWindow)

		assert.Empty(t, r.counts(t, mismatches))
		assert.Empty(t, r.counts(t, compareErr))
	})

	t.Run("a brief and an entry only KV has are mismatches", func(t *testing.T) {
		r := newBriefRig(t, ModeDual, nil, WithSampleRate(1))
		_, err := r.kv.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
		require.NoError(t, err)
		_, err = r.kv.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
		require.NoError(t, err)

		_, _, _ = r.briefs.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
		_, _ = r.briefs.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)

		assert.Equal(t, map[string]int64{"get_weekly_brief": 1, "get_weekly_brief_throttle": 1}, r.counts(t, mismatches))
	})

	t.Run("a revision that differs alone is a mismatch", func(t *testing.T) {
		r := newBriefRig(t, ModeDual, nil, WithSampleRate(1))
		brief, err := r.briefs.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
		require.NoError(t, err)
		throttle, err := r.briefs.PutGroupWeeklyBriefThrottle(ctx, storetest.NewThrottle("uid-1", storetest.FixtureWindow))
		require.NoError(t, err)
		sameContent := *brief
		sameContent.Revision = 0
		wctx, err := postgres.WithRevision(ctx, brief.Revision+1000)
		require.NoError(t, err)
		_, err = r.pg.PutGroupWeeklyBrief(wctx, &sameContent)
		require.NoError(t, err)
		sameThrottleContent := *throttle
		wctx, err = postgres.WithRevision(ctx, throttle.Revision+1000)
		require.NoError(t, err)
		_, err = r.pg.PutGroupWeeklyBriefThrottle(wctx, &sameThrottleContent)
		require.NoError(t, err)

		_, _, _ = r.briefs.GetGroupWeeklyBriefForWindow(ctx, "uid-1", briefWindow)
		_, _ = r.briefs.GetGroupWeeklyBriefThrottle(ctx, "uid-1", storetest.FixtureWindow)

		assert.Equal(t, map[string]int64{"get_weekly_brief": 1, "get_weekly_brief_throttle": 1}, r.counts(t, mismatches))
	})

	t.Run("the index keys are not compared", func(t *testing.T) {
		r := newBriefRig(t, ModeDual, nil, WithSampleRate(1))
		_, err := r.kv.PutGroupWeeklyBrief(ctx, storetest.NewBrief("b-1", "uid-1", storetest.FixtureWindow))
		require.NoError(t, err)

		keys, err := r.briefs.ListGroupWeeklyBriefIndexKeys(ctx)

		require.NoError(t, err)
		assert.Len(t, keys, 1)
		assert.Empty(t, r.counts(t, mismatches), "a whole-set read is not made twice on a request")
	})
}

func TestBriefs_Defaults(t *testing.T) {
	kv, pg := natstest.NewBackend(t), postgrestest.NewStore(t)

	b, err := NewBriefs(ModeDual, kv, pg)

	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, b.writeTimeout)
	_, err = NewBriefs(ModeDual, kv, nil)
	assert.Error(t, err)
	_, err = NewBriefs(ModeDualPG, kv, pg)
	assert.Error(t, err)
}
