// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// maxPrintedProblems bounds the problems the summary lists; every one is in the log.
const maxPrintedProblems = 100

// migrateSubcommand copies the committee data from the KV store into PostgreSQL.
type migrateSubcommand struct {
	// out receives the summary; os.Stdout when nil.
	out io.Writer
}

func (s *migrateSubcommand) Name() string { return "migrate" }

func (s *migrateSubcommand) Help() string {
	return "copy committees, settings and members from the NATS KV store to PostgreSQL, keeping their revisions"
}

func (s *migrateSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli storage migrate [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	entities := fs.String("entity", "", "comma-separated entities to copy ("+strings.Join(allEntities, ", ")+"); default all")
	dryRun := fs.Bool("dry-run", false, "read and classify everything but write nothing to PostgreSQL")
	includeOrphans := fs.Bool("include-orphans", false, "also copy children whose committee does not exist in KV; they are only reported by default")
	workers := fs.Int("workers", 4, "records written to PostgreSQL concurrently")
	batchSize := fs.Int("batch-size", 100, "records read ahead of the workers, which bounds the memory used")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *workers <= 0 || *batchSize <= 0 {
		return errs.NewValidation("--workers and --batch-size must be greater than zero")
	}
	if rc.KVExport == nil || rc.PostgresStore == nil {
		return errs.NewUnexpected("the KV export and the PostgreSQL store are not wired in RunContext")
	}

	opts := backfillOptions{DryRun: *dryRun, IncludeOrphans: *includeOrphans, Workers: *workers, BatchSize: *batchSize}
	if *entities != "" {
		for _, entity := range strings.Split(*entities, ",") {
			opts.Entities = append(opts.Entities, strings.TrimSpace(entity))
		}
		if err := validateEntities(opts.Entities); err != nil {
			return err
		}
	}

	store, closeStore, err := rc.PostgresStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	results, runErr := runBackfill(ctx, rc.KVExport, store, opts)
	if results != nil {
		s.print(results, *dryRun)
	}
	return runErr
}

// print writes the summary: one line per entity, the problems with their uids, and the highest revision.
func (s *migrateSubcommand) print(results []backfillResult, dryRun bool) {
	out := s.out
	if out == nil {
		out = os.Stdout
	}
	if dryRun {
		_, _ = fmt.Fprintln(out, "dry run: nothing was written; \"written\" is what a run would write")
	}
	var highest uint64
	for _, r := range results {
		_, _ = fmt.Fprintf(out, "%-10s scanned=%d written=%d unchanged=%d orphans=%d errors=%d\n",
			r.Entity, r.Scanned, r.Written, r.Unchanged, r.Orphans, r.Errors)
		highest = max(highest, r.HighestRevision)
	}
	printed := 0
	for _, r := range results {
		for _, p := range r.Problems {
			if printed == maxPrintedProblems {
				_, _ = fmt.Fprintln(out, "... more problems are in the log")
				break
			}
			_, _ = fmt.Fprintf(out, "  %s %s uid=%s: %s\n", r.Entity, p.Kind, p.UID, p.Message)
			printed++
		}
	}
	_, _ = fmt.Fprintf(out, "highest KV revision seen: %d (advance the PostgreSQL revision sequence past the highest over every bucket at the flip)\n", highest)
}
