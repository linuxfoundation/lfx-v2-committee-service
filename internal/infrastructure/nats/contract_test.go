// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"testing"
	"time"

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
	js := startJetStreamTestServer(t)
	url := js.Conn().ConnectedUrl()

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
	_, err := js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: constants.ObjectStoreNameCommitteeDocuments})
	require.NoError(t, err)

	client, err := NewClient(ctx, Config{URL: url, Timeout: 5 * time.Second, MaxReconnect: 3, ReconnectWait: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// The NATS KV store runs the same behavioral suites as the PostgreSQL store (see
// internal/infrastructure/postgres/contract_test.go).
//
// A lost compare-and-swap on a committee, its settings or a delete is reported as errs.Unexpected, not
// errs.Conflict as the PostgreSQL store does. That is a known defect of the KV store (the knowledge base
// records the mapping to 409 as the intended one, and the link, document and member-update writes already do it), kept
// visible here until it is fixed; see docs/postgres-schema.md.
//
// A read with an empty uid is the same kind of defect: the validation error is wrapped in an internal
// error, which the HTTP layer answers with a 500 where PostgreSQL answers 400.
func TestContract_Committee(t *testing.T) {
	kv := storetest.Backend{Name: "kv", StaleRevision: storetest.RequireUnexpected, EmptyUID: storetest.RequireUnexpected}
	storetest.RunCommitteeContract(t, kv, func(t *testing.T) port.CommitteeReaderWriter {
		return NewStorage(newKVClient(t))
	})
}
