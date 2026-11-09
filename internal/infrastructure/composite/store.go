// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/copybudget"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	// defaultSampleRate is the fraction of reads in dual mode that are also made against the secondary store.
	defaultSampleRate = 0.01
	// defaultCompareTimeout bounds the extra read of a sampled request, so a slow secondary store costs a sampled
	// read at most this long.
	defaultCompareTimeout = 2 * time.Second
	// defaultWriteTimeout bounds the write to the secondary store after the primary store's write succeeded. The write is
	// made on the request's path, so this is the most a degraded secondary store adds to a write.
	defaultWriteTimeout = 3 * time.Second
)

// Store runs the NATS KV store and the PostgreSQL store side by side and is chosen by Mode. The primary store, the
// one the mode makes authoritative, decides every result; the Store embeds its aggregate, so every method that is not
// overridden goes to it unchanged. The methods that are overridden add what the secondary store needs:
//
//   - kv: the KV store only.
//   - dual: KV decides. After a write succeeds, the record is read back from KV, with the revision KV gave it, and
//     written to PostgreSQL with that revision, so a token a client holds means the same in both stores. A failed
//     write to PostgreSQL is logged and counted and never fails the request, and it is found by storage verify and
//     brought over again by storage migrate, which copies what is missing or behind; the next update of the same
//     committee also brings PostgreSQL up to what KV holds. The copy is made on the request's path, so a degraded
//     PostgreSQL adds up to the write timeout to a write, which is why that is short. A delete is converged the same
//     way: if PostgreSQL is behind, the row is removed under the revision it has. What storage migrate cannot do is
//     delete, so a row that a delete still could not remove is reported as extra by storage verify and has to be
//     removed by hand; before dual mode is turned on, storage verify must report no extra record. A sampled fraction of reads is also made against PostgreSQL and compared, the KV result being
//     the one returned; a write that is being copied while the read is made can show as a mismatch.
//   - dual-pg: PostgreSQL decides, the mirror of dual. After a write succeeds, the record is read back from PostgreSQL and
//     written to KV. KV assigns revisions of its own and cannot be given the one PostgreSQL gave, so the token a client
//     holds is PostgreSQL's and the copy in KV is at another revision: a write to KV is guarded by the revision KV has, is
//     skipped when KV already holds the same content, and a delete is guarded by the revision KV has too. A failed copy to
//     KV is logged and counted (store "kv") and never fails the request, and the next update of the same record brings KV up
//     to what PostgreSQL holds. Reads are compared with KV by content alone. KV stamps the update time of a weekly brief
//     itself, so a brief in KV carries the time of its copy, and briefs are compared without it. A client that holds a token
//     from before a switch back to kv gets a revision mismatch, or, when the number happens to be KV's own, writes over what
//     it did not read; the switch is to be made with no writes in flight (docs/postgres-schema.md).
//   - postgres: the PostgreSQL store only.
//
// There is no transaction across the two stores. A write the primary store only partly made, and so refused, is not
// copied. The committees, their settings, the members, the invites and the applications are composed, which is the whole
// of port.CommitteeReaderWriter apart from the streams EachMember and EachMemberStrict, which go to the primary store
// alone. The links and folders (Links), the weekly briefs and throttle entries (Briefs) and the documents (Documents, whose
// files stay in the Object Store in every mode) are other aggregates with stores of their own, composed the same way.
// The Store does not itself have the cascading delete of a committee, port.CommitteeCascadeDeleter, which the service uses
// in place of Delete when its writer has it: a mode in which PostgreSQL is the primary store would otherwise leave the
// children of a deleted committee behind there, and kv and dual keep the committee delete the service has always had. Where
// PostgreSQL is the primary store, WithCascade returns a CascadingStore that has it. The API and the CLI build the stores of
// the mode in package stores; in kv mode that is the KV storage itself, with no composite in between.
type Store struct {
	port.CommitteeReaderWriter

	core
	// src is the primary store, which the copy to the secondary store reads from, and dst the secondary store it writes.
	// In dual mode they are KV and PostgreSQL; in dual-pg mode, PostgreSQL and KV.
	src, dst port.CommitteeReaderWriter
	// alsoReady is the KV store where PostgreSQL is the primary store: its readiness is the primary store's, which is
	// PostgreSQL's, and the service also needs NATS (the files of documents, the indexer and access messages), which only
	// the KV store reports. Nil where KV is the primary store, whose readiness is NATS's.
	alsoReady interface {
		IsReady(ctx context.Context) error
	}
}

