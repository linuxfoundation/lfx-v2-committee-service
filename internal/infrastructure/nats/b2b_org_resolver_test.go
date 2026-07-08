// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func TestB2BOrgResolver_ResolveByUID(t *testing.T) {
	const wantSFID = "0014100000Te2ovAAB"

	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	sub, err := nc.Subscribe(constants.MemberB2BOrgLookupSubject, func(msg *nats.Msg) {
		_ = msg.Respond([]byte(`{"id":"` + wantSFID + `"}`))
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	client, err := NewClient(context.Background(), Config{URL: nats.DefaultURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	resolver := NewB2BOrgResolver(client)
	sfid, ok, err := resolver.ResolveByUID(context.Background(), wantSFID)
	if err != nil {
		t.Fatalf("ResolveByUID error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok")
	}
	if sfid != wantSFID {
		t.Fatalf("sfid = %q", sfid)
	}
}

func TestB2BOrgResolver_ResolveByUID_notFound(t *testing.T) {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	sub, err := nc.Subscribe(constants.MemberB2BOrgLookupSubject, func(msg *nats.Msg) {
		_ = msg.Respond([]byte(`{"error":"b2b org not found"}`))
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	client, err := NewClient(context.Background(), Config{URL: nats.DefaultURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	resolver := NewB2BOrgResolver(client)
	_, ok, err := resolver.ResolveByUID(context.Background(), "51fde723-67df-4e0e-91c6-936d01d59559")
	if err != nil {
		t.Fatalf("ResolveByUID error: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestB2BOrgResolver_ResolveByUID_lookupFailed(t *testing.T) {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	sub, err := nc.Subscribe(constants.MemberB2BOrgLookupSubject, func(msg *nats.Msg) {
		_ = msg.Respond([]byte(`{"error":"b2b org lookup failed"}`))
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	client, err := NewClient(context.Background(), Config{URL: nats.DefaultURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	resolver := NewB2BOrgResolver(client)
	_, ok, err := resolver.ResolveByUID(context.Background(), "0014100000Te2ovAAB")
	if err == nil {
		t.Fatal("expected lookup error")
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestB2BOrgLookupResponse_decode(t *testing.T) {
	var resp b2bOrgLookupResponse
	if err := json.Unmarshal([]byte(`{"id":"0014100000Te2ovAAB"}`), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "0014100000Te2ovAAB" {
		t.Fatalf("id = %q", resp.ID)
	}
}
