// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// editableBrief returns a generated brief for the test window with a known
// revision and source refs — the shape a chair would edit via PUT /current.
func editableBrief() *model.GroupWeeklyBrief {
	winStart, winEnd := model.WeeklyWindow(testNow)
	return &model.GroupWeeklyBrief{
		UID:          "b-1",
		CommitteeUID: "c-1",
		WindowStart:  winStart,
		WindowEnd:    winEnd,
		State:        model.GroupWeeklyBriefStateGenerated,
		BriefText:    "original body",
		SourceRefs: []model.SourceRef{
			{Kind: "meeting", ID: "m-1", Title: "Sync", Excerpt: "notes"},
		},
		Revision: 5,
	}
}

func TestUpdate_Success_TransitionsToEditedAndBumpsRevision(t *testing.T) {
	br := &fakeBriefReader{brief: editableBrief()}
	bw := &fakeBriefWriter{}
	w := NewGroupWeeklyBriefWriterOrchestrator(
		WithGroupWeeklyBriefReaderForWriter(br),
		WithGroupWeeklyBriefWriterForWriter(bw),
	)

	updated, err := w.Update(context.Background(), GroupWeeklyBriefUpdateInput{
		CommitteeUID: "c-1",
		BriefText:    "edited body",
		Revision:     5,
		EditedBy:     "alice",
		Now:          testNow,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	// State moves to "edited"; the new text is written.
	assert.Equal(t, model.GroupWeeklyBriefStateEdited, updated.State)
	assert.Equal(t, "edited body", updated.BriefText)
	// Audit fields are recorded from the input.
	assert.Equal(t, "alice", updated.LastEditedBy)
	assert.Equal(t, testNow.UTC(), updated.LastEditedAt)
	// source_refs are preserved untouched.
	require.Len(t, updated.SourceRefs, 1)
	assert.Equal(t, "m-1", updated.SourceRefs[0].ID)
	// Exactly one CAS write happened and the revision advanced (fake bumps it).
	assert.EqualValues(t, 1, bw.briefPutCount.Load())
	assert.Equal(t, uint64(6), updated.Revision)
}

func TestUpdate_NoBriefForWindow_ReturnsNotFound(t *testing.T) {
	br := &fakeBriefReader{brief: nil}
	bw := &fakeBriefWriter{}
	w := NewGroupWeeklyBriefWriterOrchestrator(
		WithGroupWeeklyBriefReaderForWriter(br),
		WithGroupWeeklyBriefWriterForWriter(bw),
	)

	_, err := w.Update(context.Background(), GroupWeeklyBriefUpdateInput{
		CommitteeUID: "c-1",
		BriefText:    "edited body",
		Revision:     1,
		Now:          testNow,
	})
	require.Error(t, err)
	var nf errors.NotFound
	require.ErrorAs(t, err, &nf)
	// No write attempted when there is nothing to edit.
	assert.EqualValues(t, 0, bw.briefPutCount.Load())
}

func TestUpdate_StaleRevision_ReturnsRevisionMismatchWithCurrent(t *testing.T) {
	br := &fakeBriefReader{brief: editableBrief()} // current revision is 5
	bw := &fakeBriefWriter{}
	w := NewGroupWeeklyBriefWriterOrchestrator(
		WithGroupWeeklyBriefReaderForWriter(br),
		WithGroupWeeklyBriefWriterForWriter(bw),
	)

	_, err := w.Update(context.Background(), GroupWeeklyBriefUpdateInput{
		CommitteeUID: "c-1",
		BriefText:    "edited body",
		Revision:     4, // stale
		Now:          testNow,
	})
	require.Error(t, err)
	var rm errors.RevisionMismatch
	require.ErrorAs(t, err, &rm)
	// The conflict carries the current server-side revision for the client to refetch.
	assert.Equal(t, uint64(5), rm.Revision)
	// No CAS write on a stale token.
	assert.EqualValues(t, 0, bw.briefPutCount.Load())
}

func TestUpdate_EmptyBriefText_ReturnsValidation(t *testing.T) {
	br := &fakeBriefReader{brief: editableBrief()}
	bw := &fakeBriefWriter{}
	w := NewGroupWeeklyBriefWriterOrchestrator(
		WithGroupWeeklyBriefReaderForWriter(br),
		WithGroupWeeklyBriefWriterForWriter(bw),
	)

	_, err := w.Update(context.Background(), GroupWeeklyBriefUpdateInput{
		CommitteeUID: "c-1",
		BriefText:    "   ", // whitespace-only is empty
		Revision:     5,
		Now:          testNow,
	})
	require.Error(t, err)
	var v errors.Validation
	require.ErrorAs(t, err, &v)
	// Validation happens before any storage interaction.
	assert.EqualValues(t, 0, bw.briefPutCount.Load())
}

// revShiftReader returns base on every call, but bumps the revision to
// secondRev from the second call onward — simulating a concurrent edit that
// lands between the orchestrator's pre-check read and its CAS write.
type revShiftReader struct {
	base      *model.GroupWeeklyBrief
	secondRev uint64
	calls     int
}

func (r *revShiftReader) GetGroupWeeklyBriefForWindow(_ context.Context, _ string, _ model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	r.calls++
	cp := *r.base
	if r.calls >= 2 {
		cp.Revision = r.secondRev
	}
	return &cp, nil, nil
}

func TestUpdate_CASConflictReReadsAndReturns409(t *testing.T) {
	// Pre-check passes at revision 5, the CAS write fails with the storage's
	// generic 503, and a re-read shows the revision moved to 6 → a concurrent
	// edit. The orchestrator must translate this into a 409 carrying revision 6
	// rather than leaking the transient 503.
	rd := &revShiftReader{base: editableBrief(), secondRev: 6} // editableBrief() is revision 5
	bw := &fakeBriefWriter{putErr: errors.NewServiceUnavailable("weekly brief CAS conflict — retry")}
	w := NewGroupWeeklyBriefWriterOrchestrator(
		WithGroupWeeklyBriefReaderForWriter(rd),
		WithGroupWeeklyBriefWriterForWriter(bw),
	)

	_, err := w.Update(context.Background(), GroupWeeklyBriefUpdateInput{
		CommitteeUID: "c-1",
		BriefText:    "edited body",
		Revision:     5,
		Now:          testNow,
	})
	require.Error(t, err)
	var rm errors.RevisionMismatch
	require.ErrorAs(t, err, &rm)
	assert.Equal(t, uint64(6), rm.Revision)
	// One CAS write was attempted; the disambiguation re-read makes it 2 reads.
	assert.EqualValues(t, 1, bw.briefPutCount.Load())
	assert.Equal(t, 2, rd.calls)
}

func TestUpdate_WriteErrorWithoutRevisionShift_PropagatesOriginal(t *testing.T) {
	// The CAS write fails but the re-read shows the same revision — this is a
	// genuine infrastructure failure, not a conflict. The orchestrator must
	// propagate the original error (503) unchanged, not fabricate a 409.
	rd := &revShiftReader{base: editableBrief(), secondRev: 5} // unchanged at 5
	bw := &fakeBriefWriter{putErr: errors.NewServiceUnavailable("nats unavailable")}
	w := NewGroupWeeklyBriefWriterOrchestrator(
		WithGroupWeeklyBriefReaderForWriter(rd),
		WithGroupWeeklyBriefWriterForWriter(bw),
	)

	_, err := w.Update(context.Background(), GroupWeeklyBriefUpdateInput{
		CommitteeUID: "c-1",
		BriefText:    "edited body",
		Revision:     5,
		Now:          testNow,
	})
	require.Error(t, err)
	var su errors.ServiceUnavailable
	require.ErrorAs(t, err, &su)
	var rm errors.RevisionMismatch
	assert.NotErrorAs(t, err, &rm, "must not fabricate a 409 on a genuine infra failure")
}