// core is what the composites of every aggregate share: the mode, the metrics, the sampling and the timeouts, and what
// follows from them.
type core struct {
	mode           Mode
	metrics        *Metrics
	rate           float64
	random         func() float64
	compareTimeout time.Duration
	writeTimeout   time.Duration
}

func newCore(mode Mode, opts []Option) core {
	c := core{
		mode: mode, rate: defaultSampleRate, random: rand.Float64,
		compareTimeout: defaultCompareTimeout, writeTimeout: defaultWriteTimeout,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

var _ port.CommitteeReaderWriter = (*Store)(nil)

// Option tunes a composite store.
type Option func(*core)

// WithMetrics sets the metrics that count what the secondary store does. Without it nothing is counted, and
// everything is still logged.
func WithMetrics(m *Metrics) Option { return func(c *core) { c.metrics = m } }

// WithSampleRate sets the fraction of reads in dual mode that are also compared with the secondary store, from 0 (none)
// to 1 (all). The default is 0.01.
func WithSampleRate(rate float64) Option { return func(c *core) { c.rate = min(max(rate, 0), 1) } }

// WithRandom sets where the sampling draws from, a number in [0, 1), so a test can decide which reads are sampled.
func WithRandom(random func() float64) Option { return func(c *core) { c.random = random } }

// WithTimeouts bounds the extra read of a sampled request and the write to the secondary store. A value that is not
// positive keeps the default.
func WithTimeouts(compare, write time.Duration) Option {
	return func(c *core) {
		if compare > 0 {
			c.compareTimeout = compare
		}
		if write > 0 {
			c.writeTimeout = write
		}
	}
}

// New returns the composite for the mode. kv is needed by every mode that uses KV (kv and dual) and pg by every mode
// that uses PostgreSQL (dual, dual-pg and postgres); the one a mode does not use may be nil.
func New(mode Mode, kv, pg port.CommitteeReaderWriter, opts ...Option) (*Store, error) {
	s := &Store{core: newCore(mode, opts), src: kv, dst: pg}
	switch mode {
	case ModeKV:
		if kv == nil {
			return nil, errors.New("storage mode kv needs the KV store")
		}
		s.CommitteeReaderWriter = kv
	case ModeDual:
		if kv == nil || pg == nil {
			return nil, errors.New("storage mode dual needs the KV store and the PostgreSQL store")
		}
		s.CommitteeReaderWriter = kv
	case ModeDualPG:
		if kv == nil || pg == nil {
			return nil, errors.New("storage mode dual-pg needs the KV store and the PostgreSQL store")
		}
		s.CommitteeReaderWriter = pg
		s.src, s.dst = pg, kv
	case ModePostgres:
		if pg == nil {
			return nil, errors.New("storage mode postgres needs the PostgreSQL store")
		}
		s.CommitteeReaderWriter = pg
	default:
		return nil, fmt.Errorf("storage mode %q is not available in the composite store yet", mode)
	}
	if mode.PostgresIsPrimary() && kv != nil {
		s.alsoReady = kv
	}
	return s, nil
}

// IsReady reports whether the store the service depends on is ready: the primary store's readiness, and where that is
// PostgreSQL, NATS's too, which the primary store does not report. In kv and dual mode it is the KV store's, as it has always
// been, and a PostgreSQL that is down is not a reason to leave rotation while KV decides.
func (s *Store) IsReady(ctx context.Context) error {
	if err := s.CommitteeReaderWriter.IsReady(ctx); err != nil {
		return err
	}
	if s.alsoReady != nil {
		return s.alsoReady.IsReady(ctx)
	}
	return nil
}

// selectPrimary checks that the stores the mode needs are there and returns the primary one: KV for kv and dual,
// PostgreSQL for dual-pg and postgres.
func selectPrimary[T comparable](mode Mode, kv, pg T) (T, error) {
	var none T
	switch mode {
	case ModeKV:
		if kv == none {
			return none, errors.New("storage mode kv needs the KV store")
		}
		return kv, nil
	case ModeDual:
		if kv == none || pg == none {
			return none, errors.New("storage mode dual needs the KV store and the PostgreSQL store")
		}
		return kv, nil
	case ModeDualPG:
		if kv == none || pg == none {
			return none, errors.New("storage mode dual-pg needs the KV store and the PostgreSQL store")
		}
		return pg, nil
	case ModePostgres:
		if pg == none {
			return none, errors.New("storage mode postgres needs the PostgreSQL store")
		}
		return pg, nil
	}
	return none, fmt.Errorf("storage mode %q is not available in the composite store yet", mode)
}

// sampled decides whether this read is also made against the secondary store. Only the modes that write both have one.
func (s *core) sampled() bool {
	if !s.mode.HasSecondary() || s.rate <= 0 {
		return false
	}
	return s.rate >= 1 || s.random() < s.rate
}

// replicate runs a write to the secondary store after the primary store's write succeeded. It never fails the request:
// an error is logged, with the uid of the record, and counted. It does not use the request's cancellation, because the primary store's write has
// already happened and a client that went away must not leave the secondary store behind for that reason.
func (s *core) replicate(ctx context.Context, operation, uid string, write func(ctx context.Context) error) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.writeTimeout)
	defer cancel()
	// The NATS adapter detaches its rollback and cleanup from the deadline it is given; the budget tells it how much of
	// this copy's deadline is left, so a stalled KV does not make the request wait for a second timeout.
	wctx = copybudget.With(wctx, time.Now().Add(s.writeTimeout))
	// A panic in the secondary store is a failure of the secondary store like any other: it must not take down a request
	// whose result the primary store has already decided.
	err := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("the secondary store panicked: %v", recovered)
			}
		}()
		return write(wctx)
	}()
	if err != nil {
		s.metrics.SecondaryWriteFailed(ctx, s.mode, s.secondaryName(), operation, uid, err)
	}
}

