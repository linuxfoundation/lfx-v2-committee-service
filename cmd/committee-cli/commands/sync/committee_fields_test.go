// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// fieldsWriter records the base and settings passed to Update and UpdateSettings.
// conflicts is the number of leading Update calls that fail with a revision conflict.
type fieldsWriter struct {
	stubCommitteeWriter
	bases            []*model.Committee
	baseRevisions    []uint64
	settings         []*model.CommitteeSettings
	settingsRevision []uint64
	conflicts        int
	settingsErr      error
}

func (w *fieldsWriter) Update(_ context.Context, committee *model.Committee, revision uint64, _ bool) (*model.Committee, error) {
	w.bases = append(w.bases, committee)
	w.baseRevisions = append(w.baseRevisions, revision)
	if w.conflicts > 0 {
		w.conflicts--
		return nil, errs.NewConflict("committee has been modified by another process")
	}
	return committee, nil
}

func (w *fieldsWriter) UpdateSettings(_ context.Context, settings *model.CommitteeSettings, revision uint64, _ bool) (*model.CommitteeSettings, error) {
	w.settings = append(w.settings, settings)
	w.settingsRevision = append(w.settingsRevision, revision)
	if w.settingsErr != nil {
		return nil, w.settingsErr
	}
	return settings, nil
}

// fakeV1Reader returns canned v1 records by v2 committee UID.
type fakeV1Reader struct {
	records map[string]map[string]any
	err     map[string]error
}

func (r *fakeV1Reader) GetV1Committee(_ context.Context, uid string) (map[string]any, error) {
	if err, ok := r.err[uid]; ok {
		return nil, err
	}
	record, ok := r.records[uid]
	if !ok {
		return nil, errs.NewNotFound("no v1 record for " + uid)
	}
	return record, nil
}

func fieldsBase(uid string) *model.CommitteeBase {
	return &model.CommitteeBase{
		UID:         uid,
		ProjectUID:  "project-1",
		Name:        "First Last Committee",
		Category:    "Technical Steering Committee",
		Description: "Committee description",
		MailingList: strPtr("list@example.com"),
	}
}

func strPtr(s string) *string { return &s }

// fieldsReader reports a settings revision, which the shared mockReader always leaves at 0.
type fieldsReader struct {
	*mockReader
	settingsRevision uint64
}

func (r *fieldsReader) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {
	settings, _, err := r.mockReader.GetSettings(ctx, uid)
	return settings, r.settingsRevision, err
}

type fieldsFixture struct {
	reader *fieldsReader
	v1     *fakeV1Reader
	writer *fieldsWriter
}

// newFieldsFixture seeds committee-1 with a v2 base (revision 7), v2 settings (revision 3) and a
// v1 record.
func newFieldsFixture(base *model.CommitteeBase, settings *model.CommitteeSettings, v1 map[string]any) *fieldsFixture {
	reader := &mockReader{
		bases:    map[string]*model.CommitteeBase{base.UID: base},
		revision: map[string]uint64{base.UID: 7},
		settings: map[string]*model.CommitteeSettings{},
	}
	if settings != nil {
		reader.settings[base.UID] = settings
	}
	return &fieldsFixture{
		reader: &fieldsReader{mockReader: reader, settingsRevision: 3},
		v1:     &fakeV1Reader{records: map[string]map[string]any{base.UID: v1}},
		writer: &fieldsWriter{},
	}
}

func (f *fieldsFixture) run(args ...string) error {
	rc := commands.RunContext{
		CommitteeReader:             f.reader,
		CommitteeWriterOrchestrator: f.writer,
		Args:                        args,
	}
	return (&committeeFieldsSubcommand{v1: f.v1}).Run(context.Background(), rc)
}

func TestCommitteeFields_DryRunByDefaultDoesNotWrite(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": true})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1"))
	assert.Empty(t, f.writer.bases, "dry-run is the default and must not write")
}

