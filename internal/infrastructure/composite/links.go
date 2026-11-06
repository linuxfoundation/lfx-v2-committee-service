// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// LinkBackend is what the composite needs of a store of the committee links and folders: the reader and writer of the
// request path, and the lists and updates the audit-sync tooling uses.
type LinkBackend interface {
	port.CommitteeLinkReaderWriter
	ListAllLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error)
	ListAllLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error)
	UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error
	UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error
}

// Links is the composite of the committee links and link folders, built like Store: the primary store, the one the mode
// makes authoritative, decides every result and is embedded, so every method not overridden goes to it unchanged; in dual
// mode each write is copied to PostgreSQL from what KV holds after it, with the revision KV gave it, and a sampled fraction
// of reads is compared; in dual-pg mode the roles are the other way round. See Store for what that guarantees. The lists
// of the audit-sync tooling go to the primary store alone.
type Links struct {
	LinkBackend
	core
	// src is the primary store the copy reads and dst the secondary store it writes; see Store.
	src, dst LinkBackend
}

var _ LinkBackend = (*Links)(nil)

// NewLinks returns the composite of the links and folders for the mode; see New.
func NewLinks(mode Mode, kv, pg LinkBackend, opts ...Option) (*Links, error) {
	primary, err := selectPrimary(mode, kv, pg)
	if err != nil {
		return nil, err
	}
	l := &Links{LinkBackend: primary, core: newCore(mode, opts), src: kv, dst: pg}
	if mode == ModeDualPG {
		l.src, l.dst = pg, kv
	}
	return l, nil
}

// CreateLink persists a link.
func (l *Links) CreateLink(ctx context.Context, link *model.CommitteeLink) error {
	if err := l.LinkBackend.CreateLink(ctx, link); err != nil {
		return err
	}
	if l.mode.HasSecondary() {
		committeeUID, uid := link.CommitteeUID, link.UID
		l.replicate(ctx, "create_link", uid, func(ctx context.Context) error {
			return copyCreated(ctx, &l.core, func(ctx context.Context) (*model.CommitteeLink, uint64, error) {
				return l.src.GetLink(ctx, committeeUID, uid)
			},
				l.dst.CreateLink, func(ctx context.Context, revision uint64) error {
					return l.deleteLinkInSecondary(ctx, committeeUID, uid, revision)
				},
				func(ctx context.Context) error { return l.copyLinkUpdate(ctx, committeeUID, uid) })
		})
	}
	return nil
}

// UpdateLink replaces the link found by its uid, guarded by the revision.
func (l *Links) UpdateLink(ctx context.Context, link *model.CommitteeLink, revision uint64) error {
	if err := l.LinkBackend.UpdateLink(ctx, link, revision); err != nil {
		return err
	}
	if l.mode.HasSecondary() {
		committeeUID, uid := link.CommitteeUID, link.UID
		l.replicate(ctx, "update_link", uid, func(ctx context.Context) error { return l.copyLinkUpdate(ctx, committeeUID, uid) })
	}
	return nil
}

// copyLinkUpdate writes the link the primary store holds now to the secondary store; see copyUpdated.
func (l *Links) copyLinkUpdate(ctx context.Context, committeeUID, uid string) error {
	return copyUpdated(ctx, &l.core, func(ctx context.Context) (*model.CommitteeLink, uint64, error) {
		return l.src.GetLink(ctx, committeeUID, uid)
	},
		func(ctx context.Context) (*model.CommitteeLink, uint64, error) {
			return l.dst.GetLink(ctx, committeeUID, uid)
		},
		l.dst.UpdateLink)
}

// DeleteLink removes the link when the revision is still current.
func (l *Links) DeleteLink(ctx context.Context, committeeUID, linkUID string, revision uint64) error {
	if err := l.LinkBackend.DeleteLink(ctx, committeeUID, linkUID, revision); err != nil {
		return err
	}
	if l.mode.HasSecondary() {
		l.replicate(ctx, "delete_link", linkUID, func(ctx context.Context) error { return l.deleteLinkInSecondary(ctx, committeeUID, linkUID, revision) })
	}
	return nil
}

// deleteLinkInSecondary removes the link from the secondary store after the primary store decided that it is gone; see deleteConverged.
func (l *Links) deleteLinkInSecondary(ctx context.Context, committeeUID, uid string, revision uint64) error {
	return deleteConverged(ctx, &l.core,
		func(ctx context.Context, revision uint64) error {
			return l.dst.DeleteLink(ctx, committeeUID, uid, revision)
		},
		func(ctx context.Context) (uint64, error) {
			_, current, err := l.dst.GetLink(ctx, committeeUID, uid)
			return current, err
		}, revision)
}

