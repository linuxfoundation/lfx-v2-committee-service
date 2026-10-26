// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	sourceV1 = "v1"
	sourceV2 = "v2"

	locationBase     = "base"
	locationSettings = "settings"

	// maxLoggedValueLen bounds how much of a field value is written to the logs.
	maxLoggedValueLen = 80

	// collateralNotice warns that a write is not limited to the selected fields on the v1 side.
	collateralNotice = "every write also makes the v1 sync helper PATCH v1 with all the v2 fields it maps " +
		"(name, category, description, website, join mode, chat channel, public, display name), " +
		"not only the selected ones, so other v1 values can change too"
)

// committeeField describes one committee field the committee-fields subcommand can reconcile
// between v1 and v2.
type committeeField struct {
	name     string
	location string // locationBase or locationSettings: which v2 record holds the field
	v1Key    string // key in the v1 committee record
	isBool   bool
	help     string

	// maxLen is the longest value, in characters, the committee API accepts for this field. The
	// writer orchestrator does not re-check it, so --from=v1 enforces it. 0 means no limit.
	maxLen int

	// fromV1 is true when the v1 value can be copied straight into v2. When false,
	// fromV1Note says why.
	fromV1     bool
	fromV1Note string

	// fromV2 is true when republishing the v2 committee makes the v1 sync helper write the
	// field to v1. When false, fromV2Note says why.
	fromV2     bool
	fromV2Note string

	v2Get func(*model.CommitteeBase, *model.CommitteeSettings) string
	v2Set func(*model.CommitteeBase, *model.CommitteeSettings, string)
}

// committeeFields is the registry of supported fields, keyed by name.
var committeeFields = map[string]*committeeField{
	"public": {
		name: "public", location: locationBase, v1Key: "public_enabled", isBool: true,
		help:   "whether the committee is publicly visible",
		fromV1: true, fromV2: true,
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string { return strconv.FormatBool(b.Public) },
		v2Set: func(b *model.CommitteeBase, _ *model.CommitteeSettings, v string) { b.Public, _ = strconv.ParseBool(v) },
	},
	"display_name": {
		name: "display_name", location: locationBase, v1Key: "public_name",
		help:   "public display name (v1 public_name)",
		maxLen: 100,
		fromV1: true, fromV2: true,
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string { return b.DisplayName },
		v2Set: func(b *model.CommitteeBase, _ *model.CommitteeSettings, v string) { b.DisplayName = v },
	},
	"enable_voting": {
		name: "enable_voting", location: locationBase, v1Key: "enable_voting__c", isBool: true,
		help:   "whether the committee has voting enabled",
		fromV1: true,
		fromV2: false, fromV2Note: "the sync helper does not send enable_voting to v1 yet",
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string {
			return strconv.FormatBool(b.EnableVoting)
		},
		v2Set: func(b *model.CommitteeBase, _ *model.CommitteeSettings, v string) {
			b.EnableVoting, _ = strconv.ParseBool(v)
		},
	},
	"business_email_required": {
		name: "business_email_required", location: locationSettings, v1Key: "business_email_required__c", isBool: true,
		help:   "whether members must use a business email (a committee setting)",
		fromV1: true,
		fromV2: false, fromV2Note: "the sync helper does not send business_email_required to v1",
		v2Get: func(_ *model.CommitteeBase, s *model.CommitteeSettings) string {
			return strconv.FormatBool(s != nil && s.BusinessEmailRequired)
		},
		v2Set: func(_ *model.CommitteeBase, s *model.CommitteeSettings, v string) {
			s.BusinessEmailRequired, _ = strconv.ParseBool(v)
		},
	},
	"description": {
		name: "description", location: locationBase, v1Key: "description__c",
		help:   "committee description",
		maxLen: 2000,
		fromV1: true, fromV2: true,
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string { return b.Description },
		v2Set: func(b *model.CommitteeBase, _ *model.CommitteeSettings, v string) { b.Description = v },
	},
	"chat_channel": {
		name: "chat_channel", location: locationBase, v1Key: "chat_channel",
		help:   "chat channel URL",
		maxLen: 500,
		fromV1: true, fromV2: true,
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string {
			if b.ChatChannel == nil {
				return ""
			}
			return *b.ChatChannel
		},
		v2Set: func(b *model.CommitteeBase, _ *model.CommitteeSettings, v string) {
			if v == "" {
				b.ChatChannel = nil
				return
			}
			b.ChatChannel = &v
		},
	},
	"website": {
		name: "website", location: locationBase, v1Key: "committee_website__c",
		help:   "committee website",
		fromV1: false, fromV1Note: "v1 values need URL normalization first (lfx-self-serve-ops#950)",
		fromV2: false, fromV2Note: "v1 values need URL normalization first (lfx-self-serve-ops#950)",
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string {
			if b.Website == nil {
				return ""
			}
			return *b.Website
		},
	},
	"category": {
		name: "category", location: locationBase, v1Key: "type__c",
		help:   "committee category",
		fromV1: false, fromV1Note: "v1 type__c needs the sync helper's type-to-category mapping (lfx-self-serve-ops#948)",
		fromV2: false, fromV2Note: "v1 type__c needs the sync helper's type-to-category mapping (lfx-self-serve-ops#948)",
		v2Get: func(b *model.CommitteeBase, _ *model.CommitteeSettings) string { return b.Category },
	},
}

