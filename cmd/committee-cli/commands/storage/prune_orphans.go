// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// defaultMaxPrune is how many members a run deletes before it refuses and asks for a higher --max, so a scan that
// wrongly finds a whole store orphaned deletes nothing.
const defaultMaxPrune = 1000

// maxPrintedCommittees bounds the committees the summary lists.
const maxPrintedCommittees = 50

// pruneOrphansSubcommand deletes the members whose committee no longer exists in KV. Deleting a committee in the kv and
// dual modes does not delete its members, so they stay behind; they can never be listed again, and PostgreSQL cannot
// validate the committee_uid foreign key while a copy of one is there.
type pruneOrphansSubcommand struct {
	// out receives the summary; os.Stdout when nil.
	out io.Writer
}

func (s *pruneOrphansSubcommand) Name() string { return "prune-orphans" }

func (s *pruneOrphansSubcommand) Help() string {
	return "delete the committee members whose committee no longer exists in KV, from the stores of the storage mode (a dry run unless --apply)"
}

// orphanMember is a member whose committee is gone, with the revision KV holds it at.
type orphanMember struct {
	uid          string
	committeeUID string
	revision     uint64
}

// pruneResult is what a run found and did.
type pruneResult struct {
	Orphans       int
	Deleted       int
	AlreadyGone   int
	Failed        int
	IndexerFailed int
	ByCommittee   map[string]int
}

func (s *pruneOrphansSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	fs := flag.NewFlagSet("prune-orphans", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli storage prune-orphans [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}
	apply := fs.Bool("apply", false, "delete the orphans; without it the run only reports what it would delete")
	maxDelete := fs.Int("max", defaultMaxPrune, "refuse to delete more than this many members in one run")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return errs.NewValidation(fmt.Sprintf("unexpected argument %q: options are flags, for example --apply", fs.Arg(0)))
	}
	if *maxDelete <= 0 {
		return errs.NewValidation("--max must be greater than zero")
	}
	if rc.KVExport == nil || rc.CommitteeMemberWriter == nil || rc.Publisher == nil || rc.KVReader() == nil {
		return errs.NewUnexpected("the KV export, the KV reader, the member writer and the publisher are not wired in RunContext")
	}
	// Orphans only arise where KV decides and a committee delete does not cascade. In the PostgreSQL-primary modes the delete
	// goes to PostgreSQL first, which issues its own revisions, so the KV revision of a scan is not the one to delete at, and
	// a committee that failed to copy to KV would make its members look orphaned.
	if mode := composite.Mode(rc.StorageMode); mode != composite.ModeKV && mode != composite.ModeDual {
		return errs.NewValidation(fmt.Sprintf("prune-orphans runs with STORAGE_MODE=%s or %s, not %q: members of deleted committees are only left behind in those modes",
			composite.ModeKV, composite.ModeDual, rc.StorageMode))
	}

	// The indexer rejects a message without an authorization header; the other CLI commands that publish set this one.
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	orphans, err := findOrphanMembers(ctx, rc)
	if err != nil {
		return err
	}
	result := &pruneResult{Orphans: len(orphans), ByCommittee: map[string]int{}}
	for _, o := range orphans {
		result.ByCommittee[o.committeeUID]++
	}

	if !*apply {
		s.print(result, false)
		return nil
	}
	if len(orphans) > *maxDelete {
		s.print(result, false)
		return errs.NewValidation(fmt.Sprintf("%d orphan members is more than --max=%d: check the list above, then raise --max to delete them", len(orphans), *maxDelete))
	}

	for _, o := range orphans {
		s.prune(ctx, rc, o, result)
	}
	s.print(result, true)
	if result.Failed > 0 || result.IndexerFailed > 0 {
		return errs.NewUnexpected(fmt.Sprintf("%d members could not be deleted and %d indexer messages were not sent: see the log", result.Failed, result.IndexerFailed))
	}
	return nil
}

