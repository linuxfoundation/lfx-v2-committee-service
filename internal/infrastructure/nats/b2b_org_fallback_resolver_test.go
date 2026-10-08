// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func setupB2BOrgFallbackResolverTest(t *testing.T, responder func(*nats.Msg) []byte) port.B2BOrgFallbackResolver {
	t.Helper()

	_, url := startTestNATSServer(t)

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	_, err = nc.Subscribe(constants.MemberB2BOrgLookupByWebsiteSubject, func(msg *nats.Msg) {
		_ = msg.Respond(responder(msg))
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	return NewB2BOrgFallbackResolver(&NATSClient{
		conn:    nc,
		timeout: 2 * time.Second,
	})
}

func TestB2BOrgFallbackResolver_ResolveSFID(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"

	resolver := setupB2BOrgFallbackResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"id":"` + wantSFID + `"}`)
	})

	sfid, ok, err := resolver.ResolveSFID(context.Background(), "Example Inc", "example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, wantSFID, sfid)
}

func TestB2BOrgFallbackResolver_ResolveSFID_notFound(t *testing.T) {
	resolver := setupB2BOrgFallbackResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"error":"b2b org not found"}`)
	})

	_, ok, err := resolver.ResolveSFID(context.Background(), "Unknown Org", "unknown.example")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestB2BOrgFallbackResolver_ResolveSFID_malformedSFID(t *testing.T) {
	resolver := setupB2BOrgFallbackResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"id":"not-a-real-sfid!"}`)
	})

	_, ok, err := resolver.ResolveSFID(context.Background(), "Example Inc", "example.com")
	require.Error(t, err, "a malformed sfid in the reply must be treated as an error, not a resolution")
	require.False(t, ok)
}

func TestB2BOrgFallbackResolver_ResolveSFID_lookupFailed(t *testing.T) {
	resolver := setupB2BOrgFallbackResolverTest(t, func(_ *nats.Msg) []byte {
		return []byte(`{"error":"b2b org lookup failed"}`)
	})

	_, ok, err := resolver.ResolveSFID(context.Background(), "Example Inc", "example.com")
	require.Error(t, err)
	require.False(t, ok)
}

func TestB2BOrgFallbackResolver_ResolveSFID_emptyInput(t *testing.T) {
	resolver := &b2bOrgFallbackResolver{client: &NATSClient{timeout: 2 * time.Second}}

	_, ok, err := resolver.ResolveSFID(context.Background(), "  ", "  ")
	require.NoError(t, err)
	require.False(t, ok)
}
