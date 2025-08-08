// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// CommitteeReader defines the interface for committee read operations
type CommitteeReader interface {
	// GetBase retrieves committee base information by UID and returns the revision
	GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
	// GetSettings retrieves committee settings by UID and returns the revision
	GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error)
}

// committeeReaderOrchestratorOption defines a function type for setting options
type committeeReaderOrchestratorOption func(*committeeReaderOrchestrator)

// WithCommitteeReader sets the committee reader
func WithCommitteeReader(reader port.CommitteeReader) committeeReaderOrchestratorOption {
	return func(r *committeeReaderOrchestrator) {
		r.committeeReader = reader
	}
}

// committeeReaderOrchestrator orchestrates the committee reading process
type committeeReaderOrchestrator struct {
	committeeReader port.CommitteeReader
}

// GetBase retrieves committee base information by UID
func (rc *committeeReaderOrchestrator) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {

	slog.DebugContext(ctx, "executing get committee base use case",
		"committee_uid", uid,
	)

	// Get committee base from storage
	committeeBase, revision, err := rc.committeeReader.GetBase(ctx, uid)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee base",
			"error", err,
			"committee_uid", uid,
		)
		return nil, 0, err
	}

	slog.DebugContext(ctx, "committee base retrieved successfully",
		"committee_uid", uid,
		"committee_name", committeeBase.Name,
		"revision", revision,
	)

	return committeeBase, revision, nil
}

// GetSettings retrieves committee settings by UID
func (rc *committeeReaderOrchestrator) GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error) {

	slog.DebugContext(ctx, "executing get committee settings use case",
		"committee_uid", uid,
	)

	// Get committee settings from storage
	committeeSettings, revision, err := rc.committeeReader.GetSettings(ctx, uid)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee settings",
			"error", err,
			"committee_uid", uid,
		)
		return nil, 0, err
	}

	slog.DebugContext(ctx, "committee settings retrieved successfully",
		"committee_uid", uid,
		"revision", revision,
	)

	return committeeSettings, revision, nil
}

// NewCommitteeReaderOrchestrator creates a new committee reader use case using the option pattern
func NewCommitteeReaderOrchestrator(opts ...committeeReaderOrchestratorOption) CommitteeReader {
	rc := &committeeReaderOrchestrator{}
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}
