// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Backfill-test fakes
// ─────────────────────────────────────────────────────────────────────────────

type fakeBackfillBriefReader struct {
	keys    []string
	keysErr error
	// briefs maps "{committee_uid}.{yyyymmdd}" → brief
	briefs map[string]*model.GroupWeeklyBrief
}

func (r *fakeBackfillBriefReader) GetGroupWeeklyBriefForWindow(_ context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	key := committeeUID + "." + model.WindowDateKey(window.WindowStart)
	return r.briefs[key], nil, nil
}

func (r *fakeBackfillBriefReader) ListGroupWeeklyBriefIndexKeys(_ context.Context) ([]string, error) {
	return r.keys, r.keysErr
}

type fakeBackfillPublisher struct {
	subjects []string
	callErr  error
}

func (p *fakeBackfillPublisher) Indexer(_ context.Context, subject string, _ any, _ bool) error {
	p.subjects = append(p.subjects, subject)
	return p.callErr
}
func (p *fakeBackfillPublisher) UpdateAccess(_ context.Context, _ any) error            { return nil }
func (p *fakeBackfillPublisher) DeleteAccess(_ context.Context, _ any) error            { return nil }
func (p *fakeBackfillPublisher) MemberPut(_ context.Context, _ any) error               { return nil }
func (p *fakeBackfillPublisher) MemberRemove(_ context.Context, _ any) error            { return nil }
func (p *fakeBackfillPublisher) Event(_ context.Context, _ string, _ any, _ bool) error { return nil }

// briefAt builds a brief keyed by the supplied committee UID and YYYYMMDD date
// string for injection into fakeBackfillBriefReader.briefs.
func briefAt(uid, committeeUID, yyyymmdd string, state model.GroupWeeklyBriefState) (*model.GroupWeeklyBrief, string) {
	t, _ := time.Parse("20060102", yyyymmdd)
	key := committeeUID + "." + yyyymmdd
	return &model.GroupWeeklyBrief{
		UID:          uid,
		CommitteeUID: committeeUID,
		WindowStart:  t.UTC(),
		State:        state,
	}, key
}

