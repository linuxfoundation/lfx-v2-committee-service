// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAcceptInviteEmptyBodyMiddleware(t *testing.T) {
	committeeUID := "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	inviteUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	acceptPath := "/committees/" + committeeUID + "/invites/" + inviteUID + "/accept"

	t.Run("injects empty JSON for missing accept body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", nil)
		body := serveThroughMiddleware(req)
		if body != "{}" {
			t.Fatalf("expected injected body {}, got %q", body)
		}
	})

	t.Run("preserves provided accept body", func(t *testing.T) {
		want := `{"organization":{"name":"LF"}}`
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", strings.NewReader(want))
		body := serveThroughMiddleware(req)
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
		body := serveThroughMiddleware(req)
		if body != "" {
			t.Fatalf("expected decline path left unchanged, got %q", body)
		}
	})
}

func serveThroughMiddleware(req *http.Request) string {
	var captured string
	handler := acceptInviteEmptyBodyMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		captured = string(bodyBytes)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return captured
}
