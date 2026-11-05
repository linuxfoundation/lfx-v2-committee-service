// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// verifySource is the PostgreSQL store as verify reads it: every table streamed with the revision of each row.
// *postgres.Store implements it.
type verifySource interface {
	ListAllUIDs(ctx context.Context) ([]string, error)
	EachCommitteeExport(ctx context.Context, fn func(*model.CommitteeBase, uint64) error) error
	EachSettingsExport(ctx context.Context, fn func(*model.CommitteeSettings, uint64) error) error
	EachMemberExport(ctx context.Context, fn func(*model.CommitteeMember, uint64) error) error
	EachInviteExport(ctx context.Context, fn func(*model.CommitteeInvite, uint64) error) error
	EachApplicationExport(ctx context.Context, fn func(*model.CommitteeApplication, uint64) error) error
	EachLinkExport(ctx context.Context, fn func(*model.CommitteeLink, uint64) error) error
	EachFolderExport(ctx context.Context, fn func(*model.CommitteeLinkFolder, uint64) error) error
	EachDocumentExport(ctx context.Context, fn func(*model.CommitteeDocument, uint64) error) error
	EachBriefExport(ctx context.Context, fn func(*model.GroupWeeklyBrief, uint64) error) error
	EachThrottleExport(ctx context.Context, fn func(*model.GroupWeeklyBriefThrottle, uint64) error) error
}

var _ verifySource = (*postgres.Store)(nil)

// verifyOptions tune a run.
type verifyOptions struct {
	// Entities to compare; empty means allEntities.
	Entities []string
	// IncludeOrphans expects the children whose committee does not exist in KV to have been copied (migrate
	// --include-orphans), so one that is missing from PostgreSQL is a difference. By default they are only counted.
	IncludeOrphans bool
}

// The kinds of problem a comparison reports. The first four are differences between the stores.
const (
	verifyMissing  = "missing"  // in KV, not in PostgreSQL
	verifyExtra    = "extra"    // in PostgreSQL, not in KV
	verifyMismatch = "mismatch" // in both, with different content or revision
	verifyScan     = "source"   // a scan that could not be completed
	verifyKVOrphan = "kv-orphan"
	verifyPGOrphan = "pg-orphan"
)

// verifyProblem is a record the stores disagree on, an orphan, or a scan that was not complete.
type verifyProblem struct {
	UID     string
	Kind    string
	Message string
}

// verifyResult is what the comparison found for one entity.
type verifyResult struct {
	Entity string
	// KV and PostgreSQL are the records read from each store.
	KV, PostgreSQL int64
	Matched        int64
	Missing        int64
	Extra          int64
	Mismatched     int64
	// KVOrphans are KV children whose committee does not exist in KV; migrate skips them unless asked to copy them.
	KVOrphans int64
	// PostgreSQLOrphans are rows whose committee has no row: the rows that would stop a foreign key being added.
	PostgreSQLOrphans int64
	// Errors are the scans that could not be completed.
	Errors   int64
	Problems []verifyProblem
}

// differences is what makes the stores differ; orphans are reported and are not a difference.
func (r *verifyResult) differences() int64 { return r.Missing + r.Extra + r.Mismatched + r.Errors }

func (r *verifyResult) add(ctx context.Context, uid, kind, message string) {
	switch kind {
	case verifyMissing:
		r.Missing++
	case verifyExtra:
		r.Extra++
	case verifyMismatch:
		r.Mismatched++
	case verifyScan:
		r.Errors++
	case verifyKVOrphan:
		r.KVOrphans++
	case verifyPGOrphan:
		r.PostgreSQLOrphans++
	}
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, verifyProblem{UID: uid, Kind: kind, Message: message})
	}
	slog.WarnContext(ctx, "verify finding", "entity", r.Entity, "uid", uid, "kind", kind, "message", message)
}

// verifySpec is what the comparison needs to know about an entity: how to stream it from both stores and how to
// name it. T is the record, a pointer to the model.
type verifySpec[T any] struct {
	kv, pg func(ctx context.Context, fn func(record T, revision uint64) error) error
	// uid names the record; committeeUID is the committee it belongs to, nil for the committees themselves.
	uid          func(T) string
	committeeUID func(T) string
}

