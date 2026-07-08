// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestB2BOrgResolver_ResolveSFIDByDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query/resources" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("type"); got != "b2b_org" {
			t.Fatalf("type = %q, want b2b_org", got)
		}
		cel := r.URL.Query().Get("cel_filter")
		if cel != `data.primary_domain == "linuxfoundation.org"` {
			t.Fatalf("unexpected cel_filter: %s", cel)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resources": []map[string]any{{
				"uid":  "0014100000Te2ovAAB",
				"data": map[string]any{"name": "The Linux Foundation"},
			}},
		})
	}))
	defer srv.Close()

	resolver := NewB2BOrgResolver(B2BOrgResolverConfig{BaseURL: srv.URL}, srv.Client())
	sfid, ok, err := resolver.ResolveSFID(context.Background(), "The Linux Foundation", "https://www.linuxfoundation.org")
	if err != nil {
		t.Fatalf("ResolveSFID error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok")
	}
	if sfid != "0014100000Te2ovAAB" {
		t.Fatalf("sfid = %q", sfid)
	}
}

func TestExtractPrimaryDomain(t *testing.T) {
	if got := extractPrimaryDomain("https://WWW.RedHat.COM/path"); got != "redhat.com" {
		t.Fatalf("got %q", got)
	}
}
