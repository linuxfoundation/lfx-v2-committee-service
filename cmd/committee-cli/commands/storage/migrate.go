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

// The directions of a migration.
const (
	directionKVToPG = "kv-to-pg"
	directionPGToKV = "pg-to-kv"
)

// migrateSubcommand copies the committee data between the KV store and PostgreSQL: into PostgreSQL by default, and
// back into KV with --direction pg-to-kv.
type migrateSubcommand struct {
	// out receives the summary; os.Stdout when nil.
	out io.Writer
}

func (s *migrateSubcommand) Name() string { return "migrate" }

func (s *migrateSubcommand) Help() string {
	return "copy the committee data from the NATS KV store to PostgreSQL, keeping the revisions, or with --direction pg-to-kv back from PostgreSQL to KV"
}

func (s *migrateSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli storage migrate [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	direction := fs.String("direction", directionKVToPG, "kv-to-pg copies KV into PostgreSQL, keeping the revisions; pg-to-kv copies PostgreSQL into KV, which gives each record a revision of its own, and replaces a record KV holds with other content; neither deletes")
	entities := fs.String("entity", "", "comma-separated entities to copy ("+strings.Join(allEntities, ", ")+"); default all")
	dryRun := fs.Bool("dry-run", false, "read and classify everything but write nothing (to PostgreSQL, or with --direction pg-to-kv to KV)")
	includeOrphans := fs.Bool("include-orphans", false, "also copy children whose committee does not exist in the source store (KV, or with --direction pg-to-kv PostgreSQL); they are only reported by default")
	workers := fs.Int("workers", 4, "records written concurrently (to PostgreSQL, or with --direction pg-to-kv to KV)")
	batchSize := fs.Int("batch-size", 100, "records read ahead of the workers, which bounds the memory the records use (the committee uids are also held, for the orphan check)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	// The flag parser stops at the first argument that is not a flag, so a flag after a stray word is never
	// read: "migrate members --dry-run" would run live, over everything. Reject it before anything connects.
	if fs.NArg() > 0 {
		return errs.NewValidation(fmt.Sprintf("unexpected argument %q: options are flags, for example --entity=members --dry-run", fs.Arg(0)))
	}
	if *workers <= 0 || *batchSize <= 0 {
		return errs.NewValidation("--workers and --batch-size must be greater than zero")
	}
	if *direction != directionKVToPG && *direction != directionPGToKV {
		return errs.NewValidation(fmt.Sprintf("unknown direction %q: use %s or %s", *direction, directionKVToPG, directionPGToKV))
	}
	reverse := *direction == directionPGToKV
	if reverse && (rc.KVRepair == nil || rc.PostgresStore == nil) {
		return errs.NewUnexpected("the KV store and the PostgreSQL store are not wired in RunContext")
	}
	if !reverse && (rc.KVExport == nil || rc.PostgresStore == nil) {
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

	if reverse {
		results, runErr := runReverse(ctx, store, rc.KVRepair, opts)
		if results != nil {
			s.print(results, *dryRun, true)
		}
		return runErr
	}
	results, runErr := runBackfill(ctx, rc.KVExport, store, opts)
	if results != nil {
		s.print(results, *dryRun, false)
	}
	return runErr
}

// print writes the summary: one line per entity, the problems with their uids, and the highest revision.
func (s *migrateSubcommand) print(results []backfillResult, dryRun, reverse bool) {
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
	if reverse {
		_, _ = fmt.Fprintln(out, "nothing was deleted: what only KV holds stays; check the copy with storage verify --by-content")
		return
	}
	_, _ = fmt.Fprintf(out, "highest KV revision seen: %d (advance the PostgreSQL revision sequence past the highest over every bucket at the flip)\n", highest)
}
