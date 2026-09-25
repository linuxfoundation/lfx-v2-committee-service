// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	goahttp "goa.design/goa/v3/http"
)

func TestAcceptInviteEmptyBodyMiddleware(t *testing.T) {
	committeeUID := "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	inviteUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	acceptPath := "/committees/" + committeeUID + "/invites/" + inviteUID + "/accept"

	t.Run("injects empty JSON for missing accept body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", nil)
		body := serveThroughMiddleware(t, req)
		if body != "{}" {
			t.Fatalf("expected injected body {}, got %q", body)
		}
	})

	t.Run("preserves provided accept body", func(t *testing.T) {
		want := `{"organization":{"name":"LF"}}`
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", strings.NewReader(want))
		body := serveThroughMiddleware(t, req)
		if body != want {
			t.Fatalf("expected body preserved, got %q", body)
		}
	})

	t.Run("ignores decline path", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPost,
			"/committees/"+committeeUID+"/invites/"+inviteUID+"/decline?v=1",
			nil,
		)
		body := serveThroughMiddleware(t, req)
		if body != "" {
			t.Fatalf("expected decline path left unchanged, got %q", body)
		}
	})

	t.Run("rejects oversized accept body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", strings.NewReader(strings.Repeat("x", acceptInviteMaxBodyBytes+1)))
		rec := httptest.NewRecorder()
		acceptInviteEmptyBodyMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", rec.Code)
		}
	})
}

// TestJoinCommitteeBodylessTransport pins the wire contract at the transport layer:
// raw HTTP clients (e.g. the Self Serve UI) may omit the join body entirely, and the
// generated server decoder must accept the empty body (io.EOF) and produce a payload
// carrying no organization. Goa-generated Go clients and the generated CLI cannot omit
// the body (they must send at least "{}") — an upstream Goa codegen limitation
// documented on JoinCommitteeOptionalBody — but the server side accepts omission.
func TestJoinCommitteeBodylessTransport(t *testing.T) {
	committeeUID := "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	joinPath := "/committees/" + committeeUID + "/join"

	decode := func(t *testing.T, req *http.Request) (*committeeservice.JoinCommitteePayload, error) {
		t.Helper()
		var (
			payload *committeeservice.JoinCommitteePayload
			decErr  error
		)
		mux := goahttp.NewMuxer()
		committeeservicesvr.MountJoinCommitteeHandler(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload, decErr = committeeservicesvr.DecodeJoinCommitteeRequest(mux, goahttp.RequestDecoder)(r)
		}))
		mux.ServeHTTP(httptest.NewRecorder(), req)
		return payload, decErr
	}

	t.Run("decodes a bodyless join request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, joinPath+"?v=1", nil)
		payload, err := decode(t, req)
		if err != nil {
			t.Fatalf("expected bodyless join request to decode, got error %v", err)
		}
		if payload == nil {
			t.Fatal("expected a decoded payload, got nil")
		}
		if payload.UID != committeeUID {
			t.Fatalf("expected committee uid %q, got %q", committeeUID, payload.UID)
		}
		if payload.Body != nil && payload.Body.Organization != nil {
			t.Fatalf("expected no organization on a bodyless join, got %+v", payload.Body.Organization)
		}
	})

	t.Run("decodes a join request carrying an organization body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, joinPath+"?v=1",
			strings.NewReader(`{"organization":{"name":"Example Org","website":"https://example.com"}}`))
		req.Header.Set("Content-Type", "application/json")
		payload, err := decode(t, req)
		if err != nil {
			t.Fatalf("expected join request with body to decode, got error %v", err)
		}
		if payload == nil || payload.Body == nil || payload.Body.Organization == nil {
			t.Fatalf("expected organization on the decoded payload, got %+v", payload)
		}
		if got := *payload.Body.Organization.Name; got != "Example Org" {
			t.Fatalf("expected organization name %q, got %q", "Example Org", got)
		}
		if got := *payload.Body.Organization.Website; got != "https://example.com" {
			t.Fatalf("expected organization website %q, got %q", "https://example.com", got)
		}
	})
}

func serveThroughMiddleware(t *testing.T, req *http.Request) string {
	var captured string
	rec := httptest.NewRecorder()
	acceptInviteEmptyBodyMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		captured = string(bodyBytes)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected middleware to succeed, got status %d body %q", rec.Code, rec.Body.String())
	}
	return captured
}
