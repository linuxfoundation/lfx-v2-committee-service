// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func setupB2BOrgResolverTest(t *testing.T, responder func(*nats.Msg) []byte) port.B2BOrgResolver {
	t.Helper()

	_, url := startTestNATSServer(t)

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	_, err = nc.Subscribe(constants.MemberB2BOrgLookupSubject, func(msg *nats.Msg) {
		_ = msg.Respond(responder(msg))
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	return NewB2BOrgResolver(&NATSClient{
		conn:    nc,
		timeout: 2 * time.Second,
	})
}

func TestB2BOrgResolver_ResolveByUID(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"

	resolver := setupB2BOrgResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"id":"` + wantSFID + `"}`)
	})

	sfid, ok, err := resolver.ResolveByUID(context.Background(), wantSFID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, wantSFID, sfid)
}

func TestB2BOrgResolver_ResolveByUID_notFound(t *testing.T) {
	resolver := setupB2BOrgResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"error":"b2b org not found"}`)
	})

	_, ok, err := resolver.ResolveByUID(context.Background(), "51fde723-67df-4e0e-91c6-936d01d59559")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestB2BOrgResolver_ResolveByUID_lookupFailed(t *testing.T) {
	resolver := setupB2BOrgResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"error":"b2b org lookup failed"}`)
	})

	_, ok, err := resolver.ResolveByUID(context.Background(), "0014100000Te2ovAAB")
	require.Error(t, err)
	require.False(t, ok)
}

func TestB2BOrgLookupResponse_decode(t *testing.T) {
	var resp b2bOrgLookupResponse
	require.NoError(t, json.Unmarshal([]byte(`{"id":"0014100000Te2ovAAB"}`), &resp))
	require.Equal(t, "0014100000Te2ovAAB", resp.ID)
}
