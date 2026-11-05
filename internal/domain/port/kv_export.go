// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeExport is a committee as the KV store holds it: the base and the settings, each with the
// revision it was read at. Settings is nil when the committee has none.
type CommitteeExport struct {
	Base             *model.CommitteeBase
	BaseRevision     uint64
	Settings         *model.CommitteeSettings
	SettingsRevision uint64
}

// KVExportReader streams the KV store's records together with the revision each one was read at, for
// the tooling that copies the data to another store and compares the two. It is specific to KV and goes
// away with it. A record that cannot be read is logged and skipped while the scan goes on, and the scan
// returns an aggregated error at the end, so a partial scan is never mistaken for a complete one; the
// first error fn returns stops the scan at once.
type KVExportReader interface {
	// ListAllUIDs returns the uid of every committee.
	ListAllUIDs(ctx context.Context) ([]string, error)
	// EachCommitteeExport streams every committee with its settings.
	EachCommitteeExport(ctx context.Context, fn func(CommitteeExport) error) error
	// EachMemberExport streams every committee member.
	EachMemberExport(ctx context.Context, fn func(member *model.CommitteeMember, revision uint64) error) error
}
