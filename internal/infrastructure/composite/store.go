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
//   - postgres: the PostgreSQL store only.
//
// There is no transaction across the two stores. A write the primary store only partly made, and so refused, is not
// copied. The committees, their settings, the members, the invites and the applications are composed, which is the whole
// of port.CommitteeReaderWriter apart from the streams EachMember and EachMemberStrict, which go to the primary store
// alone. The links and folders (Links), the weekly briefs and throttle entries (Briefs) and the documents (Documents, whose
// files stay in the Object Store in every mode) are other aggregates with stores of their own, composed the same way.
// The Store does not expose port.CommitteeCascadeDeleter, which in postgres mode would leave the children of a deleted
// committee behind. None of them is complete enough to be wired in until the cascade is
// exposed for the stores that have it, which is why no mode but kv is available at startup.
type Store struct {
	port.CommitteeReaderWriter

	core
	kv, pg port.CommitteeReaderWriter
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
// that uses PostgreSQL (dual and postgres); the one a mode does not use may be nil. dual-pg is not available yet.
func New(mode Mode, kv, pg port.CommitteeReaderWriter, opts ...Option) (*Store, error) {
	s := &Store{core: newCore(mode, opts), kv: kv, pg: pg}
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
	case ModePostgres:
		if pg == nil {
			return nil, errors.New("storage mode postgres needs the PostgreSQL store")
		}
		s.CommitteeReaderWriter = pg
	default:
		return nil, fmt.Errorf("storage mode %q is not available in the composite store yet", mode)
	}
	return s, nil
}

// selectPrimary checks that the stores the mode needs are there and returns the primary one: KV for kv and dual,
// PostgreSQL for postgres. dual-pg is not available yet.
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
	case ModePostgres:
		if pg == none {
			return none, errors.New("storage mode postgres needs the PostgreSQL store")
		}
		return pg, nil
	}
	return none, fmt.Errorf("storage mode %q is not available in the composite store yet", mode)
}

// sampled decides whether this read is also made against the secondary store. Only dual mode has one.
func (s *core) sampled() bool {
	if s.mode != ModeDual || s.rate <= 0 {
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
		s.metrics.SecondaryWriteFailed(ctx, s.mode, "postgres", operation, uid, err)
	}
}

func isNotFound(err error) bool {
	var notFound errs.NotFound
	return errors.As(err, &notFound)
}
