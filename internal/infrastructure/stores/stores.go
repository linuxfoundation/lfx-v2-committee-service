// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package stores builds the stores the API and the CLI read and write the committee data through, from the storage mode.
package stores

import (
	"context"
	"errors"
	"fmt"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
)

// Stores are the stores of the committee data for one storage mode. Each is an aggregate of its own, as the service uses
// them: the committees with their settings, members, invites and applications; the links and folders; the weekly briefs and
// their throttle entries; and the documents.
type Stores struct {
	Committees port.CommitteeReaderWriter
	Links      port.CommitteeLinkReaderWriter
	Briefs     composite.BriefBackend
	Documents  port.CommitteeDocumentReaderWriter
	// DocumentAudit is what the audit-sync commands repair links, folders and documents through. In kv mode it is nil and the
	// caller keeps using the KV storage, as it always has; in the other modes it is the composites of the links and the
	// documents, so a repair reaches both stores the mode names.
	DocumentAudit port.DocumentAuditSyncStorage
}

// auditSync is the audit-sync port over the composites of the links and of the documents.
type auditSync struct {
	*composite.Links
	*composite.Documents
}

// kvDocuments is the NATS side of the documents as the composite needs it: the metadata in KV and the files in the Object
// Store, with the lists and updates of the audit-sync tooling, and the lookups of a document by its uid alone.
type kvDocuments struct {
	port.CommitteeDocumentReaderWriter
	audit   port.DocumentAuditSyncStorage
	lookups interface {
		DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
		DocumentCommittee(ctx context.Context, documentUID string) (string, error)
	}
}

func (d kvDocuments) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	return d.audit.UpdateDocumentMetadata(ctx, doc, revision)
}

func (d kvDocuments) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	return d.audit.ListAllDocuments(ctx, committeeUID)
}

func (d kvDocuments) DocumentRevision(ctx context.Context, documentUID string) (uint64, error) {
	return d.lookups.DocumentRevision(ctx, documentUID)
}

func (d kvDocuments) DocumentCommittee(ctx context.Context, documentUID string) (string, error) {
	return d.lookups.DocumentCommittee(ctx, documentUID)
}

// Build returns the stores for the mode. In kv mode they are the NATS KV stores and nothing else: no composite is built,
// the PostgreSQL store is not needed and is not touched, and the service stores its data as it always has. The other modes
// need the PostgreSQL store, and wrap the KV stores and PostgreSQL's in the composite of each aggregate.
func Build(mode composite.Mode, client *nats.NATSClient, pg *postgres.Store, opts ...composite.Option) (*Stores, error) {
	if client == nil {
		return nil, errors.New("the NATS client is needed to build the stores")
	}
	files := nats.NewDocumentStorage(client)
	if mode == composite.ModeKV {
		// Exactly what the API has always used: one KV storage for the committees, links and briefs, and the document
		// storage beside it.
		committees := nats.NewStorage(client)
		links, ok := committees.(port.CommitteeLinkReaderWriter)
		if !ok {
			return nil, errors.New("the NATS storage does not implement the link reader and writer")
		}
		briefs, ok := committees.(composite.BriefBackend)
		if !ok {
			return nil, errors.New("the NATS storage does not implement the weekly brief reader and writer")
		}
		return &Stores{Committees: committees, Links: links, Briefs: briefs, Documents: files}, nil
	}
	kv := nats.NewStorageBackend(client)
	if pg == nil {
		return nil, fmt.Errorf("storage mode %s needs the PostgreSQL store", mode)
	}
	lookups, ok := files.(interface {
		DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
		DocumentCommittee(ctx context.Context, documentUID string) (string, error)
	})
	if !ok {
		return nil, errors.New("the NATS document storage does not look documents up by uid")
	}
	committees, err := composite.New(mode, kv, pg, opts...)
	if err != nil {
		return nil, err
	}
	cascading, err := composite.WithCascade(committees, pg)
	if err != nil {
		return nil, err
	}
	links, err := composite.NewLinks(mode, kv, pg, opts...)
	if err != nil {
		return nil, err
	}
	briefs, err := composite.NewBriefs(mode, kv, pg, opts...)
	if err != nil {
		return nil, err
	}
	documents, err := composite.NewDocuments(mode, kvDocuments{CommitteeDocumentReaderWriter: files, audit: kv, lookups: lookups}, pg, opts...)
	if err != nil {
		return nil, err
	}
	return &Stores{Committees: cascading, Links: links, Briefs: briefs, Documents: documents, DocumentAudit: auditSync{links, documents}}, nil
}