// compared is what is kept of a PostgreSQL row: the digest of its content and its revision.
type compared struct {
	digest   [sha256.Size]byte
	revision uint64
}

// compareEntity compares an entity in the two stores. It holds a digest and a revision for each PostgreSQL row of the
// entity, one entity at a time, and nothing else of the records. kvCommittees and pgCommittees are the uids of the
// committees each store has.
func compareEntity[T any](ctx context.Context, spec verifySpec[T], opts verifyOptions, res *verifyResult, kvCommittees, pgCommittees map[string]struct{}) {
	rows := map[string]compared{}
	err := spec.pg(ctx, func(record T, revision uint64) error {
		res.PostgreSQL++
		digest, err := digestOf(record)
		if err != nil {
			return err
		}
		uid := spec.uid(record)
		rows[uid] = compared{digest: digest, revision: revision}
		if spec.committeeUID != nil {
			if _, ok := pgCommittees[spec.committeeUID(record)]; !ok {
				res.add(ctx, uid, verifyPGOrphan, "its committee has no row in PostgreSQL")
			}
		}
		return nil
	})
	if err != nil {
		// Without the PostgreSQL side nothing says which KV records are missing.
		res.add(ctx, "", verifyScan, fmt.Sprintf("reading PostgreSQL: %v", err))
		return
	}

	err = spec.kv(ctx, func(record T, revision uint64) error {
		res.KV++
		uid := spec.uid(record)
		row, inPostgreSQL := rows[uid]
		if spec.committeeUID != nil {
			if _, ok := kvCommittees[spec.committeeUID(record)]; !ok {
				res.add(ctx, uid, verifyKVOrphan, "its committee does not exist in KV")
				if !inPostgreSQL && !opts.IncludeOrphans {
					return nil
				}
			}
		}
		if !inPostgreSQL {
			res.add(ctx, uid, verifyMissing, "in KV and not in PostgreSQL")
			return nil
		}
		delete(rows, uid)
		digest, err := digestOf(record)
		if err != nil {
			return err
		}
		var differs []string
		if digest != row.digest {
			differs = append(differs, "content differs")
		}
		if revision != row.revision {
			differs = append(differs, fmt.Sprintf("revision differs (KV %d, PostgreSQL %d)", revision, row.revision))
		}
		if len(differs) > 0 {
			res.add(ctx, uid, verifyMismatch, strings.Join(differs, "; "))
			return nil
		}
		res.Matched++
		return nil
	})
	if err != nil {
		// A KV scan that did not complete cannot say which PostgreSQL rows KV lacks: a record it could not read, or
		// that it never reached, is still in KV. The rows are not reported extra; they are counted as unchecked.
		res.add(ctx, "", verifyScan, fmt.Sprintf("reading KV: %v; %d PostgreSQL rows could not be checked against KV", err, len(rows)))
		return
	}

	extra := make([]string, 0, len(rows))
	for uid := range rows {
		extra = append(extra, uid)
	}
	slices.Sort(extra)
	for _, uid := range extra {
		res.add(ctx, uid, verifyExtra, "in PostgreSQL and not in KV")
	}
}

// digestOf is the SHA-256 of the record in a form that does not depend on how a store holds it: the JSON of the
// model without its revision (which is compared on its own, and is not in the JSON), with the keys sorted and the
// times in UTC and to the microsecond PostgreSQL keeps. Nothing else is folded away: an empty list and a missing one
// are different values, as the PostgreSQL store keeps them.
func digestOf(record any) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encoding a record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("decoding a record: %w", err)
	}
	normalized, err := json.Marshal(normalizeTimes(value))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encoding a normalized record: %w", err)
	}
	return sha256.Sum256(normalized), nil
}

// normalizeTimes returns v with every time written the same way: in UTC, to the microsecond.
func normalizeTimes(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeTimes(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeTimes(item)
		}
		return out
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		}
		return v
	default:
		return v
	}
}