func TestCommitteeFields_FromV1CopiesOnlyTheChosenFields(t *testing.T) {
	base := fieldsBase("committee-1")
	f := newFieldsFixture(base, nil, map[string]any{
		"public_enabled":   true,
		"public_name":      "Display Name",
		"enable_voting__c": true, // differs, but not selected
	})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public,display_name", "--from=v1", "--dry-run=false"))

	require.Len(t, f.writer.bases, 1)
	want := *base
	want.Public = true
	want.DisplayName = "Display Name"
	assert.Equal(t, want, f.writer.bases[0].CommitteeBase, "only the chosen fields change")
	assert.Equal(t, uint64(7), f.writer.baseRevisions[0], "must pass the revision it read")
	assert.Empty(t, f.writer.settings)
}

func TestCommitteeFields_FromV1SkipsFieldsThatAlreadyMatch(t *testing.T) {
	base := fieldsBase("committee-1")
	base.Public = true
	f := newFieldsFixture(base, nil, map[string]any{"public_enabled": true})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1", "--dry-run=false"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_FromV1MissingV1KeyIsTheZeroValue(t *testing.T) {
	base := fieldsBase("committee-1")
	base.Public = true
	f := newFieldsFixture(base, nil, map[string]any{})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1", "--dry-run=false"))

	require.Len(t, f.writer.bases, 1)
	assert.False(t, f.writer.bases[0].Public, "an absent v1 public_enabled reads as false, as v1 does")
}

func TestCommitteeFields_FromV1ClearsAnOptionalTextField(t *testing.T) {
	base := fieldsBase("committee-1")
	base.ChatChannel = strPtr("https://example.com/chat")
	f := newFieldsFixture(base, nil, map[string]any{"chat_channel": nil})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=chat_channel", "--from=v1", "--dry-run=false"))

	require.Len(t, f.writer.bases, 1)
	assert.Nil(t, f.writer.bases[0].ChatChannel)
}

func TestCommitteeFields_BusinessEmailRequiredGoesThroughSettings(t *testing.T) {
	base := fieldsBase("committee-1")
	settings := &model.CommitteeSettings{UID: "committee-1", BusinessEmailRequired: true, MemberVisibility: "hidden"}
	f := newFieldsFixture(base, settings, map[string]any{"business_email_required__c": false})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=business_email_required", "--from=v1", "--dry-run=false"))

	assert.Empty(t, f.writer.bases, "a settings-only change must not rewrite the base")
	require.Len(t, f.writer.settings, 1)
	assert.False(t, f.writer.settings[0].BusinessEmailRequired)
	assert.Equal(t, "hidden", f.writer.settings[0].MemberVisibility, "other settings are carried through")
	assert.Equal(t, uint64(3), f.writer.settingsRevision[0])
	assert.True(t, settings.BusinessEmailRequired, "the stored settings value must not be mutated in place")
}

func TestCommitteeFields_MissingSettingsFails(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"business_email_required__c": true})

	require.Error(t, f.run("--committee-uid=committee-1", "--field=business_email_required", "--from=v1", "--dry-run=false"))
	assert.Empty(t, f.writer.settings)
}

func TestCommitteeFields_BaseAndSettingsFieldsTogether(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), &model.CommitteeSettings{UID: "committee-1"},
		map[string]any{"enable_voting__c": true, "business_email_required__c": true})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=enable_voting,business_email_required", "--from=v1", "--dry-run=false"))

	require.Len(t, f.writer.bases, 1)
	assert.True(t, f.writer.bases[0].EnableVoting)
	require.Len(t, f.writer.settings, 1)
	assert.True(t, f.writer.settings[0].BusinessEmailRequired)
}

func TestCommitteeFields_SettingsFailureAfterBaseUpdateIsReported(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), &model.CommitteeSettings{UID: "committee-1"},
		map[string]any{"enable_voting__c": true, "business_email_required__c": true})
	f.writer.settingsErr = errs.NewUnexpected("boom")

	err := f.run("--committee-uid=committee-1", "--field=enable_voting,business_email_required", "--from=v1", "--dry-run=false")

	require.Error(t, err)
	assert.Len(t, f.writer.bases, 1)
}

