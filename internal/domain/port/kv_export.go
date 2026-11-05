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
	// EachInviteExport streams every committee invite.
	EachInviteExport(ctx context.Context, fn func(invite *model.CommitteeInvite, revision uint64) error) error
	// EachApplicationExport streams every committee application.
	EachApplicationExport(ctx context.Context, fn func(application *model.CommitteeApplication, revision uint64) error) error
	// EachLinkExport streams every committee link.
	EachLinkExport(ctx context.Context, fn func(link *model.CommitteeLink, revision uint64) error) error
	// EachFolderExport streams every committee link folder.
	EachFolderExport(ctx context.Context, fn func(folder *model.CommitteeLinkFolder, revision uint64) error) error
	// EachDocumentExport streams the metadata of every committee document; the files stay in the Object Store.
	EachDocumentExport(ctx context.Context, fn func(document *model.CommitteeDocument, revision uint64) error) error
	// EachBriefExport streams every weekly brief, with Revision set. The briefs the window index serves come
	// first, so that when a race left two briefs for one window the one that was served is the one a copy keeps.
	EachBriefExport(ctx context.Context, fn func(brief *model.GroupWeeklyBrief, revision uint64) error) error
	// EachThrottleExport streams every weekly-brief throttle entry, with Revision set.
	EachThrottleExport(ctx context.Context, fn func(throttle *model.GroupWeeklyBriefThrottle, revision uint64) error) error
}