// runVerify compares the committee data in KV with the PostgreSQL data, one entity at a time. It only reads. It
// returns one result per entity, with the settings of the committees as a line of their own; the error is non-nil
// when the stores differ or a scan could not be completed, so the command can exit non-zero. The results are returned
// either way.
func runVerify(ctx context.Context, src port.KVExportReader, dst verifySource, opts verifyOptions) ([]verifyResult, error) {
	if len(opts.Entities) == 0 {
		opts.Entities = allEntities
	}
	if err := validateEntities(opts.Entities); err != nil {
		return nil, err
	}
	want := func(entity string) bool { return slices.Contains(opts.Entities, entity) }

	kvCommittees, kvErr := committeeSet(ctx, src)
	pgCommittees, pgErr := postgresCommitteeSet(ctx, dst)
	var results []*verifyResult
	run := func(entity string, compare func(res *verifyResult)) {
		res := &verifyResult{Entity: entity}
		results = append(results, res)
		switch {
		case kvErr != nil:
			res.add(ctx, "", verifyScan, fmt.Sprintf("listing the committees in KV: %v", kvErr))
		case pgErr != nil:
			res.add(ctx, "", verifyScan, fmt.Sprintf("listing the committees in PostgreSQL: %v", pgErr))
		default:
			compare(res)
		}
		slog.InfoContext(ctx, "verify summary", "entity", entity, "kv", res.KV, "postgresql", res.PostgreSQL, "matched", res.Matched,
			"missing", res.Missing, "extra", res.Extra, "mismatched", res.Mismatched, "kv_orphans", res.KVOrphans,
			"postgresql_orphans", res.PostgreSQLOrphans, "errors", res.Errors)
	}

	if want(entityCommittees) {
		run(entityCommittees, func(res *verifyResult) {
			compareEntity(ctx, committeeVerifySpec(src, dst), opts, res, kvCommittees, pgCommittees)
		})
		run(resultSettings, func(res *verifyResult) {
			compareEntity(ctx, settingsVerifySpec(src, dst), opts, res, kvCommittees, pgCommittees)
		})
	}
	for _, child := range childVerifies(src, dst) {
		if !want(child.entity) {
			continue
		}
		run(child.entity, func(res *verifyResult) { child.compare(ctx, opts, res, kvCommittees, pgCommittees) })
	}

	out := make([]verifyResult, 0, len(results))
	total := int64(0)
	for _, r := range results {
		out = append(out, *r)
		total += r.differences()
	}
	if total > 0 {
		return out, fmt.Errorf("KV and PostgreSQL differ: %d differences", total)
	}
	return out, nil
}

// postgresCommitteeSet lists the uid of every committee in PostgreSQL.
func postgresCommitteeSet(ctx context.Context, dst verifySource) (map[string]struct{}, error) {
	uids, err := dst.ListAllUIDs(ctx)
	if err != nil {
		return nil, err
	}
	exists := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		exists[uid] = struct{}{}
	}
	return exists, nil
}

// childVerify is the comparison of one entity that belongs to a committee.
type childVerify struct {
	entity  string
	compare func(ctx context.Context, opts verifyOptions, res *verifyResult, kvCommittees, pgCommittees map[string]struct{})
}

func verifyChild[T any](entity string, spec verifySpec[T]) childVerify {
	return childVerify{entity: entity, compare: func(ctx context.Context, opts verifyOptions, res *verifyResult, kvCommittees, pgCommittees map[string]struct{}) {
		compareEntity(ctx, spec, opts, res, kvCommittees, pgCommittees)
	}}
}

// committeeVerifySpec compares the committees, which KV streams together with their settings.
func committeeVerifySpec(src port.KVExportReader, dst verifySource) verifySpec[*model.CommitteeBase] {
	return verifySpec[*model.CommitteeBase]{
		kv: func(ctx context.Context, fn func(*model.CommitteeBase, uint64) error) error {
			return src.EachCommitteeExport(ctx, func(c port.CommitteeExport) error { return fn(c.Base, c.BaseRevision) })
		},
		pg:  dst.EachCommitteeExport,
		uid: func(c *model.CommitteeBase) string { return c.UID },
	}
}