// findOrphanMembers lists the members whose committee is not in KV. A scan that could not read everything returns an
// error and no members, so a partial scan never turns into deletes.
func findOrphanMembers(ctx context.Context, rc commands.RunContext) ([]orphanMember, error) {
	uids, err := rc.KVExport.ListAllUIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the committees: %w", err)
	}
	if len(uids) == 0 {
		// An empty list is more likely a wrong store than a store without committees; every member would be an orphan.
		return nil, errs.NewValidation("KV has no committees, so every member would be an orphan: refusing to continue")
	}
	committees := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		committees[uid] = struct{}{}
	}

	var orphans []orphanMember
	scanErr := rc.KVExport.EachMemberExport(ctx, func(member *model.CommitteeMember, revision uint64) error {
		if _, ok := committees[member.CommitteeUID]; !ok {
			orphans = append(orphans, orphanMember{uid: member.UID, committeeUID: member.CommitteeUID, revision: revision})
		}
		return nil
	})
	if scanErr != nil {
		return nil, fmt.Errorf("scanning the members: %w", scanErr)
	}
	if err := confirmCommitteesGone(ctx, rc, orphans); err != nil {
		return nil, err
	}
	return orphans, nil
}

// confirmCommitteesGone reads each committee the orphans point at, one by one, and returns an error unless every one of them
// is gone. The committee uids come from a key listing that can end early without an error (docs/nats-storage.md), and they
// are read before the members, so a committee that the listing missed, or that was created in between, would otherwise make
// its members look orphaned; they would be deleted at their current revision, which the revision guard cannot catch. An error
// stops the run before anything is deleted, since a listing that missed one committee may have missed others. There are few
// distinct orphan committees, so this costs a few reads.
func confirmCommitteesGone(ctx context.Context, rc commands.RunContext, orphans []orphanMember) error {
	reader := rc.KVReader()
	checked := map[string]struct{}{}
	for _, o := range orphans {
		if _, done := checked[o.committeeUID]; done {
			continue
		}
		checked[o.committeeUID] = struct{}{}
		_, _, err := reader.GetBase(ctx, o.committeeUID)
		if err == nil {
			return errs.NewUnexpected(fmt.Sprintf("committee %s was missing from the committee listing but still exists in KV: the listing is incomplete, so nothing was deleted", o.committeeUID))
		}
		var notFound errs.NotFound
		if !errors.As(err, &notFound) {
			return fmt.Errorf("checking that committee %s is gone: %w", o.committeeUID, err)
		}
	}
	return nil
}

// prune deletes one orphan from the stores and tells the indexer. It does not publish the member deleted event or touch
// the access tuples: the event notifies the person, and nobody should be told they were removed from a committee that was
// deleted long ago.
func (s *pruneOrphansSubcommand) prune(ctx context.Context, rc commands.RunContext, o orphanMember, result *pruneResult) {
	if err := rc.CommitteeMemberWriter.DeleteMember(ctx, o.uid, o.revision); err != nil {
		var notFound errs.NotFound
		if errors.As(err, &notFound) {
			result.AlreadyGone++
		} else {
			result.Failed++
			slog.ErrorContext(ctx, "failed to delete an orphan member", "error", err, "member_uid", o.uid, "committee_uid", o.committeeUID)
			return
		}
	} else {
		result.Deleted++
	}

	message := model.CommitteeIndexerMessage{Action: model.ActionDeleted}
	built, err := message.Build(ctx, o.uid)
	if err == nil {
		err = rc.Publisher.Indexer(ctx, constants.IndexCommitteeMemberSubject, built, false)
	}
	if err != nil {
		result.IndexerFailed++
		slog.ErrorContext(ctx, "failed to publish the indexer delete of an orphan member", "error", err, "member_uid", o.uid)
	}
}

// print writes the summary: the totals, then the committees with the most orphans.
func (s *pruneOrphansSubcommand) print(result *pruneResult, applied bool) {
	out := s.out
	if out == nil {
		out = os.Stdout
	}
	_, _ = fmt.Fprintf(out, "orphan members (committee not in KV): %d in %d committees\n", result.Orphans, len(result.ByCommittee))

	type group struct {
		uid   string
		count int
	}
	groups := make([]group, 0, len(result.ByCommittee))
	for uid, count := range result.ByCommittee {
		groups = append(groups, group{uid, count})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].count != groups[j].count {
			return groups[i].count > groups[j].count
		}
		return groups[i].uid < groups[j].uid
	})
	for i, g := range groups {
		if i == maxPrintedCommittees {
			_, _ = fmt.Fprintf(out, "  ... %d more committees\n", len(groups)-i)
			break
		}
		_, _ = fmt.Fprintf(out, "  committee %s: %d\n", g.uid, g.count)
	}

	if !applied {
		_, _ = fmt.Fprintln(out, "nothing was deleted; run again with --apply to delete them")
		return
	}
	_, _ = fmt.Fprintf(out, "deleted=%d already_gone=%d failed=%d indexer_failed=%d\n", result.Deleted, result.AlreadyGone, result.Failed, result.IndexerFailed)
}
