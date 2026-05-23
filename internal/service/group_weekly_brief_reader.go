// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// GroupWeeklyBriefDataReader is the use-case façade for reading working-group
// weekly briefs. Phase 1 only exposes the current-window read; later phases
// will add list/history reads here.
type GroupWeeklyBriefDataReader interface {
	// GetCurrent returns the brief and throttle bytes for the most recently
	// completed UTC weekly window. A miss yields (nil, nil, nil).
	GetCurrent(ctx context.Context, committeeUID string, now time.Time) (*model.GroupWeeklyBrief, []byte, error)
}

type groupWeeklyBriefReaderOrchestrator struct {
	reader port.GroupWeeklyBriefReader
}

// GroupWeeklyBriefReaderOption configures the orchestrator.
type GroupWeeklyBriefReaderOption func(*groupWeeklyBriefReaderOrchestrator)

// WithGroupWeeklyBriefReader injects the persistence-layer reader.
func WithGroupWeeklyBriefReader(r port.GroupWeeklyBriefReader) GroupWeeklyBriefReaderOption {
	return func(o *groupWeeklyBriefReaderOrchestrator) {
		o.reader = r
	}
}

// NewGroupWeeklyBriefReaderOrchestrator builds a GroupWeeklyBriefDataReader.
func NewGroupWeeklyBriefReaderOrchestrator(opts ...GroupWeeklyBriefReaderOption) GroupWeeklyBriefDataReader {
	o := &groupWeeklyBriefReaderOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.reader == nil {
		panic("group weekly brief reader is required")
	}
	return o
}

func (o *groupWeeklyBriefReaderOrchestrator) GetCurrent(ctx context.Context, committeeUID string, now time.Time) (*model.GroupWeeklyBrief, []byte, error) {
	start, end := model.WeeklyWindow(now)
	window := model.GroupWeeklyBrief{WindowStart: start, WindowEnd: end}
	return o.reader.GetGroupWeeklyBriefForWindow(ctx, committeeUID, window)
}
