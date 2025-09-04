// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/fields"
)

// CommitteeReader defines the interface for committee read operations
type CommitteeReader interface {
	CommitteeDataReader
	CommitteeMemberDataReader
}

// CommitteeDataReader defines the interface for committee-specific read operations
type CommitteeDataReader interface {
	// GetBase retrieves committee base information by UID and returns the revision
	GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
	// GetSettings retrieves committee settings by UID and returns the revision
	GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error)
	// GetBaseAttributeValue retrieves an attribute value by UID and returns the revision
	GetBaseAttributeValue(ctx context.Context, uid string, attributeName string) (any, error)
}

// CommitteeMemberDataReader defines the interface for committee member read operations
type CommitteeMemberDataReader interface {
	// GetMember retrieves a committee member by committee UID and member UID
	GetMember(ctx context.Context, committeeUID, memberUID string) (*model.CommitteeMember, uint64, error)
	// ListMembers retrieves all members for a given committee UID
	ListMembers(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error)
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

// GetAttributeValue retrieves an attribute value by UID and returns the revision
func (rc *committeeReaderOrchestrator) GetBaseAttributeValue(ctx context.Context, uid string, attributeName string) (any, error) {

	committeeBase, _, err := rc.committeeReader.GetBase(ctx, uid)
	if err != nil {
		return nil, err
	}

	field, ok := fields.LookupByTag(committeeBase, "json", attributeName)
	if !ok {
		return nil, errors.New("attribute not found")
	}

	return field, nil
}

// GetMember retrieves a committee member by committee UID and member UID
func (rc *committeeReaderOrchestrator) GetMember(ctx context.Context, committeeUID, memberUID string) (*model.CommitteeMember, uint64, error) {

	slog.DebugContext(ctx, "executing get committee member use case",
		"committee_uid", committeeUID,
		"member_uid", memberUID,
	)

	// First, verify that the committee exists
	_, _, err := rc.committeeReader.GetBase(ctx, committeeUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee base - committee does not exist",
			"error", err,
			"committee_uid", committeeUID,
		)
		return nil, 0, err
	}

	// Get committee member from storage
	committeeMember, revision, err := rc.committeeReader.GetMember(ctx, memberUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee member",
			"error", err,
			"committee_uid", committeeUID,
			"member_uid", memberUID,
		)
		return nil, 0, err
	}

	// Verify that the member belongs to the requested committee
	if committeeMember.CommitteeUID != committeeUID {
		slog.ErrorContext(ctx, "committee member does not belong to the requested committee",
			"committee_uid", committeeUID,
			"member_uid", memberUID,
			"member_committee_uid", committeeMember.CommitteeUID,
		)
		return nil, 0, errs.NewValidation("committee member does not belong to the requested committee")
	}

	slog.DebugContext(ctx, "committee member retrieved successfully",
		"committee_uid", committeeUID,
		"member_uid", memberUID,
		"revision", revision,
	)

	return committeeMember, revision, nil
}

// ListMembers retrieves all members for a given committee UID
func (rc *committeeReaderOrchestrator) ListMembers(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {

	slog.DebugContext(ctx, "executing list committee members use case",
		"committee_uid", committeeUID,
	)

	// First, verify that the committee exists
	_, _, err := rc.committeeReader.GetBase(ctx, committeeUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get committee base - committee does not exist",
			"error", err,
			"committee_uid", committeeUID,
		)
		return nil, err
	}

	// Get all committee members from storage
	members, err := rc.committeeReader.ListMembers(ctx, committeeUID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list committee members",
			"error", err,
			"committee_uid", committeeUID,
		)
		return nil, err
	}

	slog.DebugContext(ctx, "committee members retrieved successfully",
		"committee_uid", committeeUID,
		"member_count", len(members),
	)

	return members, nil
}

// NewCommitteeReaderOrchestrator creates a new committee reader use case using the option pattern
func NewCommitteeReaderOrchestrator(opts ...committeeReaderOrchestratorOption) CommitteeReader {
	rc := &committeeReaderOrchestrator{}
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}
