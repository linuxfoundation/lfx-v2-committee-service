// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// flipTarget is the PostgreSQL store as flip uses it: what verify reads, and the revision sequence.
// *postgres.Store implements it.
type flipTarget interface {
	verifySource
	AdvanceRevisionSequence(ctx context.Context, atLeast uint64) error
}

// flipSubcommand makes PostgreSQL ready to be the store that issues the revisions.
type flipSubcommand struct {
	// out receives the summary; os.Stdout when nil.
	out io.Writer
}

func (s *flipSubcommand) Name() string { return "flip" }

func (s *flipSubcommand) Help() string {
	return "advance the PostgreSQL revision sequence above every KV revision, so that PostgreSQL can issue them (dry run unless --confirm)"
}

func (s *flipSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet("flip", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli storage flip [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	confirm := fs.Bool("confirm", false, "advance the sequence; without it nothing is changed and the command only says what it would do")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return errs.NewValidation(fmt.Sprintf("unexpected argument %q: the option is --confirm", fs.Arg(0)))
	}
	if rc.KVExport == nil || rc.PostgresStore == nil {
		return errs.NewUnexpected("the KV export and the PostgreSQL store are not wired in RunContext")
	}
	store, closeStore, err := rc.PostgresStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	out := s.out
	if out == nil {
		out = os.Stdout
	}
	return runFlip(ctx, rc.KVExport, store, *confirm, out)
}

// runFlip advances the revision sequence of PostgreSQL above the highest revision KV has, once the two stores hold the same
// records at the same revisions. It refuses otherwise: a record that PostgreSQL lacks or holds at another revision is a
// token that would stop meaning what the client holding it expects. Without confirm it only reports what it would do.
//
// The highest revision is taken over every record of every KV bucket the tables mirror, which cover every token a client can
// hold, since the sequence is shared by all the tables and KV counts the revisions of each bucket on its own. The read and the
// set are not one atomic step, so no writes may be in flight.
func runFlip(ctx context.Context, src port.KVExportReader, dst flipTarget, confirm bool, out io.Writer) error {
	results, err := runVerify(ctx, src, dst, verifyOptions{})
	if err != nil {
		if results != nil {
			(&verifySubcommand{out: out}).print(results)
		}
		return fmt.Errorf("refusing to flip: %w", err)
	}
	highest, err := highestKVRevision(ctx, src)
	if err != nil {
		return fmt.Errorf("refusing to flip: %w", err)
	}
	if !confirm {
		_, _ = fmt.Fprintf(out, "KV and PostgreSQL hold the same records at the same revisions; the highest revision in KV is %d.\n", highest)
		_, _ = fmt.Fprintf(out, "dry run: nothing changed. With --confirm, and no writes in flight, the revision sequence is advanced above %d.\n", highest)
		return nil
	}
	if err := dst.AdvanceRevisionSequence(ctx, highest); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "the revision sequence of PostgreSQL is now above %d, the highest revision in KV\n", highest)
	return nil
}

// highestKVRevision is the highest revision of any record KV holds, over every entity, the orphans included. A scan that
// could not be completed is an error: the highest revision of the records it did not reach is not known.
func highestKVRevision(ctx context.Context, src port.KVExportReader) (uint64, error) {
	var highest uint64
	note := func(revision uint64) { highest = max(highest, revision) }
	scans := []struct {
		name string
		scan func() error
	}{
		{"committees", func() error {
			return src.EachCommitteeExport(ctx, func(c port.CommitteeExport) error {
				note(c.BaseRevision)
				note(c.SettingsRevision)
				return nil
			})
		}},
		{"settings", func() error {
			return src.EachSettingsExport(ctx, func(_ *model.CommitteeSettings, revision uint64) error { note(revision); return nil })
		}},
		{"members", func() error {
			return src.EachMemberExport(ctx, func(_ *model.CommitteeMember, revision uint64) error { note(revision); return nil })
		}},
		{"invites", func() error {
			return src.EachInviteExport(ctx, func(_ *model.CommitteeInvite, revision uint64) error { note(revision); return nil })
		}},
		{"applications", func() error {
			return src.EachApplicationExport(ctx, func(_ *model.CommitteeApplication, revision uint64) error { note(revision); return nil })
		}},
		{"links", func() error {
			return src.EachLinkExport(ctx, func(_ *model.CommitteeLink, revision uint64) error { note(revision); return nil })
		}},
		{"folders", func() error {
			return src.EachFolderExport(ctx, func(_ *model.CommitteeLinkFolder, revision uint64) error { note(revision); return nil })
		}},
		{"documents", func() error {
			return src.EachDocumentExport(ctx, func(_ *model.CommitteeDocument, revision uint64) error { note(revision); return nil })
		}},
		{"briefs", func() error {
			return src.EachBriefExport(ctx, func(_ *model.GroupWeeklyBrief, revision uint64) error { note(revision); return nil })
		}},
		{"throttles", func() error {
			return src.EachThrottleExport(ctx, func(_ *model.GroupWeeklyBriefThrottle, revision uint64) error { note(revision); return nil })
		}},
	}
	for _, s := range scans {
		if err := s.scan(); err != nil {
			return 0, fmt.Errorf("reading the %s in KV: %w", s.name, err)
		}
	}
	return highest, nil
}