// v1Value reads and normalizes this field from a v1 committee record. A missing or null key is
// the zero value (false / empty), matching how v1 reads these columns.
func (f *committeeField) v1Value(record map[string]any) (string, error) {
	raw, ok := record[f.v1Key]
	if !ok || raw == nil {
		if f.isBool {
			return "false", nil
		}
		return "", nil
	}
	if f.isBool {
		b, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("v1 field %q has type %T, want bool", f.v1Key, raw)
		}
		return strconv.FormatBool(b), nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("v1 field %q has type %T, want string", f.v1Key, raw)
	}
	return s, nil
}

func sortedFieldNames() []string {
	names := make([]string, 0, len(committeeFields))
	for name := range committeeFields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// writeFieldList prints every field the subcommand knows about and which directions it supports.
func writeFieldList(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Fields supported by committee-fields:")
	_, _ = fmt.Fprintln(w)
	for _, name := range sortedFieldNames() {
		f := committeeFields[name]
		kind := "text"
		if f.isBool {
			kind = "bool"
		}
		_, _ = fmt.Fprintf(w, "  %-24s %-5s v2: %-8s v1: %-28s %s\n", f.name, kind, f.location, f.v1Key, f.help)
		_, _ = fmt.Fprintf(w, "  %-24s       --from=v1: %s\n", "", directionStatus(f.fromV1, f.fromV1Note, "copies the v1 value into v2"))
		_, _ = fmt.Fprintf(w, "  %-24s       --from=v2: %s\n", "", directionStatus(f.fromV2, f.fromV2Note, "republishes v2 so the sync helper writes it to v1"))
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "--from=v2 cannot push an empty text value: the v1 API ignores empty strings, so a v1 value cannot be cleared.")
	_, _ = fmt.Fprintln(w, "Note: "+collateralNotice+".")
}

func directionStatus(supported bool, note, how string) string {
	if supported {
		return "supported (" + how + ")"
	}
	return "NOT supported: " + note
}

// stringList is a flag.Value that accepts comma-separated values and may be repeated.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*l = append(*l, part)
		}
	}
	return nil
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// committeeFieldsSubcommand reconciles chosen fields of chosen committees between v1 and v2,
// in the direction given by --from.
type committeeFieldsSubcommand struct {
	// v1 is the source of v1 committee records. When nil, Run reads the v1 sync helper's mirror
	// through rc.KVStore; tests set it to a fake.
	v1 v1CommitteeReader
}

func (s *committeeFieldsSubcommand) Name() string { return "committee-fields" }

// RunsOffline implements commands.OfflineSubcommand: --list-fields only prints the static field
// catalog, so it needs no NATS connection.
func (s *committeeFieldsSubcommand) RunsOffline(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--list-fields", "-list-fields", "--list-fields=true", "-list-fields=true":
			return true
		}
	}
	return false
}

