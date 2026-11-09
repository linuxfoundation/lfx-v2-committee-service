// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package natstest gives the tests of other packages a NATS KV store on an in-process JetStream server.
// It is imported only from _test.go files, so it never links into the binaries. The tests of the nats
// package itself use their own helpers, which this one mirrors: that package cannot import it.
package natstest

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

// Backend is the KV store as every aggregate of the service sees it.
type Backend interface {
	port.CommitteeReaderWriter
	port.CommitteeLinkReaderWriter
	port.DocumentAuditSyncStorage
	port.GroupWeeklyBriefReader
	port.GroupWeeklyBriefWriter
}

// NewStore returns the KV store on a JetStream server of its own, with every bucket the service binds to, which a
// deployment creates ahead of time. The server is shut down when the test ends.
func NewStore(t *testing.T) port.CommitteeReaderWriter {
	t.Helper()
	return NewBackend(t)
}

// NewBackend is NewStore for the tests of the aggregates beyond the committees.
func NewBackend(t *testing.T) Backend {
	t.Helper()
	return nats.NewStorageBackend(newClient(t))
}

// RepairBackend is the KV store as the copy back from PostgreSQL uses it, with the export that checks the copy.
type RepairBackend interface {
	port.KVRepairStore
	port.KVExportReader
}

type repairBackend struct {
	port.KVRepairStore
	port.KVExportReader
}

// NewRepairBackend is NewBackend for the tests of the copy back from PostgreSQL.
func NewRepairBackend(t *testing.T) RepairBackend {
	t.Helper()
	client := newClient(t)
	return repairBackend{KVRepairStore: nats.NewKVRepairStore(client), KVExportReader: nats.NewStorageBackend(client)}
}

// DocumentBackend is the KV and Object Store as the documents see them: the metadata and the files, and the lists and
// updates of the audit-sync tooling.
type DocumentBackend interface {
	port.CommitteeDocumentReaderWriter
	UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error
	ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error)
	DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
	DocumentCommittee(ctx context.Context, documentUID string) (string, error)
}

type documentBackend struct {
	port.CommitteeDocumentReaderWriter
	audit port.DocumentAuditSyncStorage
	// lookups are the lookups of a document by uid alone, which the document storage has beside its port.
	lookups interface {
		DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
		DocumentCommittee(ctx context.Context, documentUID string) (string, error)
	}
}

func (d documentBackend) DocumentRevision(ctx context.Context, documentUID string) (uint64, error) {
	return d.lookups.DocumentRevision(ctx, documentUID)
}

func (d documentBackend) DocumentCommittee(ctx context.Context, documentUID string) (string, error) {
	return d.lookups.DocumentCommittee(ctx, documentUID)
}

func (d documentBackend) UpdateDocumentMetadata(ctx context.Context, doc *model.CommitteeDocument, revision uint64) error {
	return d.audit.UpdateDocumentMetadata(ctx, doc, revision)
}

func (d documentBackend) ListAllDocuments(ctx context.Context, committeeUID string) ([]*model.CommitteeDocument, error) {
	return d.audit.ListAllDocuments(ctx, committeeUID)
}

// NewDocumentBackend is NewBackend for the tests of the documents, over a server of its own.
func NewDocumentBackend(t *testing.T) DocumentBackend {
	t.Helper()
	client := newClient(t)
	docs := nats.NewDocumentStorage(client)
	return documentBackend{
		CommitteeDocumentReaderWriter: docs, audit: nats.NewStorageBackend(client),
		lookups: docs.(interface {
			DocumentRevision(ctx context.Context, documentUID string) (uint64, error)
			DocumentCommittee(ctx context.Context, documentUID string) (string, error)
		}),
	}
}

// NewClient returns a client of an in-process NATS server with the committee buckets, for the tests of what is built over the
// stores. The server is shut down when the test ends.
func NewClient(t *testing.T) *nats.NATSClient {
	t.Helper()
	return newClient(t)
}

func newClient(t *testing.T) *nats.NATSClient {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		NoLog:     true,
		NoSigs:    true,
		JetStream: true,
		StoreDir:  t.TempDir(),
	})
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(4*time.Second), "NATS server not ready")
	t.Cleanup(ns.Shutdown)

	conn, err := natsgo.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	js, err := jetstream.New(conn)
	require.NoError(t, err)

	ctx := context.Background()
	for _, bucket := range []string{
		constants.KVBucketNameCommittees,
		constants.KVBucketNameCommitteeSettings,
		constants.KVBucketNameCommitteeMembers,
		constants.KVBucketNameCommitteeInvites,
		constants.KVBucketNameCommitteeApplications,
		constants.KVBucketNameCommitteeLinks,
		constants.KVBucketNameCommitteeFolders,
		constants.KVBucketNameCommitteeDocuments,
		constants.KVBucketNameGroupWeeklyBriefs,
		constants.KVBucketNameGroupWeeklyBriefUIDIndex,
		constants.KVBucketNameGroupWeeklyBriefThrottle,
	} {
		_, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: bucket, History: 5})
		require.NoError(t, err, bucket)
	}
	_, err = js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: constants.ObjectStoreNameCommitteeDocuments})
	require.NoError(t, err)

	client, err := nats.NewClient(ctx, nats.Config{URL: ns.ClientURL(), Timeout: 5 * time.Second, MaxReconnect: 3, ReconnectWait: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}