// secondaryIsPostgres reports whether the secondary store is PostgreSQL, as in dual mode, which is the one that can be
// given the revision the primary store gave. In dual-pg mode the secondary store is KV, which assigns its own revisions: a
// copy there is guarded by the revision KV has, is skipped when the content is already there, and is compared by content
// alone, because the two stores hold the same record at different revisions and the token a client holds is the primary's.
func (s *core) secondaryIsPostgres() bool { return s.mode == ModeDual }

// secondaryName is the store the metrics name for a failed copy.
func (s *core) secondaryName() string {
	if s.mode == ModeDualPG {
		return "kv"
	}
	return "postgres"
}

// carry makes the context the secondary store is written with: in dual mode one that gives the record the revision of the
// primary store, in dual-pg mode the context unchanged, since KV cannot be given one.
func (s *core) carry(ctx context.Context, revision uint64) (context.Context, error) {
	if !s.secondaryIsPostgres() {
		return ctx, nil
	}
	return postgres.WithRevision(ctx, revision)
}

// sameRevision reports whether the two stores' revisions of a record agree, which only means something in dual mode.
func (s *core) sameRevision(primary, secondary uint64) bool {
	return !s.secondaryIsPostgres() || primary == secondary
}

func isNotFound(err error) bool {
	var notFound errs.NotFound
	return errors.As(err, &notFound)
}
