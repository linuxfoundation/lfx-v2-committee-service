// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// The entities a run can copy. Committees come with their settings, which are reported as a line of their own.
const (
	entityCommittees = "committees"
	entityMembers    = "members"
)

// resultSettings is the report line of the settings copied along with the committees.
const resultSettings = "settings"

// allEntities lists the entities a run copies by default, in the order it copies them.
var allEntities = []string{entityCommittees, entityMembers}

// backfillSink is the PostgreSQL store as the backfill uses it. *postgres.Store implements it.
type backfillSink interface {
	GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
	GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error)
	Create(ctx context.Context, committee *model.Committee) error
	UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error
	UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error
	GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error)
	CreateMember(ctx context.Context, member *model.CommitteeMember) error
	UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error)
}

var _ backfillSink = (*postgres.Store)(nil)

// backfillOptions tune a run.
type backfillOptions struct {
	// Entities to copy; empty means allEntities.
	Entities []string
	// DryRun reads and classifies everything and writes nothing: the written count is what would be written.
	DryRun bool
	// IncludeOrphans copies children whose committee does not exist in KV. By default they are only reported.
	IncludeOrphans bool
	// Workers write concurrently; zero means 4.
	Workers int
	// BatchSize is how many records are read ahead of the workers, which bounds the memory the records use; the uid of
	// every committee is also held, for the orphan check. Zero means 100.
	BatchSize int
	// ProgressEvery logs progress after this many records of an entity; zero means 1000.
	ProgressEvery int64
}

// maxProblems caps the problems a result keeps; the counts stay exact and every problem is logged.
const maxProblems = 1000

// backfillProblem is a record that was not copied, or a scan that was not complete.
type backfillProblem struct {
	UID     string
	Kind    string // read, source, constraint, invalid, write, ahead, missing, orphan
	Message string
}

// backfillResult is what a run did for one entity.
type backfillResult struct {
	Entity    string
	Scanned   int64
	Written   int64
	Unchanged int64
	Orphans   int64
	Errors    int64
	// HighestRevision is the highest KV revision seen. The revision sequence has to be advanced past the
	// highest revision over every KV bucket at the flip to PostgreSQL.
	HighestRevision uint64
	Problems        []backfillProblem
}

// tracker keeps the result of one entity while workers update it.
type tracker struct {
	mu            sync.Mutex
	res           backfillResult
	progressEvery int64
}

func (t *tracker) scanned(ctx context.Context, revision uint64) {
	t.mu.Lock()
	t.res.Scanned++
	t.res.HighestRevision = max(t.res.HighestRevision, revision)
	n := t.res.Scanned
	t.mu.Unlock()
	if n%t.progressEvery == 0 {
		slog.InfoContext(ctx, "backfill progress", "entity", t.res.Entity, "scanned", n)
	}
}

func (t *tracker) written()   { t.mu.Lock(); t.res.Written++; t.mu.Unlock() }
func (t *tracker) unchanged() { t.mu.Lock(); t.res.Unchanged++; t.mu.Unlock() }

func (t *tracker) orphan(ctx context.Context, uid string) {
	t.mu.Lock()
	t.res.Orphans++
	t.keep(backfillProblem{UID: uid, Kind: "orphan", Message: "its committee does not exist in KV"})
	t.mu.Unlock()
	slog.WarnContext(ctx, "backfill skipped an orphan", "entity", t.res.Entity, "uid", uid)
}

func (t *tracker) problem(ctx context.Context, uid, kind, message string) {
	t.mu.Lock()
	t.res.Errors++
	t.keep(backfillProblem{UID: uid, Kind: kind, Message: message})
	t.mu.Unlock()
	slog.WarnContext(ctx, "backfill problem", "entity", t.res.Entity, "uid", uid, "kind", kind, "message", message)
}

// keep records the problem unless the cap is reached. The caller holds the lock.
func (t *tracker) keep(p backfillProblem) {
	if len(t.res.Problems) < maxProblems {
		t.res.Problems = append(t.res.Problems, p)
	}
}

// writeKind names the kind of problem a failed write is.
func writeKind(err error) string {
	var conflict errs.Conflict
	var validation errs.Validation
	switch {
	case errors.As(err, &conflict):
		return "constraint"
	case errors.As(err, &validation):
		return "invalid"
	default:
		return "write"
	}
}

func isNotFound(err error) bool {
	var notFound errs.NotFound
	return errors.As(err, &notFound)
}

// stream reads items from produce into a buffer of batch items and handles them on workers goroutines. It
// returns what produce returns once every item taken from the buffer has been handled.
func stream[T any](ctx context.Context, workers, batch int, produce func(emit func(T) error) error, handle func(T)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	items := make(chan T, batch)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range items {
				handle(item)
			}
		}()
	}
	err := produce(func(item T) error {
		select {
		case items <- item:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	close(items)
	wg.Wait()
	return err
}

// validateEntities checks the names against allEntities, so a typo is found before anything connects.
func validateEntities(entities []string) error {
	for _, entity := range entities {
		if !slices.Contains(allEntities, entity) {
			return errs.NewValidation(fmt.Sprintf("unknown entity %q: use one of %v", entity, allEntities))
		}
	}
	return nil
}

// runBackfill copies committee data from the KV store into PostgreSQL. It reads each record with the revision
// it has in KV and writes the same revision into the row, never deletes anything, and can be run again: a
// record whose revision already matches is left alone. It returns one result per entity, with the settings
// of the committees as a line of their own. The error is non-nil when anything could not be copied, so the
// command can exit non-zero; the results are returned either way.
func runBackfill(ctx context.Context, src port.KVExportReader, sink backfillSink, opts backfillOptions) ([]backfillResult, error) {
	if len(opts.Entities) == 0 {
		opts.Entities = allEntities
	}
	if err := validateEntities(opts.Entities); err != nil {
		return nil, err
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 1000
	}
	newTracker := func(entity string) *tracker {
		return &tracker{res: backfillResult{Entity: entity}, progressEvery: opts.ProgressEvery}
	}

	var trackers []*tracker
	if slices.Contains(opts.Entities, entityCommittees) {
		committees, settings := newTracker(entityCommittees), newTracker(resultSettings)
		trackers = append(trackers, committees, settings)
		scanError(ctx, committees, copyCommittees(ctx, src, sink, opts, committees, settings))
	}
	if slices.Contains(opts.Entities, entityMembers) {
		members := newTracker(entityMembers)
		trackers = append(trackers, members)
		scanError(ctx, members, copyMembers(ctx, src, sink, opts, members))
	}

	results := make([]backfillResult, 0, len(trackers))
	total := int64(0)
	for _, t := range trackers {
		results = append(results, t.res)
		total += t.res.Errors
		slog.InfoContext(ctx, "backfill summary", "entity", t.res.Entity, "scanned", t.res.Scanned, "written", t.res.Written,
			"unchanged", t.res.Unchanged, "orphans", t.res.Orphans, "errors", t.res.Errors,
			"highest_revision", t.res.HighestRevision, "dry_run", opts.DryRun)
	}
	if total > 0 {
		return results, fmt.Errorf("%d records could not be copied or read", total)
	}
	return results, nil
}

// scanError records an error that ended a scan as a problem of the entity.
func scanError(ctx context.Context, t *tracker, err error) {
	if err != nil {
		t.problem(ctx, "", "source", err.Error())
	}
}