func (s *committeeFieldsSubcommand) Help() string {
	return "reconcile chosen committee fields between v1 and v2 (--from=v1|v2; --list-fields shows what is supported; dry-run by default)"
}

func (s *committeeFieldsSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	var committeeUIDs, fieldNames stringList
	fs := flag.NewFlagSet("committee-fields", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync committee-fields --committee-uid <uid[,uid...]> --field <field[,field...]> --from <v1|v2> [flags]\n\nflags:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(fs.Output(), "\nRun with --list-fields to see the supported fields.\n")
	}
	fs.Var(&committeeUIDs, "committee-uid", "committee UIDs to reconcile; comma-separated and/or repeated (required)")
	fs.Var(&fieldNames, "field", "fields to reconcile; comma-separated and/or repeated (required; see --list-fields)")
	from := fs.String("from", "", "source of truth: v1 copies the v1 value into v2; v2 republishes v2 so the sync helper writes it to v1 (required)")
	listFields := fs.Bool("list-fields", false, "print the supported fields and exit")
	sleep := fs.Duration("sleep", 0, "wait between committees to reduce indexer pressure (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", true, "report what would change without writing (default true; pass --dry-run=false to write)")
	if err := fs.Parse(rc.Args); err != nil {
		if stderrors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if *listFields {
		writeFieldList(os.Stdout)
		return nil
	}

	uids := dedupe(committeeUIDs)
	fieldsToSync, err := validateFieldsArgs(*from, uids, dedupe(fieldNames))
	if err != nil {
		return err
	}
	if rc.CommitteeReader == nil {
		return errors.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeWriterOrchestrator == nil {
		return errors.NewUnexpected("CommitteeWriterOrchestrator is not wired in RunContext")
	}
	v1 := s.v1
	if v1 == nil {
		if rc.KVStore == nil {
			return errors.NewUnexpected("KVStore is not wired in RunContext")
		}
		v1 = newKVV1CommitteeReader(rc.KVStore)
	}

	rc.DryRun = *dryRun
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	stats := commands.NewStats()
	stats.Total = len(uids)
	stats.DryRun = rc.DryRun

	slog.InfoContext(ctx, "reconciling committee fields",
		"from", *from,
		"fields", fieldNamesOf(fieldsToSync),
		"committees", len(uids),
		"dry_run", rc.DryRun,
	)
	slog.WarnContext(ctx, "note: "+collateralNotice)
	for i, uid := range uids {
		if i > 0 && *sleep > 0 {
			time.Sleep(*sleep)
		}
		// reconcileCommittee records its own outcome in stats; keep going on a failure.
		_ = reconcileCommittee(ctx, rc, v1, uid, *from, fieldsToSync, stats)
	}

	stats.Log(ctx, "sync committee-fields")
	if stats.Failed > 0 {
		return fmt.Errorf("%d committee(s) failed to reconcile", stats.Failed)
	}
	return nil
}

func fieldNamesOf(fields []*committeeField) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	return names
}

// validateFieldsArgs checks the arguments and resolves the field names, before anything is
// read or written.
func validateFieldsArgs(from string, uids, names []string) ([]*committeeField, error) {
	if from != sourceV1 && from != sourceV2 {
		return nil, errors.NewValidation("--from is required and must be v1 or v2")
	}
	if len(uids) == 0 {
		return nil, errors.NewValidation("--committee-uid is required")
	}
	if len(names) == 0 {
		return nil, errors.NewValidation("--field is required; run with --list-fields to see the supported fields")
	}

	fields := make([]*committeeField, 0, len(names))
	for _, name := range names {
		f, ok := committeeFields[name]
		if !ok {
			return nil, errors.NewValidation(fmt.Sprintf("unknown field %q; supported fields: %s", name, strings.Join(sortedFieldNames(), ", ")))
		}
		if from == sourceV1 && !f.fromV1 {
			return nil, errors.NewValidation(fmt.Sprintf("field %q cannot be synced --from=v1: %s", name, f.fromV1Note))
		}
		if from == sourceV2 && !f.fromV2 {
			return nil, errors.NewValidation(fmt.Sprintf("field %q cannot be synced --from=v2: %s", name, f.fromV2Note))
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// fieldDiff is one field whose v1 and v2 values differ.
type fieldDiff struct {
	field *committeeField
	v1    string
	v2    string
}

func summarizeValue(v string) string {
	runes := []rune(v)
	if len(runes) <= maxLoggedValueLen {
		return v
	}
	return fmt.Sprintf("%s… (%d chars)", string(runes[:maxLoggedValueLen]), len(runes))
}

// reconcileCommittee reconciles the chosen fields of one committee. Revision conflicts are
// retried from a fresh read. The outcome is recorded in stats.
func reconcileCommittee(ctx context.Context, rc commands.RunContext, v1 v1CommitteeReader, uid, from string, fields []*committeeField, stats *commands.Stats) error {
	const maxRetries = 3

	needSettings := false
	for _, f := range fields {
		if f.location == locationSettings {
			needSettings = true
		}
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		base, baseRevision, err := rc.CommitteeReader.GetBase(ctx, uid)
		if err != nil {
			stats.Failed++
			slog.ErrorContext(ctx, "failed to get committee base", "committee_uid", uid, "error", err)
			return err
		}
		var settings *model.CommitteeSettings
		var settingsRevision uint64
		if needSettings {
			if settings, settingsRevision, err = rc.CommitteeReader.GetSettings(ctx, uid); err != nil {
				stats.Failed++
				slog.ErrorContext(ctx, "failed to get committee settings", "committee_uid", uid, "error", err)
				return err
			}
			if settings == nil {
				stats.Failed++
				err = errors.NewNotFound(fmt.Sprintf("settings not found for committee %s", uid))
				slog.ErrorContext(ctx, "failed to get committee settings", "committee_uid", uid, "error", err)
				return err
			}
		}
		v1Record, err := v1.GetV1Committee(ctx, uid)
		if err != nil {
			stats.Failed++
			slog.ErrorContext(ctx, "failed to get v1 committee", "committee_uid", uid, "error", err)
			return err
		}

		var diffs []fieldDiff
		for _, f := range fields {
			v1Val, err := f.v1Value(v1Record)
			if err != nil {
				stats.Failed++
				slog.ErrorContext(ctx, "cannot read v1 field", "committee_uid", uid, "field", f.name, "error", err)
				return err
			}
			v2Val := f.v2Get(base, settings)
			if v1Val == v2Val {
				slog.InfoContext(ctx, "field already matches", "committee_uid", uid, "name", base.Name, "field", f.name, "value", summarizeValue(v2Val))
				continue
			}
			diffs = append(diffs, fieldDiff{field: f, v1: v1Val, v2: v2Val})
		}
		if len(diffs) == 0 {
			stats.Skipped++
			return nil
		}

		for _, d := range diffs {
			slog.InfoContext(ctx, "field differs",
				"committee_uid", uid,
				"name", base.Name,
				"field", d.field.name,
				"v1", summarizeValue(d.v1),
				"v2", summarizeValue(d.v2),
				"from", from,
			)
		}

		var writeErr error
		switch from {
		case sourceV1:
			writeErr = copyV1ToV2(ctx, rc, uid, base, baseRevision, settings, settingsRevision, diffs)
		default:
			writeErr = republishV2(ctx, rc, uid, base, baseRevision, diffs)
		}
		if writeErr == nil {
			stats.Updated++
			return nil
		}

		var conflictErr errors.Conflict
		if stderrors.As(writeErr, &conflictErr) && attempt < maxRetries-1 {
			slog.DebugContext(ctx, "revision conflict reconciling committee, retrying", "attempt", attempt+1, "committee_uid", uid)
			continue
		}
		stats.Failed++
		slog.ErrorContext(ctx, "failed to reconcile committee", "committee_uid", uid, "error", writeErr)
		return writeErr
	}

	// Unreachable: the final iteration always returns above.
	return nil
}

// copyV1ToV2 writes the v1 value of each differing field into v2. Base fields go through one
// Update and settings fields through one UpdateSettings, both starting from the current record
// so no other field changes.
func copyV1ToV2(ctx context.Context, rc commands.RunContext, uid string, base *model.CommitteeBase, baseRevision uint64, settings *model.CommitteeSettings, settingsRevision uint64, diffs []fieldDiff) error {
	newBase := *base
	var newSettings *model.CommitteeSettings
	if settings != nil {
		copied := *settings
		newSettings = &copied
	}

	baseChanged, settingsChanged := false, false
	for _, d := range diffs {
		if d.field.maxLen > 0 && utf8.RuneCountInString(d.v1) > d.field.maxLen {
			return errors.NewValidation(fmt.Sprintf("v1 value of field %q is %d characters, over the %d the committee API accepts", d.field.name, utf8.RuneCountInString(d.v1), d.field.maxLen))
		}
		d.field.v2Set(&newBase, newSettings, d.v1)
		if d.field.location == locationSettings {
			settingsChanged = true
		} else {
			baseChanged = true
		}
	}

	if baseChanged && willReserveSSOGroupName(base, &newBase) {
		slog.InfoContext(ctx, "an SSO group name will be generated", "committee_uid", uid)
	}

	if rc.DryRun {
		slog.InfoContext(ctx, "dry-run: would copy v1 values into v2", "committee_uid", uid, "base_update", baseChanged, "settings_update", settingsChanged)
		return nil
	}

	if baseChanged {
		if _, err := rc.CommitteeWriterOrchestrator.Update(ctx, &model.Committee{CommitteeBase: newBase}, baseRevision, false); err != nil {
			return err
		}
	}
	if settingsChanged {
		if _, err := rc.CommitteeWriterOrchestrator.UpdateSettings(ctx, newSettings, settingsRevision, false); err != nil {
			if baseChanged {
				return fmt.Errorf("committee base was updated but settings update failed: %w", err)
			}
			return err
		}
	}
	slog.InfoContext(ctx, "copied v1 values into v2", "committee_uid", uid, "fields", fieldNamesOfDiffs(diffs))
	return nil
}

// republishV2 re-saves the committee unchanged so the update event reaches the v1 sync helper,
// which writes the field values to v1. It refuses to run when any differing text field is
// empty in v2, because the v1 API ignores empty strings and the repair would silently not work.
func republishV2(ctx context.Context, rc commands.RunContext, uid string, base *model.CommitteeBase, baseRevision uint64, diffs []fieldDiff) error {
	for _, d := range diffs {
		if !d.field.isBool && d.v2 == "" {
			return errors.NewValidation(fmt.Sprintf("field %q is empty in v2 but set in v1; the v1 API cannot clear a value, so republishing v2 cannot fix it", d.field.name))
		}
	}

	if willReserveSSOGroupName(base, base) {
		slog.InfoContext(ctx, "an SSO group name will be generated", "committee_uid", uid)
	}

	if rc.DryRun {
		slog.InfoContext(ctx, "dry-run: would republish v2 so the sync helper writes the fields to v1", "committee_uid", uid, "fields", fieldNamesOfDiffs(diffs))
		return nil
	}

	if _, err := rc.CommitteeWriterOrchestrator.Update(ctx, &model.Committee{CommitteeBase: *base}, baseRevision, false); err != nil {
		return err
	}
	slog.InfoContext(ctx, "republished v2; the sync helper will write the fields to v1", "committee_uid", uid, "fields", fieldNamesOfDiffs(diffs))
	return nil
}

// willReserveSSOGroupName reports whether updating a committee from existing to updated makes the
// orchestrator reserve a new SSO group name, which is also the committee's URL slug. It does so
// on any update of a public or SSO-enabled committee that has no name yet, not only when
// public is turned on, so republishing an already-public committee can also trigger it.
func willReserveSSOGroupName(existing, updated *model.CommitteeBase) bool {
	return (updated.SSOGroupEnabled || updated.Public) && existing.SSOGroupName == ""
}

func fieldNamesOfDiffs(diffs []fieldDiff) []string {
	names := make([]string, len(diffs))
	for i, d := range diffs {
		names[i] = d.field.name
	}
	return names
}
