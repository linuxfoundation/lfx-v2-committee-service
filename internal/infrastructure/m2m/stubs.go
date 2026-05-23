// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// EmptyMailingListSource is a placeholder MailingListSource that returns no
// activity. Use until the upstream mailing-list service publishes a stable
// query contract.
type EmptyMailingListSource struct{}

// NewEmptyMailingListSource returns the stub source.
func NewEmptyMailingListSource() *EmptyMailingListSource { return &EmptyMailingListSource{} }

// ListMailingListActivityForWindow implements port.MailingListSource.
// TODO(contract-TBD): wire M2M when contract defined.
func (e *EmptyMailingListSource) ListMailingListActivityForWindow(_ context.Context, _ string, _, _ time.Time) ([]port.MailingListActivity, error) {
	return nil, nil
}

// EmptyVoteSource is the placeholder VoteSource counterpart to
// EmptyMailingListSource.
type EmptyVoteSource struct{}

// NewEmptyVoteSource returns the stub source.
func NewEmptyVoteSource() *EmptyVoteSource { return &EmptyVoteSource{} }

// ListVoteActivityForWindow implements port.VoteSource.
// TODO(contract-TBD): wire M2M when contract defined.
func (e *EmptyVoteSource) ListVoteActivityForWindow(_ context.Context, _ string, _, _ time.Time) ([]port.VoteActivity, error) {
	return nil, nil
}