// settingsVerifySpec compares the settings of the committees, keyed by the committee uid.
func settingsVerifySpec(src port.KVExportReader, dst verifySource) verifySpec[*model.CommitteeSettings] {
	return verifySpec[*model.CommitteeSettings]{
		kv: func(ctx context.Context, fn func(*model.CommitteeSettings, uint64) error) error {
			return src.EachCommitteeExport(ctx, func(c port.CommitteeExport) error {
				if c.Settings == nil {
					return nil
				}
				return fn(c.Settings, c.SettingsRevision)
			})
		},
		pg:           dst.EachSettingsExport,
		uid:          func(s *model.CommitteeSettings) string { return s.UID },
		committeeUID: func(s *model.CommitteeSettings) string { return s.UID },
	}
}

// childVerifies lists the comparisons of the entities that belong to a committee, in the order they are run.
func childVerifies(src port.KVExportReader, dst verifySource) []childVerify {
	return []childVerify{
		verifyChild(entityMembers, verifySpec[*model.CommitteeMember]{
			kv: src.EachMemberExport, pg: dst.EachMemberExport,
			uid:          func(m *model.CommitteeMember) string { return m.UID },
			committeeUID: func(m *model.CommitteeMember) string { return m.CommitteeUID },
		}),
		verifyChild(entityInvites, verifySpec[*model.CommitteeInvite]{
			kv: src.EachInviteExport, pg: dst.EachInviteExport,
			uid:          func(i *model.CommitteeInvite) string { return i.UID },
			committeeUID: func(i *model.CommitteeInvite) string { return i.CommitteeUID },
		}),
		verifyChild(entityApplications, verifySpec[*model.CommitteeApplication]{
			kv: src.EachApplicationExport, pg: dst.EachApplicationExport,
			uid:          func(a *model.CommitteeApplication) string { return a.UID },
			committeeUID: func(a *model.CommitteeApplication) string { return a.CommitteeUID },
		}),
		verifyChild(entityLinks, verifySpec[*model.CommitteeLink]{
			kv: src.EachLinkExport, pg: dst.EachLinkExport,
			uid:          func(l *model.CommitteeLink) string { return l.UID },
			committeeUID: func(l *model.CommitteeLink) string { return l.CommitteeUID },
		}),
		verifyChild(entityFolders, verifySpec[*model.CommitteeLinkFolder]{
			kv: src.EachFolderExport, pg: dst.EachFolderExport,
			uid:          func(f *model.CommitteeLinkFolder) string { return f.UID },
			committeeUID: func(f *model.CommitteeLinkFolder) string { return f.CommitteeUID },
		}),
		verifyChild(entityDocuments, verifySpec[*model.CommitteeDocument]{
			kv: src.EachDocumentExport, pg: dst.EachDocumentExport,
			uid:          func(d *model.CommitteeDocument) string { return d.UID },
			committeeUID: func(d *model.CommitteeDocument) string { return d.CommitteeUID },
		}),
		verifyChild(entityBriefs, verifySpec[*model.GroupWeeklyBrief]{
			kv: src.EachBriefExport, pg: dst.EachBriefExport,
			uid:          func(b *model.GroupWeeklyBrief) string { return b.UID },
			committeeUID: func(b *model.GroupWeeklyBrief) string { return b.CommitteeUID },
		}),
		verifyChild(entityThrottles, verifySpec[*model.GroupWeeklyBriefThrottle]{
			kv: src.EachThrottleExport, pg: dst.EachThrottleExport,
			uid: func(t *model.GroupWeeklyBriefThrottle) string {
				return t.CommitteeUID + "." + model.WindowDateKey(t.WindowStart)
			},
			committeeUID: func(t *model.GroupWeeklyBriefThrottle) string { return t.CommitteeUID },
		}),
	}
}