func TestCommitteeFields_FromV2RepublishesWithoutChangingTheCommittee(t *testing.T) {
	base := fieldsBase("committee-1")
	base.Public = true
	base.DisplayName = "Display Name"
	f := newFieldsFixture(base, nil, map[string]any{"public_enabled": false, "public_name": ""})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public,display_name", "--from=v2", "--dry-run=false"))

	require.Len(t, f.writer.bases, 1)
	assert.Equal(t, *base, f.writer.bases[0].CommitteeBase, "a republish must not change any field")
	assert.Equal(t, uint64(7), f.writer.baseRevisions[0])
}

func TestCommitteeFields_FromV2DoesNothingWhenV1AlreadyMatches(t *testing.T) {
	base := fieldsBase("committee-1")
	base.Public = true
	f := newFieldsFixture(base, nil, map[string]any{"public_enabled": true})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v2", "--dry-run=false"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_FromV2RefusesToPushAnEmptyTextValue(t *testing.T) {
	base := fieldsBase("committee-1") // display_name is empty in v2
	f := newFieldsFixture(base, nil, map[string]any{"public_name": "Display Name"})

	err := f.run("--committee-uid=committee-1", "--field=display_name", "--from=v2", "--dry-run=false")

	require.Error(t, err)
	assert.Empty(t, f.writer.bases, "an empty value cannot clear v1, so the republish must not run")
}

func TestCommitteeFields_FromV2DryRunDoesNotWrite(t *testing.T) {
	base := fieldsBase("committee-1")
	base.Public = true
	f := newFieldsFixture(base, nil, map[string]any{"public_enabled": false})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v2"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_HandlesEachCommitteeIndependently(t *testing.T) {
	baseA, baseB := fieldsBase("committee-a"), fieldsBase("committee-b")
	reader := &mockReader{
		bases:    map[string]*model.CommitteeBase{"committee-a": baseA, "committee-b": baseB},
		revision: map[string]uint64{"committee-a": 1, "committee-b": 2},
	}
	writer := &fieldsWriter{}
	v1 := &fakeV1Reader{records: map[string]map[string]any{
		"committee-a": {"public_enabled": true, "public_name": "Name A"},
		"committee-b": {"public_enabled": true, "public_name": "Name B"},
	}}
	rc := commands.RunContext{
		CommitteeReader: reader, CommitteeWriterOrchestrator: writer,
		Args: []string{"--committee-uid=committee-a", "--committee-uid=committee-b", "--field=public,display_name", "--from=v1", "--dry-run=false"},
	}

	require.NoError(t, (&committeeFieldsSubcommand{v1: v1}).Run(context.Background(), rc))

	require.Len(t, writer.bases, 2)
	assert.Equal(t, "Name A", writer.bases[0].DisplayName)
	assert.Equal(t, "Name B", writer.bases[1].DisplayName)
}

func TestCommitteeFields_OneFailureDoesNotStopTheRest(t *testing.T) {
	baseB := fieldsBase("committee-b")
	reader := &mockReader{
		bases:    map[string]*model.CommitteeBase{"committee-b": baseB},
		revision: map[string]uint64{"committee-b": 2},
		baseErr:  map[string]error{"committee-a": errs.NewNotFound("committee not found")},
	}
	writer := &fieldsWriter{}
	v1 := &fakeV1Reader{records: map[string]map[string]any{"committee-b": {"public_enabled": true}}}
	rc := commands.RunContext{
		CommitteeReader: reader, CommitteeWriterOrchestrator: writer,
		Args: []string{"--committee-uid=committee-a,committee-b", "--field=public", "--from=v1", "--dry-run=false"},
	}

	err := (&committeeFieldsSubcommand{v1: v1}).Run(context.Background(), rc)

	require.Error(t, err, "a failed committee makes the run fail")
	require.Len(t, writer.bases, 1, "the other committee is still reconciled")
	assert.Equal(t, "committee-b", writer.bases[0].CommitteeBase.UID)
}