func newBriefBackfillRC(reader *fakeBackfillBriefReader, pub *fakeBackfillPublisher, args []string) commands.RunContext {
	return commands.RunContext{
		GroupWeeklyBriefReader: reader,
		Publisher:              pub,
		Args:                   args,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestBackfillWeeklyBriefIndex_AllStatesAreIndexed(t *testing.T) {
	states := []model.GroupWeeklyBriefState{
		model.GroupWeeklyBriefStateEmpty,
		model.GroupWeeklyBriefStateGenerating,
		model.GroupWeeklyBriefStateGenerated,
		model.GroupWeeklyBriefStateEdited,
		model.GroupWeeklyBriefStateApproved,
		model.GroupWeeklyBriefStateError,
	}

	briefMap := make(map[string]*model.GroupWeeklyBrief)
	var keys []string
	for i, state := range states {
		b, k := briefAt(
			"brief-"+string(rune('a'+i)),
			"cmt-1",
			"2026050"+string(rune('1'+i)),
			state,
		)
		briefMap[k] = b
		keys = append(keys, k)
	}

	reader := &fakeBackfillBriefReader{keys: keys, briefs: briefMap}
	pub := &fakeBackfillPublisher{}
	sub := &backfillWeeklyBriefIndexSubcommand{}

	err := sub.Run(context.Background(), newBriefBackfillRC(reader, pub, nil))
	require.NoError(t, err)
	assert.Len(t, pub.subjects, len(states), "every brief should be indexed regardless of state")
	for _, subj := range pub.subjects {
		assert.Equal(t, constants.IndexGroupWeeklyBriefSubject, subj)
	}
}

func TestBackfillWeeklyBriefIndex_CommitteeUIDFilter(t *testing.T) {
	b1, k1 := briefAt("b1", "cmt-A", "20260501", model.GroupWeeklyBriefStateGenerated)
	b2, k2 := briefAt("b2", "cmt-B", "20260501", model.GroupWeeklyBriefStateGenerated)

	reader := &fakeBackfillBriefReader{
		keys:   []string{k1, k2},
		briefs: map[string]*model.GroupWeeklyBrief{k1: b1, k2: b2},
	}
	pub := &fakeBackfillPublisher{}
	sub := &backfillWeeklyBriefIndexSubcommand{}

	err := sub.Run(context.Background(), newBriefBackfillRC(reader, pub, []string{"--committee-uid=cmt-A"}))
	require.NoError(t, err)
	assert.Len(t, pub.subjects, 1, "only the matching committee brief should be indexed")
}

func TestBackfillWeeklyBriefIndex_DryRunSkipsPublish(t *testing.T) {
	b, k := briefAt("b1", "cmt-1", "20260501", model.GroupWeeklyBriefStateGenerated)
	reader := &fakeBackfillBriefReader{
		keys:   []string{k},
		briefs: map[string]*model.GroupWeeklyBrief{k: b},
	}
	pub := &fakeBackfillPublisher{}
	sub := &backfillWeeklyBriefIndexSubcommand{}

	err := sub.Run(context.Background(), newBriefBackfillRC(reader, pub, []string{"--dry-run"}))
	require.NoError(t, err)
	assert.Empty(t, pub.subjects, "dry-run must not publish")
}

func TestBackfillWeeklyBriefIndex_PublishErrorCountsAsFailed(t *testing.T) {
	b, k := briefAt("b1", "cmt-1", "20260501", model.GroupWeeklyBriefStateGenerated)
	reader := &fakeBackfillBriefReader{
		keys:   []string{k},
		briefs: map[string]*model.GroupWeeklyBrief{k: b},
	}
	pub := &fakeBackfillPublisher{callErr: errs.NewUnexpected("nats down")}
	sub := &backfillWeeklyBriefIndexSubcommand{}

	err := sub.Run(context.Background(), newBriefBackfillRC(reader, pub, nil))
	require.Error(t, err, "a publish failure should surface as a run error")
}

func TestBackfillWeeklyBriefIndex_EmptyBucket_Succeeds(t *testing.T) {
	reader := &fakeBackfillBriefReader{keys: nil}
	pub := &fakeBackfillPublisher{}
	sub := &backfillWeeklyBriefIndexSubcommand{}

	err := sub.Run(context.Background(), newBriefBackfillRC(reader, pub, nil))
	require.NoError(t, err)
	assert.Empty(t, pub.subjects)
}

func TestBackfillWeeklyBriefIndex_MissingBriefIsSkipped(t *testing.T) {
	// Index key exists but no brief in the main bucket — stale index.
	reader := &fakeBackfillBriefReader{
		keys:   []string{"cmt-1.20260501"},
		briefs: map[string]*model.GroupWeeklyBrief{}, // deliberately empty
	}
	pub := &fakeBackfillPublisher{}
	sub := &backfillWeeklyBriefIndexSubcommand{}

	err := sub.Run(context.Background(), newBriefBackfillRC(reader, pub, nil))
	require.NoError(t, err)
	assert.Empty(t, pub.subjects, "stale index key with no backing brief should be skipped, not indexed")
}

func TestParseIndexKey_Valid(t *testing.T) {
	cases := []struct {
		key           string
		wantCommittee string
		wantDate      string
	}{
		{
			key:           "abc-123.20260517",
			wantCommittee: "abc-123",
			wantDate:      "20260517",
		},
		{
			key:           "a.b.c.20260101",
			wantCommittee: "a.b.c",
			wantDate:      "20260101",
		},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			committee, windowStart, err := parseIndexKey(tc.key)
			require.NoError(t, err)
			assert.Equal(t, tc.wantCommittee, committee)
			assert.Equal(t, tc.wantDate, windowStart.UTC().Format("20060102"))
		})
	}
}

func TestParseIndexKey_Invalid(t *testing.T) {
	cases := []string{
		"nodot",
		"nodatepart.",
		"abc.2026051",   // 7 chars not 8
		"abc.202605170", // 9 chars not 8
		"abc.notadate",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			_, _, err := parseIndexKey(key)
			require.Error(t, err)
		})
	}
}
