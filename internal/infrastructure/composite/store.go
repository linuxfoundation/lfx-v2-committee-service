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
	// defaultWriteTimeout bounds the write to the secondary store after the primary store's write succeeded.
	defaultWriteTimeout = 10 * time.Second
)

// Store runs the NATS KV store and the PostgreSQL store side by side and is chosen by Mode. The primary store, the
// one the mode makes authoritative, decides every result; the Store embeds its aggregate, so every method that is not
// overridden goes to it unchanged. The methods that are overridden add what the secondary store needs:
//
//   - kv: the KV store only.
//   - dual: KV decides. After a write succeeds, the record is read back from KV, with the revision KV gave it, and
//     written to PostgreSQL with that revision, so a token a client holds means the same in both stores. A failed
//     write to PostgreSQL is logged and counted and never fails the request: it is left to storage migrate, which
//     repairs it, and storage verify, which finds it. A sampled fraction of reads is also made against PostgreSQL and
//     compared, the KV result being the one returned.
//   - postgres: the PostgreSQL store only.
//
// There is no transaction across the two stores. Only the committees and their settings are composed so far; the other
// records go to the primary store alone, so the Store is not complete enough to be wired in yet.
type Store struct {
	port.CommitteeReaderWriter

	mode           Mode
	kv, pg         port.CommitteeReaderWriter
	metrics        *Metrics
	rate           float64
	random         func() float64
	compareTimeout time.Duration
	writeTimeout   time.Duration
}

var _ port.CommitteeReaderWriter = (*Store)(nil)

// Option tunes a Store.
type Option func(*Store)

// WithMetrics sets the metrics that count what the secondary store does. Without it nothing is counted, and
// everything is still logged.
func WithMetrics(m *Metrics) Option { return func(s *Store) { s.metrics = m } }

// WithSampleRate sets the fraction of reads in dual mode that are also compared with the secondary store, from 0 (none)
// to 1 (all). The default is 0.01.
func WithSampleRate(rate float64) Option { return func(s *Store) { s.rate = min(max(rate, 0), 1) } }

// WithRandom sets where the sampling draws from, a number in [0, 1), so a test can decide which reads are sampled.
func WithRandom(random func() float64) Option { return func(s *Store) { s.random = random } }

// WithTimeouts bounds the extra read of a sampled request and the write to the secondary store. A value that is not
// positive keeps the default.
func WithTimeouts(compare, write time.Duration) Option {
	return func(s *Store) {
		if compare > 0 {
			s.compareTimeout = compare
		}
		if write > 0 {
			s.writeTimeout = write
		}
	}
}

// New returns the composite for the mode. kv is needed by every mode that uses KV (kv and dual) and pg by every mode
// that uses PostgreSQL (dual and postgres); the one a mode does not use may be nil. dual-pg is not available yet.
func New(mode Mode, kv, pg port.CommitteeReaderWriter, opts ...Option) (*Store, error) {
	s := &Store{
		mode: mode, kv: kv, pg: pg,
		rate: defaultSampleRate, random: rand.Float64,
		compareTimeout: defaultCompareTimeout, writeTimeout: defaultWriteTimeout,
	}
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
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// sampled decides whether this read is also made against the secondary store. Only dual mode has one.
func (s *Store) sampled() bool {
	if s.mode != ModeDual || s.rate <= 0 {
		return false
	}
	return s.rate >= 1 || s.random() < s.rate
}

// replicate runs a write to the secondary store after the primary store's write succeeded. It never fails the request:
// an error is logged and counted. It does not use the request's cancellation, because the primary store's write has
// already happened and a client that went away must not leave the secondary store behind for that reason.
func (s *Store) replicate(ctx context.Context, operation string, write func(ctx context.Context) error) {
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
		s.metrics.SecondaryWriteFailed(ctx, s.mode, "postgres", operation, err)
	}
}

func isNotFound(err error) bool {
	var notFound errs.NotFound
	return errors.As(err, &notFound)
}
