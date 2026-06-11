// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// GroupWeeklyBriefUpdateInput captures the chair edit/save request: the new
// brief body, the optimistic-concurrency token the caller read from
// GET /current, the editing principal, and the ambient "now" used to select
// the weekly window.
type GroupWeeklyBriefUpdateInput struct {
	CommitteeUID string
	BriefText    string
	Revision     uint64
	// EditedBy is the caller's Heimdall principal (LFX username); recorded as
	// last_edited_by. May be empty if the principal is unavailable.
	EditedBy string
	Now      time.Time
}

// GroupWeeklyBriefDataWriter is the use-case façade for chair edit/save of the
// current weekly brief. It is intentionally separate from the generator: edit
// and generate share the writer port and CAS concurrency token but are distinct
// use cases (one is a synchronous overwrite, the other a two-phase async flow).
type GroupWeeklyBriefDataWriter interface {
	// Update overwrites brief_text for the current window and transitions the
	// brief to the "edited" state, preserving source_refs and the
	// generator-owned fields. It enforces optimistic concurrency on Revision:
	//   - no brief for the window      → errors.NotFound (404)
	//   - empty brief_text             → errors.Validation (400)
	//   - stale Revision token         → errors.RevisionMismatch (409)
	// A read-then-write race that slips past the revision pre-check trips the
	// storage CAS; we re-read and surface it as RevisionMismatch (409) too, so a
	// conflict is consistently a 409 rather than a transient 503. On success it
	// returns the brief with its new revision.
	Update(ctx context.Context, in GroupWeeklyBriefUpdateInput) (*model.GroupWeeklyBrief, error)
}

type groupWeeklyBriefWriterOrchestrator struct {
	reader port.GroupWeeklyBriefReader
	writer port.GroupWeeklyBriefWriter
}

// GroupWeeklyBriefWriterOption configures the orchestrator.
type GroupWeeklyBriefWriterOption func(*groupWeeklyBriefWriterOrchestrator)

// WithGroupWeeklyBriefReaderForWriter injects the persistence-layer reader used
// to load the current brief before applying the edit.
func WithGroupWeeklyBriefReaderForWriter(r port.GroupWeeklyBriefReader) GroupWeeklyBriefWriterOption {
	return func(o *groupWeeklyBriefWriterOrchestrator) {
		o.reader = r
	}
}

// WithGroupWeeklyBriefWriterForWriter injects the persistence-layer writer used
// for the CAS write.
func WithGroupWeeklyBriefWriterForWriter(w port.GroupWeeklyBriefWriter) GroupWeeklyBriefWriterOption {
	return func(o *groupWeeklyBriefWriterOrchestrator) {
		o.writer = w
	}
}

// NewGroupWeeklyBriefWriterOrchestrator builds a GroupWeeklyBriefDataWriter.
func NewGroupWeeklyBriefWriterOrchestrator(opts ...GroupWeeklyBriefWriterOption) GroupWeeklyBriefDataWriter {
	o := &groupWeeklyBriefWriterOrchestrator{}
	for _, opt := range opts {
		opt(o)
	}
	if o.reader == nil {
		panic("group weekly brief reader is required")
	}
	if o.writer == nil {
		panic("group weekly brief writer is required")
	}
	return o
}

func (o *groupWeeklyBriefWriterOrchestrator) Update(ctx context.Context, in GroupWeeklyBriefUpdateInput) (*model.GroupWeeklyBrief, error) {
	if strings.TrimSpace(in.CommitteeUID) == "" {
		return nil, errors.NewValidation("committee_uid is required")
	}
	// Validate input before touching storage: empty body is a client error
	// regardless of whether a brief exists.
	if strings.TrimSpace(in.BriefText) == "" {
		return nil, errors.NewValidation("brief_text is required")
	}

	// Load the current brief for the window. The reader stamps current.Revision
	// with the live KV revision, which we use both for the concurrency check and
	// as the CAS precondition on the write.
	start, end := model.WeeklyWindow(in.Now)
	window := model.GroupWeeklyBrief{WindowStart: start, WindowEnd: end}
	current, _, err := o.reader.GetGroupWeeklyBriefForWindow(ctx, in.CommitteeUID, window)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, errors.NewNotFound("no weekly brief exists for the current window")
	}
	if current.Revision != in.Revision {
		return nil, errors.NewRevisionMismatch(current.Revision)
	}

	// Apply the edit, preserving source_refs and every generator-owned field.
	// State moves to "edited" so the generator's edited-guard blocks an
	// accidental regenerate without force. UpdatedAt is stamped by the storage
	// layer; we only set the edit-audit fields here. current.Revision (> 0)
	// drives the CAS write.
	current.BriefText = in.BriefText
	current.State = model.GroupWeeklyBriefStateEdited
	current.LastEditedAt = in.Now.UTC()
	current.LastEditedBy = in.EditedBy

	updated, err := o.writer.PutGroupWeeklyBrief(ctx, current)
	if err != nil {
		// The storage layer maps a CAS conflict to a generic ServiceUnavailable,
		// indistinguishable by type from a real infra error (NATS down, index
		// write failure). Re-read to disambiguate: if the live revision has moved
		// past the token we wrote against, a concurrent edit landed between the
		// pre-check and the CAS write — surface a consistent 409 carrying the new
		// revision so clients handle it exactly like the pre-check conflict.
		// Otherwise it is a genuine failure — propagate it unchanged.
		if latest, _, reReadErr := o.reader.GetGroupWeeklyBriefForWindow(ctx, in.CommitteeUID, window); reReadErr == nil && latest != nil && latest.Revision != in.Revision {
			return nil, errors.NewRevisionMismatch(latest.Revision)
		}
		return nil, err
	}
	return updated, nil
}
