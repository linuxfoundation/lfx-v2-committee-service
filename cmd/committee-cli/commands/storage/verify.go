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

// verifySubcommand compares the committee data in the KV store with the data in PostgreSQL.
type verifySubcommand struct {
	// out receives the summary; os.Stdout when nil.
	out io.Writer
}

func (s *verifySubcommand) Name() string { return "verify" }

func (s *verifySubcommand) Help() string {
	return "compare the committee data in the NATS KV store with PostgreSQL (read-only; exits non-zero on any difference)"
}

func (s *verifySubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli storage verify [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	entities := fs.String("entity", "", "comma-separated entities to compare ("+strings.Join(allEntities, ", ")+"); default all")
	includeOrphans := fs.Bool("include-orphans", false, "expect the children whose committee does not exist in KV to have been copied (migrate --include-orphans); they are only counted by default")
	byContent := fs.Bool("by-content", false, "compare the content of each record and not its revision: for dual-pg mode, where KV holds each record at a revision of its own (a weekly brief is compared without its update time too)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return errs.NewValidation(fmt.Sprintf("unexpected argument %q: options are flags, for example --entity=members", fs.Arg(0)))
	}
	if rc.KVExport == nil || rc.PostgresStore == nil {
		return errs.NewUnexpected("the KV export and the PostgreSQL store are not wired in RunContext")
	}

	opts := verifyOptions{IncludeOrphans: *includeOrphans, ByContent: *byContent}
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

	results, runErr := runVerify(ctx, rc.KVExport, store, opts)
	if results != nil {
		s.print(results)
	}
	return runErr
}

// print writes the summary: one line per entity, then what differs and the orphans with their uids.
func (s *verifySubcommand) print(results []verifyResult) {
	out := s.out
	if out == nil {
		out = os.Stdout
	}
	var differences int64
	for _, r := range results {
		_, _ = fmt.Fprintf(out, "%-12s kv=%d postgresql=%d matched=%d missing=%d extra=%d mismatched=%d errors=%d orphans(kv)=%d orphans(postgresql)=%d\n",
			r.Entity, r.KV, r.PostgreSQL, r.Matched, r.Missing, r.Extra, r.Mismatched, r.Errors, r.KVOrphans, r.PostgreSQLOrphans)
		differences += r.differences()
	}
	// The differences of every entity come first, then the orphans, so that the orphans a run usually finds cannot
	// push the differences, which decide the exit code, out of the lines that are listed.
	printed := 0
problems:
	for _, orphans := range []bool{false, true} {
		for _, r := range results {
			for _, p := range r.Problems {
				if isOrphan(p.Kind) != orphans {
					continue
				}
				if printed == maxPrintedProblems {
					_, _ = fmt.Fprintln(out, "... more are in the log")
					break problems
				}
				_, _ = fmt.Fprintf(out, "  %s %s uid=%s: %s\n", r.Entity, p.Kind, p.UID, p.Message)
				printed++
			}
		}
	}
	if differences == 0 {
		_, _ = fmt.Fprintln(out, "no differences between KV and PostgreSQL")
	}
}
