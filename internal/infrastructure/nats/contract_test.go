// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

// newKVClient starts an in-process JetStream server with every bucket the service binds to, which
// the deployment creates ahead of time, and returns a client connected to it.
func newKVClient(t *testing.T) *NATSClient {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, JetStream: true, StoreDir: t.TempDir(),
	})
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(10*time.Second), "NATS server not ready")
	t.Cleanup(ns.Shutdown)

	ctx := context.Background()
	conn, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	js, err := jetstream.New(conn)
	require.NoError(t, err)
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
	conn.Close()

	client, err := NewClient(ctx, Config{URL: ns.ClientURL(), Timeout: 5 * time.Second, MaxReconnect: 3, ReconnectWait: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// The NATS KV store runs the same behavioral suites as the PostgreSQL store (see
// internal/infrastructure/postgres/contract_test.go).
func TestContract_Committee(t *testing.T) {
	storetest.RunCommitteeContract(t, // A stale revision is reported as an internal error, not a conflict: the stored KV error is the
		// JetStream "wrong last sequence" and the store wraps it as Unexpected (documented in
		// docs/postgres-schema.md, where the PostgreSQL store returns a conflict instead).
		storetest.Backend{Name: "kv", StaleRevision: storetest.RequireUnexpected}, func(t *testing.T) port.CommitteeReaderWriter {
			return NewStorage(newKVClient(t))
		})
}
