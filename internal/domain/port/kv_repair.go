// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// KVRepairStore is the KV store as the tooling that copies the data back from PostgreSQL uses it: the records it
// reads to see what KV holds, with the revision each has there, and the writes that create or replace one. It never
// deletes. It is specific to KV and goes away with it.
type KVRepairStore interface {
	GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
	GetSettings(ctx context.Context, uid string) (*model.CommitteeSettings, uint64, error)
	Create(ctx context.Context, committee *model.Committee) error
	UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error
	UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error

	GetMember(ctx context.Context, uid string) (*model.CommitteeMember, uint64, error)
	CreateMember(ctx context.Context, member *model.CommitteeMember) error
	UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error)

	GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error)
	CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error
	UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error

	GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error)
	CreateApplication(ctx context.Context, application *model.CommitteeApplication) error
	UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error

	GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error)
	CreateLink(ctx context.Context, link *model.CommitteeLink) error
	UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error
	GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error)
	CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error
	UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error

	GetDocumentMetadata(ctx context.Context, committeeUID, documentUID string) (*model.CommitteeDocument, uint64, error)
	CreateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument) error
	UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error

	GetGroupWeeklyBriefForWindow(ctx context.Context, committeeUID string, window model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error)
	PutGroupWeeklyBrief(ctx context.Context, brief *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error)
	GetGroupWeeklyBriefThrottle(ctx context.Context, committeeUID string, windowStart time.Time) (*model.GroupWeeklyBriefThrottle, error)
	PutGroupWeeklyBriefThrottle(ctx context.Context, throttle *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error)
}
