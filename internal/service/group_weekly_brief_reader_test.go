// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// fakeGroupWeeklyBriefReader is an in-memory stand-in for the persistence
// port; it captures the (committeeUID, window) the orchestrator passes and
// returns canned responses.
type fakeGroupWeeklyBriefReader struct {
	gotCommittee string
	gotWindow    model.GroupWeeklyBrief

	brief    *model.GroupWeeklyBrief
	throttle []byte
	err      error
}

func (f *fakeGroupWeeklyBriefReader) GetGroupWeeklyBriefForWindow(_ context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	f.gotCommittee = committeeUID
	f.gotWindow = window
	return f.brief, f.throttle, f.err
}

func TestGroupWeeklyBriefReaderOrchestrator_GetCurrent_Hit(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) // Wednesday
	wantStart, wantEnd := model.WeeklyWindow(now)

	canned := &model.GroupWeeklyBrief{
		UID:          "b-1",
		CommitteeUID: "c-1",
		WindowStart:  wantStart,
		WindowEnd:    wantEnd,
		State:        model.GroupWeeklyBriefStateGenerated,
	}
	fake := &fakeGroupWeeklyBriefReader{brief: canned}

	orch := NewGroupWeeklyBriefReaderOrchestrator(WithGroupWeeklyBriefReader(fake))
	brief, throttle, err := orch.GetCurrent(context.Background(), "c-1", now)
	require.NoError(t, err)
	require.NotNil(t, brief)
	assert.Equal(t, canned, brief)
	assert.Nil(t, throttle)

	// Orchestrator must derive the correct UTC Sun→Sat window.
	assert.Equal(t, "c-1", fake.gotCommittee)
	assert.True(t, fake.gotWindow.WindowStart.Equal(wantStart))
	assert.True(t, fake.gotWindow.WindowEnd.Equal(wantEnd))
}

func TestGroupWeeklyBriefReaderOrchestrator_GetCurrent_Miss(t *testing.T) {
	fake := &fakeGroupWeeklyBriefReader{} // brief=nil → miss
	orch := NewGroupWeeklyBriefReaderOrchestrator(WithGroupWeeklyBriefReader(fake))
	brief, throttle, err := orch.GetCurrent(context.Background(), "c-1", time.Now().UTC())
	require.NoError(t, err)
	assert.Nil(t, brief)
	assert.Nil(t, throttle)
}

func TestGroupWeeklyBriefReaderOrchestrator_GetCurrent_Error(t *testing.T) {
	want := errors.New("boom")
	fake := &fakeGroupWeeklyBriefReader{err: want}
	orch := NewGroupWeeklyBriefReaderOrchestrator(WithGroupWeeklyBriefReader(fake))
	_, _, err := orch.GetCurrent(context.Background(), "c-1", time.Now().UTC())
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}