func TestCommitteeFields_MissingV1RecordFails(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, nil)
	f.v1.records = map[string]map[string]any{}

	require.Error(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1", "--dry-run=false"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_UnexpectedV1TypeFails(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": "yes"})

	require.Error(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1", "--dry-run=false"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_RetriesRevisionConflict(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": true})
	f.writer.conflicts = 2

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1", "--dry-run=false"))
	assert.Len(t, f.writer.bases, 3, "two conflicts then a success")
}

func TestCommitteeFields_GivesUpAfterRepeatedConflicts(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": true})
	f.writer.conflicts = 10

	require.Error(t, f.run("--committee-uid=committee-1", "--field=public", "--from=v1", "--dry-run=false"))
	assert.Len(t, f.writer.bases, 3)
}

func TestCommitteeFields_ArgumentValidation(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{"missing from", []string{"--committee-uid=committee-1", "--field=public"}, "--from"},
		{"invalid from", []string{"--committee-uid=committee-1", "--field=public", "--from=v3"}, "--from"},
		{"missing committee", []string{"--field=public", "--from=v1"}, "--committee-uid"},
		{"missing field", []string{"--committee-uid=committee-1", "--from=v1"}, "--field"},
		{"unknown field", []string{"--committee-uid=committee-1", "--field=nope", "--from=v1"}, "supported fields"},
		{"website not supported from v1", []string{"--committee-uid=committee-1", "--field=website", "--from=v1"}, "#950"},
		{"category not supported from v2", []string{"--committee-uid=committee-1", "--field=category", "--from=v2"}, "#948"},
		{"enable_voting not supported from v2", []string{"--committee-uid=committee-1", "--field=enable_voting", "--from=v2"}, "enable_voting"},
		{"business_email_required not supported from v2", []string{"--committee-uid=committee-1", "--field=business_email_required", "--from=v2"}, "business_email_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": true})

			err := f.run(append(tt.args, "--dry-run=false")...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMessage)
			assert.Empty(t, f.writer.bases, "invalid arguments must never write")
		})
	}
}

func TestCommitteeFields_ValidatesAllFieldsBeforeWritingAnything(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": true})

	require.Error(t, f.run("--committee-uid=committee-1", "--field=public,nope", "--from=v1", "--dry-run=false"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_ListFieldsDoesNotNeedAnyOtherArgument(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{})

	require.NoError(t, f.run("--list-fields"))
	assert.Empty(t, f.writer.bases)
}

func TestCommitteeFields_RunsOfflineOnlyForListFields(t *testing.T) {
	s := &committeeFieldsSubcommand{}

	assert.True(t, s.RunsOffline([]string{"--list-fields"}))
	assert.True(t, s.RunsOffline([]string{"-list-fields"}))
	assert.True(t, s.RunsOffline([]string{"--list-fields=true"}))
	assert.True(t, s.RunsOffline([]string{"--committee-uid=a", "--list-fields"}), "position does not matter")

	assert.False(t, s.RunsOffline(nil))
	assert.False(t, s.RunsOffline([]string{"--committee-uid=a", "--field=public", "--from=v1"}))
	assert.False(t, s.RunsOffline([]string{"--list-fields=false"}))
}

func TestCommitteeFields_FailsWhenNoV1SourceIsWired(t *testing.T) {
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_enabled": true})
	rc := commands.RunContext{
		CommitteeReader:             f.reader,
		CommitteeWriterOrchestrator: f.writer,
		Args:                        []string{"--committee-uid=committee-1", "--field=public", "--from=v1"},
	}

	// No fake v1 reader and no KVStore: the command cannot reach the v1 mirror.
	err := (&committeeFieldsSubcommand{}).Run(context.Background(), rc)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "KVStore is not wired")
	assert.Empty(t, f.writer.bases)
}

func TestWriteFieldList_ListsEveryFieldAndDirection(t *testing.T) {
	var out bytes.Buffer
	writeFieldList(&out)
	text := out.String()

	for name := range committeeFields {
		assert.Contains(t, text, name)
	}
	assert.Contains(t, text, "NOT supported")
	assert.Contains(t, text, "sync helper PATCH v1 with all the v2 fields it maps", "the footer must warn about collateral v1 changes")
	assert.Contains(t, text, "#950")
	assert.True(t, strings.Contains(text, "--from=v1") && strings.Contains(text, "--from=v2"))
}

func TestWillReserveSSOGroupName(t *testing.T) {
	tests := []struct {
		name     string
		existing model.CommitteeBase
		updated  model.CommitteeBase
		want     bool
	}{
		{"public with no SSO group name", model.CommitteeBase{Public: true}, model.CommitteeBase{Public: true}, true},
		{"public turned on with no SSO group name", model.CommitteeBase{}, model.CommitteeBase{Public: true}, true},
		{"SSO enabled with no SSO group name", model.CommitteeBase{}, model.CommitteeBase{SSOGroupEnabled: true}, true},
		{"public with an SSO group name", model.CommitteeBase{Public: true, SSOGroupName: "project-committee"}, model.CommitteeBase{Public: true, SSOGroupName: "project-committee"}, false},
		{"not public and not SSO enabled", model.CommitteeBase{}, model.CommitteeBase{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, willReserveSSOGroupName(&tt.existing, &tt.updated))
		})
	}
}