// CreateLinkFolder creates a folder.
func (l *Links) CreateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder) error {
	if err := l.LinkBackend.CreateLinkFolder(ctx, folder); err != nil {
		return err
	}
	if l.mode.HasSecondary() {
		committeeUID, uid := folder.CommitteeUID, folder.UID
		l.replicate(ctx, "create_link_folder", uid, func(ctx context.Context) error {
			return copyCreated(ctx, &l.core, func(ctx context.Context) (*model.CommitteeLinkFolder, uint64, error) {
				return l.src.GetLinkFolder(ctx, committeeUID, uid)
			}, l.dst.CreateLinkFolder, func(ctx context.Context, revision uint64) error {
				return l.deleteFolderInSecondary(ctx, committeeUID, uid, revision)
			}, func(ctx context.Context) error { return l.copyFolderUpdate(ctx, committeeUID, uid) })
		})
	}
	return nil
}

// UpdateLinkFolder replaces the folder found by its uid, guarded by the revision.
func (l *Links) UpdateLinkFolder(ctx context.Context, folder *model.CommitteeLinkFolder, revision uint64) error {
	if err := l.LinkBackend.UpdateLinkFolder(ctx, folder, revision); err != nil {
		return err
	}
	if l.mode.HasSecondary() {
		committeeUID, uid := folder.CommitteeUID, folder.UID
		l.replicate(ctx, "update_link_folder", uid, func(ctx context.Context) error { return l.copyFolderUpdate(ctx, committeeUID, uid) })
	}
	return nil
}

// copyFolderUpdate writes the folder the primary store holds now to the secondary store; see copyUpdated.
func (l *Links) copyFolderUpdate(ctx context.Context, committeeUID, uid string) error {
	return copyUpdated(ctx, &l.core, func(ctx context.Context) (*model.CommitteeLinkFolder, uint64, error) {
		return l.src.GetLinkFolder(ctx, committeeUID, uid)
	},
		func(ctx context.Context) (*model.CommitteeLinkFolder, uint64, error) {
			return l.dst.GetLinkFolder(ctx, committeeUID, uid)
		}, l.dst.UpdateLinkFolder)
}

// DeleteLinkFolder removes the folder when the revision is still current. The links in it are left alone.
func (l *Links) DeleteLinkFolder(ctx context.Context, committeeUID, folderUID string, revision uint64) error {
	if err := l.LinkBackend.DeleteLinkFolder(ctx, committeeUID, folderUID, revision); err != nil {
		return err
	}
	if l.mode.HasSecondary() {
		l.replicate(ctx, "delete_link_folder", folderUID, func(ctx context.Context) error {
			return l.deleteFolderInSecondary(ctx, committeeUID, folderUID, revision)
		})
	}
	return nil
}

// deleteFolderInSecondary removes the folder from the secondary store after the primary store decided that it is gone; see deleteConverged.
func (l *Links) deleteFolderInSecondary(ctx context.Context, committeeUID, uid string, revision uint64) error {
	return deleteConverged(ctx, &l.core,
		func(ctx context.Context, revision uint64) error {
			return l.dst.DeleteLinkFolder(ctx, committeeUID, uid, revision)
		},
		func(ctx context.Context) (uint64, error) {
			_, current, err := l.dst.GetLinkFolder(ctx, committeeUID, uid)
			return current, err
		}, revision)
}

// GetLink returns the link and its revision.
func (l *Links) GetLink(ctx context.Context, committeeUID, linkUID string) (*model.CommitteeLink, uint64, error) {
	link, revision, err := l.LinkBackend.GetLink(ctx, committeeUID, linkUID)
	if l.sampled() {
		l.compareRecord(ctx, "get_link", link, revision, err, func(ctx context.Context) (any, uint64, error) {
			return l.dst.GetLink(ctx, committeeUID, linkUID)
		})
	}
	return link, revision, err
}

// ListLinks returns the committee's links.
func (l *Links) ListLinks(ctx context.Context, committeeUID string) ([]*model.CommitteeLink, error) {
	links, err := l.LinkBackend.ListLinks(ctx, committeeUID)
	if l.sampled() {
		compareRecords(ctx, l, "list_links", links, err, func(ctx context.Context) ([]*model.CommitteeLink, error) {
			return l.dst.ListLinks(ctx, committeeUID)
		})
	}
	return links, err
}

// GetLinkFolder returns the folder and its revision.
func (l *Links) GetLinkFolder(ctx context.Context, committeeUID, folderUID string) (*model.CommitteeLinkFolder, uint64, error) {
	folder, revision, err := l.LinkBackend.GetLinkFolder(ctx, committeeUID, folderUID)
	if l.sampled() {
		l.compareRecord(ctx, "get_link_folder", folder, revision, err, func(ctx context.Context) (any, uint64, error) {
			return l.dst.GetLinkFolder(ctx, committeeUID, folderUID)
		})
	}
	return folder, revision, err
}

// ListLinkFolders returns the committee's folders.
func (l *Links) ListLinkFolders(ctx context.Context, committeeUID string) ([]*model.CommitteeLinkFolder, error) {
	folders, err := l.LinkBackend.ListLinkFolders(ctx, committeeUID)
	if l.sampled() {
		compareRecords(ctx, l, "list_link_folders", folders, err, func(ctx context.Context) ([]*model.CommitteeLinkFolder, error) {
			return l.dst.ListLinkFolders(ctx, committeeUID)
		})
	}
	return folders, err
}