func TestStringList_AcceptsCommaSeparatedAndRepeatedValues(t *testing.T) {
	var l stringList
	require.NoError(t, l.Set("a, b,,c"))
	require.NoError(t, l.Set("d"))
	assert.Equal(t, []string{"a", "b", "c", "d"}, []string(l))
	assert.Equal(t, []string{"a", "b"}, dedupe([]string{"a", "b", "a"}))
}

func TestSummarizeValue_TruncatesLongValues(t *testing.T) {
	assert.Equal(t, "short", summarizeValue("short"))
	long := strings.Repeat("x", 200)
	got := summarizeValue(long)
	assert.Less(t, len(got), len(long))
	assert.Contains(t, got, "200 chars")
}

func TestSummarizeValue_DoesNotSplitMultiByteCharacters(t *testing.T) {
	// 100 three-byte characters: a byte slice at 80 would cut one in half.
	long := strings.Repeat("é", 100)
	got := summarizeValue(long)
	assert.True(t, utf8.ValidString(got), "truncation must keep the text valid UTF-8")
	assert.Contains(t, got, "100 chars")

	atLimit := strings.Repeat("é", maxLoggedValueLen)
	assert.Equal(t, atLimit, summarizeValue(atLimit), "a value at the limit is kept whole")
}

func TestCommitteeFields_FromV1RejectsValuesOverTheAPILimit(t *testing.T) {
	tests := []struct {
		field, v1Key string
		limit        int
	}{
		{"description", "description__c", 2000},
		{"display_name", "public_name", 100},
		{"chat_channel", "chat_channel", 500},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{tt.v1Key: strings.Repeat("x", tt.limit+1)})

			err := f.run("--committee-uid=committee-1", "--field="+tt.field, "--from=v1", "--dry-run=false")
			require.Error(t, err)
			assert.Empty(t, f.writer.bases, "an over-limit value must not be written")

			// Dry-run reports the same failure, so the operator sees it before writing.
			dry := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{tt.v1Key: strings.Repeat("x", tt.limit+1)})
			require.Error(t, dry.run("--committee-uid=committee-1", "--field="+tt.field, "--from=v1"))
		})
	}
}

func TestCommitteeFields_FromV1AcceptsValuesAtTheAPILimit(t *testing.T) {
	// Counted in characters, not bytes: 100 two-byte characters is 200 bytes.
	f := newFieldsFixture(fieldsBase("committee-1"), nil, map[string]any{"public_name": strings.Repeat("é", 100)})

	require.NoError(t, f.run("--committee-uid=committee-1", "--field=display_name", "--from=v1", "--dry-run=false"))
	require.Len(t, f.writer.bases, 1)
	assert.Equal(t, strings.Repeat("é", 100), f.writer.bases[0].DisplayName)
}
